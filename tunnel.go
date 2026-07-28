package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Tunnel 是一条运行中的隧道：一个 netns + 一个 openvpn 进程 + 一个本地 SOCKS5 端口。
type Tunnel struct {
	Slot        int       `json:"slot"`
	Port        int       `json:"port"`
	Node        Node      `json:"node"`
	Status      string    `json:"status"` // starting | up | failed | stopped
	ExitIP      string    `json:"exit_ip"`
	Err         string    `json:"err,omitempty"`
	Since       time.Time `json:"since"`
	BindAddress string    `json:"-"`

	ns         string
	listener   net.Listener
	ovpn       *exec.Cmd
	mu         sync.RWMutex
	opMu       sync.Mutex
	closed     bool
	rebindFrom string
}

// TunnelSnapshot is an immutable copy used by API, persistence and backend
// synchronization. Runtime resources deliberately stay on Tunnel.
type TunnelSnapshot struct {
	Slot        int       `json:"slot"`
	Port        int       `json:"port"`
	Node        Node      `json:"node"`
	Status      string    `json:"status"`
	ExitIP      string    `json:"exit_ip"`
	Err         string    `json:"err,omitempty"`
	Since       time.Time `json:"since"`
	BindAddress string    `json:"-"`
}

func (t *Tunnel) snapshot() TunnelSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return TunnelSnapshot{t.Slot, t.Port, t.Node, t.Status, t.ExitIP, t.Err, t.Since, t.BindAddress}
}

func (t *Tunnel) setState(status, message string) {
	t.mu.Lock()
	t.Status, t.Err = status, message
	t.mu.Unlock()
}

func (t *Tunnel) setNode(node Node) {
	t.mu.Lock()
	t.Node = node
	t.mu.Unlock()
}

func (t *Tunnel) setExitIP(ip string) {
	t.mu.Lock()
	t.ExitIP = ip
	t.mu.Unlock()
}

func (t *Tunnel) beginReconnect() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.Status == "starting" {
		return false
	}
	t.Status, t.Err, t.ExitIP = "starting", "正在换节点重连", ""
	return true
}

func (t *Tunnel) isClosed() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.closed
}

func (t *Tunnel) setPendingRebind(host string) {
	t.mu.Lock()
	t.rebindFrom = host
	t.mu.Unlock()
}

func (t *Tunnel) pendingRebind() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.rebindFrom
}

func tunnelRoutable(status string) bool {
	return status == "up" || status == "syncing" || status == "vpn_up" || status == "sync_failed"
}

func (t *Tunnel) MarshalJSON() ([]byte, error) { return json.Marshal(t.snapshot()) }

func (t *Tunnel) nsName() string { return fmt.Sprintf("fo%d", t.Slot) }
func (t *Tunnel) subnet() string { return fmt.Sprintf("10.99.%d", t.Slot) }

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runQuiet 执行清理类命令，忽略"本来就不存在"这类错误。
func runQuiet(name string, args ...string) {
	_ = exec.Command(name, args...).Run()
}

// setupNetns 建立 netns 与 veth 链路，并配好 NAT 与转发放行。
func (t *Tunnel) setupNetns() error {
	ns, sub := t.nsName(), t.subnet()
	veth, peer := fmt.Sprintf("fov%d", t.Slot), fmt.Sprintf("fop%d", t.Slot)

	t.teardownNetns()

	if err := run("ip", "netns", "add", ns); err != nil {
		return err
	}
	if err := runInNetns(ns, "ip", "link", "set", "lo", "up"); err != nil {
		return err
	}
	if err := run("ip", "link", "add", veth, "type", "veth", "peer", "name", peer); err != nil {
		return err
	}
	if err := run("ip", "link", "set", peer, "netns", ns); err != nil {
		return err
	}
	if err := run("ip", "addr", "add", sub+".1/30", "dev", veth); err != nil {
		return err
	}
	if err := run("ip", "link", "set", veth, "up"); err != nil {
		return err
	}
	if err := runInNetns(ns, "ip", "addr", "add", sub+".2/30", "dev", peer); err != nil {
		return err
	}
	if err := runInNetns(ns, "ip", "link", "set", peer, "up"); err != nil {
		return err
	}
	if err := runInNetns(ns, "ip", "route", "add", "default", "via", sub+".1"); err != nil {
		return err
	}

	// Keep the host's working resolvers first and retain public fallbacks for
	// minimal containers whose resolv.conf is empty or points at a stub.
	nsDir := filepath.Join("/etc/netns", ns)
	if err := os.MkdirAll(nsDir, 0755); err != nil {
		return fmt.Errorf("创建 %s 失败: %w", nsDir, err)
	}
	if err := os.WriteFile(filepath.Join(nsDir, "resolv.conf"), []byte(effectiveResolvConf()), 0644); err != nil {
		return fmt.Errorf("写 resolv.conf 失败: %w", err)
	}

	cidr := sub + ".0/30"
	ensureRule("nat", "POSTROUTING", "-s", cidr, "-j", "MASQUERADE")
	ensureRuleInsert("filter", "FORWARD", "-s", cidr, "-j", "ACCEPT")
	ensureRuleInsert("filter", "FORWARD", "-d", cidr, "-j", "ACCEPT")
	return nil
}

func effectiveResolvConf() string {
	seen := map[string]bool{}
	servers := []string{}
	if f, err := os.Open("/etc/resolv.conf"); err == nil {
		s := bufio.NewScanner(f)
		for s.Scan() {
			fields := strings.Fields(s.Text())
			if len(fields) == 2 && fields[0] == "nameserver" && net.ParseIP(fields[1]) != nil && !seen[fields[1]] {
				seen[fields[1]] = true
				servers = append(servers, fields[1])
			}
		}
		_ = f.Close()
	}
	for _, server := range []string{"1.1.1.1", "8.8.8.8"} {
		if !seen[server] {
			servers = append(servers, server)
		}
	}
	var b strings.Builder
	for _, server := range servers {
		fmt.Fprintf(&b, "nameserver %s\n", server)
	}
	b.WriteString("options timeout:2 attempts:2\n")
	return b.String()
}

// ensureRule 幂等追加一条 iptables 规则。
func ensureRule(table, chain string, spec ...string) {
	check := append([]string{"-w", "5", "-t", table, "-C", chain}, spec...)
	if exec.Command("iptables", check...).Run() == nil {
		return
	}
	add := append([]string{"-w", "5", "-t", table, "-A", chain}, spec...)
	runQuiet("iptables", add...)
}

// ensureRuleInsert 幂等插入规则到链首。
// FORWARD 链末尾常有兜底 REJECT，必须插到最前面才生效。
func ensureRuleInsert(table, chain string, spec ...string) {
	check := append([]string{"-w", "5", "-t", table, "-C", chain}, spec...)
	if exec.Command("iptables", check...).Run() == nil {
		return
	}
	ins := append([]string{"-w", "5", "-t", table, "-I", chain, "1"}, spec...)
	runQuiet("iptables", ins...)
}

func (t *Tunnel) teardownNetns() {
	ns, sub := t.nsName(), t.subnet()
	cidr := sub + ".0/30"
	runQuiet("ip", "netns", "del", ns)
	runQuiet("ip", "link", "del", fmt.Sprintf("fov%d", t.Slot))
	runQuiet("iptables", "-w", "5", "-t", "nat", "-D", "POSTROUTING", "-s", cidr, "-j", "MASQUERADE")
	runQuiet("iptables", "-w", "5", "-D", "FORWARD", "-s", cidr, "-j", "ACCEPT")
	runQuiet("iptables", "-w", "5", "-D", "FORWARD", "-d", cidr, "-j", "ACCEPT")
}

// startOpenVPN 在 netns 内拉起 openvpn，并等待 tun0 拿到地址。
func (t *Tunnel) startOpenVPN(dir string) error {
	snap := t.snapshot()
	ns := t.nsName()
	cfgPath := filepath.Join(dir, ns+".ovpn")
	if err := os.WriteFile(cfgPath, []byte(snap.Node.Config), 0600); err != nil {
		return fmt.Errorf("写配置失败: %w", err)
	}
	authPath := filepath.Join(dir, "auth.txt")
	if err := os.WriteFile(authPath, []byte("vpn\nvpn\n"), 0600); err != nil {
		return fmt.Errorf("写凭据失败: %w", err)
	}

	logPath := filepath.Join(dir, ns+".log")
	cmd := commandInNetns(ns, "openvpn",
		"--config", cfgPath,
		"--auth-user-pass", authPath,
		"--auth-nocache",
		"--remote-cert-tls", "server",
		"--dev", "tun0",
		"--connect-retry-max", "2",
		"--connect-timeout", "20",
		"--data-ciphers", "AES-128-CBC:AES-256-GCM:AES-128-GCM:CHACHA20-POLY1305",
		"--verb", "3",
		"--log", logPath,
	)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 openvpn 失败: %w", err)
	}
	t.mu.Lock()
	t.ovpn = cmd
	t.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }() // 回收子进程，避免僵尸

	// openvpn 建好 tun0 前 SOCKS5 无法正常出网，这里等它就绪
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			return fmt.Errorf("openvpn 提前退出，详见 %s", logPath)
		default:
		}
		if out, err := commandInNetns(ns, "ip", "-4", "addr", "show", "tun0").Output(); err == nil {
			if strings.Contains(string(out), "inet ") {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	t.stopOpenVPN()
	return fmt.Errorf("等待 tun0 就绪超时，详见 %s", logPath)
}

func (t *Tunnel) stopOpenVPN() {
	t.mu.Lock()
	cmd := t.ovpn
	t.ovpn = nil
	t.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// serve 在母机上监听 SOCKS5 端口，出站连接则在 netns 内建立。
// 监听必须留在母机侧：netns 内的 loopback 与母机彼此独立，
// 监听在 netns 里的话外部根本连不上。
func (t *Tunnel) serve() error {
	snap := t.snapshot()
	bindAddress := snap.BindAddress
	if bindAddress == "" {
		bindAddress = "127.0.0.1"
	}
	// 端口要尽量保持不变，否则用户已经分发出去的客户端配置会失效。
	// 进程刚重启时旧监听可能还在 TIME_WAIT，这里给几秒重试窗口。
	var ln net.Listener
	var err error
	for i := 0; i < 6; i++ {
		ln, err = net.Listen("tcp", net.JoinHostPort(bindAddress, fmt.Sprintf("%d", snap.Port)))
		if err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		// 确实被别的进程长期占用了，才换端口
		port, perr := freeRandomPort(map[int]bool{snap.Port: true})
		if perr != nil {
			return fmt.Errorf("监听 %d 失败且无备用端口: %w", t.Port, err)
		}
		ln, err = net.Listen("tcp", net.JoinHostPort(bindAddress, fmt.Sprintf("%d", port)))
		if err != nil {
			return fmt.Errorf("监听 %d 失败: %w", port, err)
		}
		t.mu.Lock()
		t.Port = port
		t.mu.Unlock()
	}
	t.mu.Lock()
	t.listener = ln
	t.mu.Unlock()
	dial := dialerInNetns(t.nsName())

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveSocks(conn, dial)
		}
	}()
	return nil
}

// probeExitIP 通过隧道查询出口 IP，用于确认这条隧道确实换了 IP。
func (t *Tunnel) probeExitIP() (string, error) {
	ip, err := probePublicIP(t.nsName(), 15*time.Second)
	if err != nil {
		return "", fmt.Errorf("查询出口 IP 失败: %w", err)
	}
	return ip, nil
}

// stop 停止这条隧道并清理它占用的所有资源。
func (t *Tunnel) stop() {
	t.mu.Lock()
	t.closed = true
	t.Status, t.Err = "stopped", ""
	t.mu.Unlock()
	t.opMu.Lock()
	defer t.opMu.Unlock()
	t.closeListener()
	t.stopOpenVPN()
	t.teardownNetns()
}

func (t *Tunnel) hasListener() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.listener != nil
}

func (t *Tunnel) closeListener() {
	t.mu.Lock()
	ln := t.listener
	t.listener = nil
	t.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
}
