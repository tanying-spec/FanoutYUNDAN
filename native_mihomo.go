package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const mihomoManagedPrefix = "fy-out-"

type mihomoBackend struct {
	bin        string
	configPath string
}

type MihomoTemplate struct {
	Name    string `json:"name"`
	Port    int    `json:"port"`
	Network string `json:"network"`
	Path    string `json:"path,omitempty"`
	Listen  string `json:"listen,omitempty"`
}

func (m *mihomoBackend) templates() ([]MihomoTemplate, error) {
	blob, err := os.ReadFile(m.configPath)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(blob, &cfg); err != nil {
		return nil, err
	}
	var out []MihomoTemplate
	listeners, _ := cfg["listeners"].([]any)
	for _, raw := range listeners {
		l, ok := raw.(map[string]any)
		if !ok || strings.ToLower(fmt.Sprint(l["type"])) != "vless" {
			continue
		}
		network, path := "tcp", ""
		if p := fmt.Sprint(l["ws-path"]); p != "<nil>" && p != "" {
			network, path = "ws", p
		}
		out = append(out, MihomoTemplate{Name: fmt.Sprint(l["name"]), Port: yamlInt(l["port"]), Network: network, Path: path, Listen: fmt.Sprint(l["listen"])})
	}
	return out, nil
}

func findMihomo() (*mihomoBackend, error) {
	bin, err := exec.LookPath("mihomo")
	if err != nil {
		for _, p := range []string{"/usr/local/bin/mihomo", "/usr/bin/mihomo"} {
			if st, statErr := os.Stat(p); statErr == nil && st.Mode()&0111 != 0 {
				bin = p
				break
			}
		}
	}
	if bin == "" {
		return nil, fmt.Errorf("找不到 Mihomo；请先安装并启动 Mihomo")
	}
	for _, p := range []string{
		os.Getenv("MIHOMO_CONFIG"),
		"/etc/mihomo/config.yaml", "/etc/mihomo/config.yml",
		"/root/.config/mihomo/config.yaml", "/root/.config/mihomo/config.yml",
	} {
		if p == "" {
			continue
		}
		if st, statErr := os.Stat(p); statErr == nil && !st.IsDir() {
			return &mihomoBackend{bin: bin, configPath: p}, nil
		}
	}
	return nil, fmt.Errorf("找不到 Mihomo 配置；可用 MIHOMO_CONFIG 指定 config.yaml")
}

func (m *mihomoBackend) apply(inbounds []*nativeInbound, tunnels []*Tunnel) error {
	original, err := os.ReadFile(m.configPath)
	if err != nil {
		return fmt.Errorf("读取 Mihomo 配置失败: %w", err)
	}
	updated, err := mergeMihomoConfig(original, inbounds, tunnels)
	if err != nil {
		return err
	}
	if bytes.Equal(original, updated) {
		return nil
	}

	dir := filepath.Dir(m.configPath)
	tmp, err := os.CreateTemp(dir, ".fanout-yundan-*.yaml")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(updated)
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("写入临时配置失败: %w", err)
	}
	if out, testErr := exec.Command(m.bin, "-t", "-f", tmpPath).CombinedOutput(); testErr != nil {
		return fmt.Errorf("Mihomo 配置校验失败: %s", trimOutput(out))
	}

	backup := m.configPath + ".fanout-yundan.bak"
	if err := os.WriteFile(backup, original, 0600); err != nil {
		return fmt.Errorf("备份 Mihomo 配置失败: %w", err)
	}
	if err := os.Rename(tmpPath, m.configPath); err != nil {
		return fmt.Errorf("替换 Mihomo 配置失败: %w", err)
	}
	if err := restartMihomo(); err == nil {
		return nil
	}

	_ = os.WriteFile(m.configPath, original, 0600)
	rollbackErr := restartMihomo()
	if rollbackErr != nil {
		return fmt.Errorf("Mihomo 重启失败，且自动回滚后仍无法启动: %v", rollbackErr)
	}
	return fmt.Errorf("Mihomo 重启失败，已恢复原配置")
}

func mergeMihomoConfig(blob []byte, inbounds []*nativeInbound, tunnels []*Tunnel) ([]byte, error) {
	var cfg map[string]any
	if err := yaml.Unmarshal(blob, &cfg); err != nil {
		return nil, fmt.Errorf("解析 Mihomo YAML 失败: %w", err)
	}

	live := map[string]*Tunnel{}
	for _, t := range tunnels {
		if t.Status == "up" {
			live[sanitizeTag(t.Node.HostName)] = t
		}
	}
	managedUsers := map[string]bool{}
	for _, ib := range inbounds {
		for i := range ib.Clients {
			managedUsers[ib.clientUsername(i, &ib.Clients[i])] = true
		}
	}

	listeners, _ := cfg["listeners"].([]any)
	matched := map[int]bool{}
	for _, raw := range listeners {
		listener, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := listener["name"].(string)
		port := yamlInt(listener["port"])
		users, _ := listener["users"].([]any)
		kept := users[:0]
		for _, u := range users {
			um, ok := u.(map[string]any)
			if ok && managedUsers[fmt.Sprint(um["username"])] {
				continue
			}
			kept = append(kept, u)
		}
		for _, ib := range inbounds {
			if !ib.Enable || ib.Protocol != "vless" {
				continue
			}
			if ib.Template != "" && ib.Template != name {
				continue
			}
			if ib.Template == "" && ib.ListenerPort != 0 && ib.ListenerPort != port {
				continue
			}
			matched[ib.ID] = true
			for i := range ib.Clients {
				c := &ib.Clients[i]
				if !c.Enable {
					continue
				}
				kept = append(kept, map[string]any{"username": ib.clientUsername(i, c), "uuid": c.ID})
			}
		}
		listener["users"] = kept
	}
	for _, ib := range inbounds {
		if ib.Enable && !matched[ib.ID] {
			return nil, fmt.Errorf("找不到入站 %q 对应的 Mihomo listener（模板 %q，端口 %d）", ib.Remark, ib.Template, ib.ListenerPort)
		}
	}

	proxies, _ := cfg["proxies"].([]any)
	cleanProxies := proxies[:0]
	for _, p := range proxies {
		pm, ok := p.(map[string]any)
		if ok && strings.HasPrefix(fmt.Sprint(pm["name"]), mihomoManagedPrefix) {
			continue
		}
		cleanProxies = append(cleanProxies, p)
	}
	for key, t := range live {
		cleanProxies = append(cleanProxies, map[string]any{"name": mihomoManagedPrefix + key, "type": "socks5", "server": "127.0.0.1", "port": t.Port, "udp": false})
	}
	cfg["proxies"] = cleanProxies

	rules, _ := cfg["rules"].([]any)
	cleanRules := make([]any, 0, len(rules)+len(inbounds))
	for _, r := range rules {
		text := fmt.Sprint(r)
		managedRule := false
		for username := range managedUsers {
			if strings.HasPrefix(text, "IN-USER,"+username+",") {
				managedRule = true
				break
			}
		}
		if managedRule {
			continue
		}
		cleanRules = append(cleanRules, r)
	}
	managed := []any{}
	for _, ib := range inbounds {
		if !ib.Enable || live[ib.BoundTo] == nil {
			continue
		}
		for i := range ib.Clients {
			c := &ib.Clients[i]
			if !c.Enable {
				continue
			}
			managed = append(managed, fmt.Sprintf("IN-USER,%s,%s%s", ib.clientUsername(i, c), mihomoManagedPrefix, ib.BoundTo))
		}
	}
	// 保留用户原有规则优先级，只在 MATCH 等兜底规则之前插入托管规则。
	insertAt := len(cleanRules)
	for i, r := range cleanRules {
		text := strings.TrimSpace(fmt.Sprint(r))
		if strings.HasPrefix(text, "MATCH,") || text == "MATCH" {
			insertAt = i
			break
		}
	}
	mergedRules := make([]any, 0, len(cleanRules)+len(managed))
	mergedRules = append(mergedRules, cleanRules[:insertAt]...)
	mergedRules = append(mergedRules, managed...)
	mergedRules = append(mergedRules, cleanRules[insertAt:]...)
	cfg["rules"] = mergedRules

	out, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("生成 Mihomo YAML 失败: %w", err)
	}
	return out, nil
}

func yamlInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case uint64:
		return int(n)
	}
	var out int
	_, _ = fmt.Sscanf(fmt.Sprint(v), "%d", &out)
	return out
}

func restartMihomo() error {
	commands := [][]string{{"rc-service", "mihomo", "restart"}, {"systemctl", "restart", "mihomo"}, {"service", "mihomo", "restart"}}
	var last error
	for _, c := range commands {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err == nil {
			return nil
		} else {
			last = fmt.Errorf("%s: %s", err, trimOutput(out))
		}
	}
	if last == nil {
		last = fmt.Errorf("系统没有可用的 Mihomo 服务管理器")
	}
	return last
}
