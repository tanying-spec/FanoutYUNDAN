package main

import (
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"
)

type ipProbe struct {
	host string
	url  string
}

var ipProbes = []ipProbe{
	{host: "api.ipify.org", url: "https://api.ipify.org"},
	{host: "checkip.amazonaws.com", url: "https://checkip.amazonaws.com"},
	{host: "ifconfig.me", url: "https://ifconfig.me/ip"},
}

const (
	healthInterval = 10 * time.Second
	healthFailures = 2 // 连续失败几次才判定掉线，避免网络抖动误杀
	healthTimeout  = 6 * time.Second
)

// WatchHealth 周期检查每条隧道是否还能出网，掉线的自动换节点重连。
// VPN Gate 是志愿者节点，运行中掉线很常见。
func (m *Manager) WatchHealth() {
	fails := map[int]int{}
	ticker := time.NewTicker(healthInterval)
	defer ticker.Stop()

	for range ticker.C {
		for _, t := range m.Tunnels() {
			snap := t.snapshot()
			if snap.Status == "sync_failed" {
				t.opMu.Lock()
				if t.isClosed() || t.snapshot().Status != "sync_failed" {
					t.opMu.Unlock()
					continue
				}
				if err := m.resync(t); err == nil {
					t.setState("up", "")
				} else {
					t.setState("sync_failed", "VPN 已连接，但 Mihomo 同步失败: "+err.Error())
				}
				t.opMu.Unlock()
				continue
			}
			if snap.Status != "up" {
				continue
			}
			healthy, conclusive := m.tunnelHealthy(t)
			if !conclusive {
				log.Printf("隧道 %d (%s) 健康检测服务暂时不可达，本轮不触发换线", snap.Slot, snap.Node.HostName)
				continue
			}
			if healthy {
				fails[t.Slot] = 0
				continue
			}

			fails[t.Slot]++
			if fails[t.Slot] < healthFailures {
				log.Printf("隧道 %d (%s) 探测失败 %d 次", snap.Slot, snap.Node.HostName, fails[snap.Slot])
				continue
			}

			log.Printf("隧道 %d (%s) 已掉线，正在换节点重连", snap.Slot, snap.Node.HostName)
			fails[snap.Slot] = 0
			m.reconnect(t, snap.Node.HostName)
		}
	}
}

// tunnelHealthy 判断隧道是否还真的走在 VPN 上。
//
// 只看"能不能出网"是不够的：netns 通过 veth 走母机 NAT，
// openvpn 死掉后照样能出网，只是出口变回了母机 IP。
// 所以要比对出口 IP 是否仍是建立隧道时拿到的那个。
func (m *Manager) tunnelHealthy(t *Tunnel) (healthy, conclusive bool) {
	snap := t.snapshot()
	got, err := probePublicIP(t.nsName(), healthTimeout)
	if err != nil {
		return false, false
	}
	return got == snap.ExitIP, true
}

func probePublicIP(ns string, timeout time.Duration) (string, error) {
	var failures []string
	for _, probe := range ipProbes {
		resolved, err := lookupIPv4(probe.host)
		if err != nil {
			failures = append(failures, probe.host+": DNS")
			continue
		}
		out, err := commandInNetns(ns, "curl", "-fsS", "--max-time",
			strconv.Itoa(int(timeout.Seconds())), "--resolve", probe.host+":443:"+resolved, probe.url).Output()
		if err != nil {
			failures = append(failures, probe.host+": HTTP")
			continue
		}
		got := strings.TrimSpace(string(out))
		if net.ParseIP(got) != nil {
			return got, nil
		}
		failures = append(failures, probe.host+": invalid response")
	}
	return "", fmt.Errorf("出口检测源均不可达: %s", strings.Join(failures, ", "))
}

// reconnect 就地把一条隧道换到别的节点上，保持槽位与端口不变，
// 这样已经分发出去的客户端配置仍然可用。
//
// oldHost 必须是本次重连前那条隧道真正绑着的节点名。调用方若已经
// 改过 t.Node（比如手动换节点），就要把改之前的名字传进来，
// 否则 rebind 找不到旧绑定，入站会掉成孤儿。
func (m *Manager) reconnect(t *Tunnel, oldHost string) {
	if !t.beginReconnect() {
		return
	}
	m.reconnectStarted(t, oldHost)
}

func (m *Manager) reconnectStarted(t *Tunnel, oldHost string) {
	go func() {
		t.opMu.Lock()
		defer t.opMu.Unlock()
		if t.isClosed() {
			return
		}
		t.setExitIP("")
		t.closeListener()
		t.stopOpenVPN()
		t.teardownNetns()
		// 通知延后到 rebind/resync 之后：那两步会把入站改绑到新节点，
		// 提前重建配置会因为入站还指着旧节点名而丢掉路由规则
		m.bringUpLocked(t, false)
		snap := t.snapshot()
		if snap.Status != "vpn_up" {
			return
		}
		// 出站 tag 跟着节点名走，换了节点就要把原来指向它的入站重新绑过去，
		// 否则面板里的路由会指向一个已经不存在的出站。
		if snap.Node.HostName != oldHost {
			if err := m.rebind(oldHost, t); err != nil {
				log.Printf("重连后同步 3x-ui 绑定失败: %v", err)
				t.setState("sync_failed", "VPN 已连接，但 Mihomo 同步失败: "+err.Error())
				return
			}
			t.setState("up", "")
			return
		}
		// 节点名没变也要重写一次出站：出口 IP 可能变了，
		// 而且上一轮换节点时留下的绑定需要重新指回来。
		if err := m.resync(t); err != nil {
			log.Printf("重连后重写 3x-ui 出站失败: %v", err)
			t.setState("sync_failed", "VPN 已连接，但 Mihomo 同步失败: "+err.Error())
			return
		}
		t.setState("up", "")
	}()
}
