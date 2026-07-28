package main

import (
	"fmt"
	"log"
	"os/exec"
	"sort"
	"sync"
	"time"
)

// Manager 维护所有隧道，负责分配槽位与端口。
type Manager struct {
	mu        sync.RWMutex
	tunnels   map[int]*Tunnel
	nodes     []Node
	fetched   time.Time
	workDir   string
	socksBind string
	maxSlots  int
	jobs      JobStore
}

func NewManager(maxSlots int, workDir, socksBind string) *Manager {
	return &Manager{
		tunnels:   map[int]*Tunnel{},
		workDir:   workDir,
		socksBind: socksBind,
		maxSlots:  maxSlots,
	}
}

// RefreshNodes 重新拉取节点列表。
func (m *Manager) RefreshNodes() (int, error) {
	nodes, err := fetchNodes(60 * time.Second)
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	m.nodes = nodes
	m.fetched = time.Now()
	m.mu.Unlock()
	return len(nodes), nil
}

func (m *Manager) Nodes() ([]Node, time.Time) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Node, len(m.nodes))
	copy(out, m.nodes)
	return out, m.fetched
}

func (m *Manager) Tunnels() []*Tunnel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Tunnel, 0, len(m.tunnels))
	for _, t := range m.tunnels {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out
}

// freeSlot 找一个未占用的槽位。槽位同时决定端口与网段。
func (m *Manager) freeSlot() (int, error) {
	for i := 1; i <= m.maxSlots; i++ {
		if _, used := m.tunnels[i]; !used {
			return i, nil
		}
	}
	return 0, fmt.Errorf("槽位已满（上限 %d）", m.maxSlots)
}

// Start 为指定节点开一条隧道，返回分配到的本地端口。
func (m *Manager) Start(node Node) (*Tunnel, error) {
	m.mu.Lock()
	slot, err := m.freeSlot()
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	// 端口随机取，避免固定规律撞上机器上的其他服务
	taken := map[int]bool{}
	for _, other := range m.tunnels {
		taken[other.snapshot().Port] = true
	}
	port, err := freeRandomPort(taken)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	t := &Tunnel{
		Slot:        slot,
		Port:        port,
		BindAddress: m.socksBind,
		Node:        node,
		Status:      "starting",
		Since:       time.Now(),
	}
	m.tunnels[slot] = t
	m.mu.Unlock()

	go m.bringUp(t, true)
	return t, nil
}

// bringUp 把一条隧道拉起来。
//
// notify 决定成功后是否立刻重建后端配置。换节点重连时要传 false：
// 那条路径随后会调 rebind/resync 把入站改绑到新节点，在那之前重建配置
// 会因为入站还指着旧节点名而把路由规则丢掉。
func (m *Manager) bringUp(t *Tunnel, notify bool) {
	t.opMu.Lock()
	defer t.opMu.Unlock()
	m.bringUpLocked(t, notify)
}

// bringUpRestored reconnects a persisted tunnel. Unlike a newly created
// tunnel, existing inbounds may still point at the saved VPN Gate hostname.
// If fallback selects another node, migrate those bindings before reporting up.
func (m *Manager) bringUpRestored(t *Tunnel, oldHost string) {
	t.opMu.Lock()
	defer t.opMu.Unlock()
	m.bringUpLocked(t, false)
	if t.snapshot().Status != "vpn_up" {
		return
	}
	if err := m.syncRestoredTunnel(t, oldHost); err != nil {
		t.setState("sync_failed", "VPN 已连接，但 Mihomo 同步失败: "+err.Error())
		return
	}
	t.setState("up", "")
}

func (m *Manager) syncRestoredTunnel(t *Tunnel, oldHost string) error {
	if t.snapshot().Node.HostName != oldHost {
		t.setPendingRebind(oldHost)
		if err := m.rebind(oldHost, t); err != nil {
			return err
		}
		t.setPendingRebind("")
		return nil
	}
	return m.resync(t)
}

func (m *Manager) retryTunnelSync(t *Tunnel) error {
	if oldHost := t.pendingRebind(); oldHost != "" {
		return m.syncRestoredTunnel(t, oldHost)
	}
	return m.resync(t)
}

func (m *Manager) bringUpLocked(t *Tunnel, notify bool) {
	// VPN Gate 是志愿者节点，列表里有相当比例已下线或满员（AUTH_FAILED），
	// 连不上就顺着候选列表换下一个，不必让用户手动试。
	candidates := m.candidatesFor(t.snapshot().Node)
	var lastErr error

	for i, node := range candidates {
		if t.isClosed() {
			return
		}
		// 其他隧道可能在重试期间占用了这个节点，跳过以免多个端口撞同一出口 IP
		if i > 0 && m.nodeInUse(node.HostName, t.Slot) {
			continue
		}
		t.setNode(node)
		if i > 0 {
			t.setState("starting", fmt.Sprintf("已换到第 %d 个候选节点", i+1))
		}

		err := m.tryNode(t)
		if err == nil {
			if t.isClosed() {
				t.stopOpenVPN()
				t.teardownNetns()
				return
			}
			t.setState("syncing", "正在同步 Mihomo 配置")
			if notify {
				if syncErr := m.notifyPanel(); syncErr != nil {
					t.setState("sync_failed", "VPN 已连接，但 Mihomo 同步失败: "+syncErr.Error())
					return
				}
				t.setState("up", "")
			} else {
				t.setState("vpn_up", "等待同步 Mihomo 配置")
			}
			if serr := m.saveState(); serr != nil {
				log.Printf("保存状态失败: %v", serr)
			}
			return
		}
		lastErr = err
		t.stopOpenVPN()
		t.teardownNetns()
	}

	message := ""
	if lastErr != nil {
		message = fmt.Sprintf("尝试 %d 个节点均失败，最后一个: %v", len(candidates), lastErr)
	}
	t.setState("failed", message)
	if serr := m.saveState(); serr != nil {
		log.Printf("保存状态失败: %v", serr)
	}
}

// tryNode 尝试用当前节点把隧道拉起来。
func (m *Manager) tryNode(t *Tunnel) error {
	if err := t.setupNetns(); err != nil {
		return err
	}
	if err := t.startOpenVPN(m.workDir); err != nil {
		return err
	}
	ip, err := t.probeExitIP()
	if err != nil {
		return err
	}
	t.setExitIP(ip)
	if !t.hasListener() {
		if err := t.serve(); err != nil {
			return err
		}
	}
	return nil
}

// candidatesFor 以指定节点打头，后面跟上同地区的其他节点作为备选。
func (m *Manager) candidatesFor(first Node) []Node {
	const maxTries = 6
	m.mu.RLock()
	defer m.mu.RUnlock()

	used := map[string]bool{first.HostName: true}
	for _, t := range m.tunnels {
		used[t.snapshot().Node.HostName] = true
	}

	// 地区决定了备选范围，缺失时先从当前列表补一次，
	// 否则会退化成"任意地区都算同区"。
	region := first.CountryCode
	if region == "" {
		for _, n := range m.nodes {
			if n.HostName == first.HostName {
				region = n.CountryCode
				break
			}
		}
	}

	out := []Node{first}
	for _, n := range m.nodes {
		if len(out) >= maxTries {
			break
		}
		if used[n.HostName] {
			continue
		}
		// 地区实在拿不到时不做限制，总比连不上强
		if region != "" && n.CountryCode != region {
			continue
		}
		out = append(out, n)
	}
	return out
}

// Stop 停掉一条隧道并释放槽位。
func (m *Manager) Stop(slot int) error {
	invalidateInbounds()
	m.mu.Lock()
	t, ok := m.tunnels[slot]
	if ok {
		delete(m.tunnels, slot)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("槽位 %d 没有运行中的隧道", slot)
	}
	t.stop()
	if err := m.saveState(); err != nil {
		log.Printf("保存状态失败: %v", err)
	}
	if err := m.notifyPanel(); err != nil {
		log.Printf("同步节点链接后端失败: %v", err)
	}
	return nil
}

// Swap 把一条隧道换到同地区的另一个节点上，端口与已分发的客户端配置保持不变。
//
// 与健康检查的自动重连不同：那边优先重连原节点（目标是恢复），
// 这里用户是嫌当前出口 IP 不好用，必须真的换一个。
func (m *Manager) Swap(slot int) error {
	return m.SwapTo(slot, "")
}

// SwapTo switches an existing tunnel to a user-selected VPN Gate node. An
// empty hostname keeps the old speed-first automatic selection behaviour.
func (m *Manager) SwapTo(slot int, hostname string) error {
	m.mu.RLock()
	t, ok := m.tunnels[slot]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("槽位 %d 没有运行中的隧道", slot)
	}
	snap := t.snapshot()
	if snap.Status == "starting" || snap.Status == "syncing" || snap.Status == "vpn_up" {
		return fmt.Errorf("这个出口正在连接中，稍等一下")
	}

	var target Node
	if hostname == "" {
		// pickNodes 已排除所有在用节点，拿到的必然不是当前这个
		picks, err := m.pickNodes(snap.Node.CountryCode, 1)
		if err != nil {
			return err
		}
		target = picks[0]
	} else {
		m.mu.RLock()
		for _, candidate := range m.nodes {
			if candidate.HostName == hostname {
				target = candidate
				break
			}
		}
		for otherSlot, other := range m.tunnels {
			if otherSlot != slot && other.snapshot().Node.HostName == hostname {
				m.mu.RUnlock()
				return fmt.Errorf("节点 %s 已被另一个出口使用", hostname)
			}
		}
		m.mu.RUnlock()
		if target.HostName == "" {
			return fmt.Errorf("节点 %s 不存在，可能列表已刷新", hostname)
		}
		if target.HostName == snap.Node.HostName {
			return fmt.Errorf("当前出口已经在使用这个节点")
		}
	}
	oldHost := snap.Node.HostName
	if !t.beginReconnect() {
		return fmt.Errorf("这个出口正在执行其他操作")
	}
	t.setNode(target)
	m.reconnectStarted(t, oldHost)
	return nil
}

// StopAll 停掉所有隧道并清空状态文件。
func (m *Manager) StopAll() {
	for _, t := range m.Tunnels() {
		_ = m.Stop(t.Slot)
	}
}

// Shutdown 停掉运行态但保留状态文件，让下次启动能恢复同样的隧道。
func (m *Manager) Shutdown() {
	for _, t := range m.Tunnels() {
		t.stop()
	}
}

// prepareHost 打开转发开关。netns 出网依赖它。
func prepareHost() error {
	if err := exec.Command("sysctl", "-qw", "net.ipv4.ip_forward=1").Run(); err != nil {
		return fmt.Errorf("开启 ip_forward 失败: %w", err)
	}
	return nil
}

// nodeInUse 判断某节点是否已被别的隧道占用。
func (m *Manager) nodeInUse(host string, exceptSlot int) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for slot, t := range m.tunnels {
		if slot != exceptSlot && t.snapshot().Node.HostName == host {
			return true
		}
	}
	return false
}

// rebind 在隧道换节点后，把原先指向旧节点的 3x-ui 入站改绑到新节点。
// 面板不可用时静默跳过，健康检查本身不应因此失败。
func (m *Manager) rebind(oldHost string, t *Tunnel) error {
	x, err := openPanel()
	if err != nil {
		return err
	}
	return x.Rebind(oldHost, t, m.Tunnels())
}

// resync 在节点没换但重连过之后，把 3x-ui 的出站配置刷新一遍。
// 面板不可用时静默跳过，健康检查本身不应因此失败。
func (m *Manager) resync(t *Tunnel) error {
	x, err := openPanel()
	if err != nil {
		return err
	}
	return x.ResyncOutbound(t, m.Tunnels())
}

// notifyPanel 告诉后端隧道集合变了。
//
// 自建模式下出站是由隧道列表现算出来的，不通知的话新开的出口在 Xray 里
// 没有对应的 socks 出站，绑定会指向一个不存在的 tag。接管 3x-ui 时是空操作。
// 后端不可用不该让开关出口失败，所以只记日志。
func (m *Manager) notifyPanel() error {
	p, err := openPanel()
	if err != nil {
		return err
	}
	return p.OnTunnelsChanged(m.Tunnels())
}
