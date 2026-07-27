package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// persistedTunnel 是隧道在磁盘上的形态。
// 只存重建所需的信息，运行态（netns、进程、监听）重启后重新建立。
type persistedTunnel struct {
	Slot        int    `json:"slot"`
	Port        int    `json:"port"`
	HostName    string `json:"hostname"`
	CountryCode string `json:"country_code"`
	Country     string `json:"country"`
	Config      string `json:"config"`
}

type persistedState struct {
	Tunnels []persistedTunnel `json:"tunnels"`
}

type persistedSlotPorts struct {
	Ports map[int]int `json:"ports"`
}

func statePath(dir string) string     { return filepath.Join(dir, "state.json") }
func slotPortsPath(dir string) string { return filepath.Join(dir, "slot-ports.json") }

func (m *Manager) loadSlotPorts() {
	blob, err := os.ReadFile(slotPortsPath(m.workDir))
	if err != nil {
		return
	}
	var saved persistedSlotPorts
	if json.Unmarshal(blob, &saved) == nil && saved.Ports != nil {
		m.slotPorts = saved.Ports
	}
}

func (m *Manager) saveSlotPortsLocked() error {
	blob, err := json.MarshalIndent(persistedSlotPorts{Ports: m.slotPorts}, "", "  ")
	if err != nil {
		return err
	}
	tmp := slotPortsPath(m.workDir) + ".tmp"
	if err := os.WriteFile(tmp, blob, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, slotPortsPath(m.workDir))
}

// portForSlotLocked returns the permanent port owned by a slot. The caller
// must hold m.mu. A reserved port is never silently changed.
func (m *Manager) portForSlotLocked(slot int) (int, error) {
	if port := m.slotPorts[slot]; port > 0 {
		if !portAvailable(m.socksBind, port) {
			return 0, fmt.Errorf("槽位 %d 的固定端口 %d 被其他程序占用", slot, port)
		}
		return port, nil
	}
	taken := map[int]bool{}
	for _, port := range m.slotPorts {
		taken[port] = true
	}
	port, err := freeRandomPort(taken, m.socksBind)
	if err != nil {
		return 0, err
	}
	m.slotPorts[slot] = port
	if err := m.saveSlotPortsLocked(); err != nil {
		delete(m.slotPorts, slot)
		return 0, err
	}
	return port, nil
}

// saveState 把当前隧道写入磁盘，供重启后恢复。
func (m *Manager) saveState() error {
	var st persistedState
	for _, t := range m.Tunnels() {
		if t.Status != "up" {
			continue
		}
		st.Tunnels = append(st.Tunnels, persistedTunnel{
			Slot:        t.Slot,
			Port:        t.Port,
			HostName:    t.Node.HostName,
			CountryCode: t.Node.CountryCode,
			Country:     t.Node.Country,
			Config:      t.Node.Config,
		})
	}

	blob, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := statePath(m.workDir) + ".tmp"
	if err := os.WriteFile(tmp, blob, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, statePath(m.workDir))
}

// restoreState 读回上次的隧道并逐条拉起。
// 节点配置一并存了盘，所以即使 VPN Gate 列表里该节点已消失也能重建。
func (m *Manager) restoreState() (int, error) {
	m.loadSlotPorts()
	blob, err := os.ReadFile(statePath(m.workDir))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	var st persistedState
	if err := json.Unmarshal(blob, &st); err != nil {
		return 0, fmt.Errorf("解析状态文件失败: %w", err)
	}

	// 从当前节点列表补回地区、延迟等元数据；节点已下线时退回存盘的最小信息
	known := map[string]Node{}
	for _, n := range m.nodes {
		known[n.HostName] = n
	}

	for _, p := range st.Tunnels {
		// Upgrade path: seed the permanent slot port from the existing state.
		if m.slotPorts[p.Slot] == 0 {
			m.slotPorts[p.Slot] = p.Port
		}
		node, ok := known[p.HostName]
		if !ok {
			// 节点已从 VPN Gate 列表消失，用存盘的信息重建
			node = Node{
				HostName:    p.HostName,
				CountryCode: p.CountryCode,
				Country:     p.Country,
			}
		}
		node.Config = p.Config
		t := &Tunnel{
			Slot:        p.Slot,
			Port:        p.Port,
			BindAddress: m.socksBind,
			Node:        node,
			Status:      "starting",
		}
		m.mu.Lock()
		m.tunnels[p.Slot] = t
		m.mu.Unlock()
		go m.bringUp(t)
	}
	m.mu.Lock()
	_ = m.saveSlotPortsLocked()
	m.mu.Unlock()
	return len(st.Tunnels), nil
}
