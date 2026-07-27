package main

import (
	"sync"
)

// Panel 是 fanout 管理节点链接的后端。
//
// FanoutYUNDAN 只使用 Mihomo 后端。界面和编排层仍依赖这个接口，
// 以保留原版 Fanout 的操作语义和用户体验。
type Panel interface {
	// Kind 返回 "native"，以兼容原版前端对可创建入站后端的判断。
	Kind() string
	// Describe 给出一行人能读的后端说明。
	Describe() string

	Inbounds(live map[string]bool) ([]Inbound, error)
	InboundDetail(id int, publicHost string) (*InboundDetail, error)
	InboundLinks(ids []int, publicHost string) ([]string, error)

	Bind(inboundTag string, hostname string, tunnels []*Tunnel) error
	Rebind(oldHost string, target *Tunnel, tunnels []*Tunnel) error
	ResyncOutbound(t *Tunnel, tunnels []*Tunnel) error

	CloneToTunnels(templateID int, hosts []string, tunnels []*Tunnel) ([]int, error)
	DeleteInbounds(ids []int, tunnels []*Tunnel) error

	// UpdateInbound 改端口、备注与启停。只有非零/非 nil 的字段会被写入。
	UpdateInbound(id int, patch InboundPatch, tunnels []*Tunnel) error

	// AddClient 给入站加一个客户端，email 留空时自动命名。
	AddClient(id int, email string, tunnels []*Tunnel) error
	// DeleteClient 摘掉入站上的一个客户端。
	DeleteClient(id int, email string, tunnels []*Tunnel) error
	// ResetClient 换掉客户端的凭据（UUID / trojan 密码），已分发的旧链接随即失效。
	ResetClient(id int, email string, tunnels []*Tunnel) error

	// OnTunnelsChanged 在隧道集合变化后调用。
	//
	// Mihomo 的 SOCKS5 出站由隧道列表推导，新开的出口需要同步配置。
	OnTunnelsChanged(tunnels []*Tunnel) error

	// Mihomo 由系统服务管理，Close 不停止 Mihomo。
	Close()
}

// InboundPatch 描述对入站的一次局部修改。指针为 nil 表示该字段不动。
type InboundPatch struct {
	Port   *int
	Remark *string
	Enable *bool
}

// closePanel 在进程退出时释放后端资源。
func closePanel() {
	panelState.mu.Lock()
	p := panelState.current
	panelState.mu.Unlock()
	if p != nil {
		p.Close()
	}
}

// panelState 缓存已选定的后端。探测涉及执行 x-ui 命令，没必要每个请求都做一次。
var panelState struct {
	mu      sync.Mutex
	current Panel
	workDir string
}

// configurePanel 记录 Mihomo 状态目录。mode 参数仅为兼容旧启动命令。
func configurePanel(workDir, mode string) {
	panelState.mu.Lock()
	defer panelState.mu.Unlock()
	panelState.workDir = workDir
	panelState.current = nil
}

// openPanel 返回当前可用的后端。
//
// FanoutYUNDAN 不探测 3x-ui，也不拉起额外 Xray，始终复用本机 Mihomo。
func openPanel() (Panel, error) {
	panelState.mu.Lock()
	defer panelState.mu.Unlock()

	if panelState.current != nil {
		return panelState.current, nil
	}

	n, err := openNative(panelState.workDir)
	if err != nil {
		return nil, err
	}
	panelState.current = n
	return n, nil
}
