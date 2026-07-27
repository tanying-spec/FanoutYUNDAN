package main

import (
	"crypto/rand"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const mihomoStateVersion = 1

type mihomoState struct {
	Version  int             `json:"version"`
	Bindings []mihomoInbound `json:"bindings"`
}

type mihomoInbound struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	UUID           string `json:"uuid"`
	Template       string `json:"template"`
	Protocol       string `json:"protocol"`
	Mode           string `json:"mode"`
	SocksPort      int    `json:"port"`
	ListenerPort   int    `json:"listener_port"`
	PublicAddress  string `json:"public_address"`
	PublicPort     int    `json:"public_port"`
	Host           string `json:"host,omitempty"`
	SNI            string `json:"sni,omitempty"`
	Path           string `json:"path,omitempty"`
	PublicKey      string `json:"public_key,omitempty"`
	ShortID        string `json:"short_id,omitempty"`
	Link           string `json:"link"`
	CreatedUnix    int64  `json:"created_unix"`
}

type mihomoTemplate struct {
	Name          string `json:"name"`
	Protocol      string `json:"protocol"`
	Mode          string `json:"mode"`
	ListenerPort  int    `json:"listener_port"`
	PublicAddress string `json:"public_address"`
	PublicPort    int    `json:"public_port"`
	Host          string `json:"host,omitempty"`
	SNI           string `json:"sni,omitempty"`
	Path          string `json:"path,omitempty"`
	PublicKey     string `json:"public_key,omitempty"`
	ShortID       string `json:"short_id,omitempty"`
	Ready         bool   `json:"ready"`
	Reason        string `json:"reason,omitempty"`
}

type nodeMetadata struct {
	Protocol      string
	Name          string
	Port          int
	Path          string
	Host          string
	Mode          string
	PublicAddress string
	PublicPort    int
	SNI           string
	PublicKey     string
	ShortID       string
}

func (m *Manager) mihomoStatePath() string {
	return filepath.Join(m.workDir, "mihomo-inbounds.json")
}

func findMihomoConfig() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("FANOUT_YUNDAN_MIHOMO_CONFIG")); configured != "" {
		if filepath.IsAbs(configured) {
			if _, err := os.Stat(configured); err == nil {
				return configured, nil
			}
		}
		return "", fmt.Errorf("指定的 Mihomo 配置不可读: %s", configured)
	}
	for _, path := range []string{
		"/etc/mihomo/config.yaml",
		"/etc/mihomo/config.yml",
		"/usr/local/etc/mihomo/config.yaml",
		"/root/.config/mihomo/config.yaml",
	} {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("未找到 Mihomo config.yaml")
}

func loadMihomoConfig(path string) (map[string]any, []byte, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("读取 Mihomo 配置失败: %w", err)
	}
	var config map[string]any
	if err := yaml.Unmarshal(blob, &config); err != nil {
		return nil, nil, fmt.Errorf("解析 Mihomo 配置失败: %w", err)
	}
	if config == nil {
		config = map[string]any{}
	}
	return config, blob, nil
}

func loadMihomoState(path string) (mihomoState, error) {
	state := mihomoState{Version: mihomoStateVersion, Bindings: []mihomoInbound{}}
	blob, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("读取 FanoutYUNDAN Mihomo 状态失败: %w", err)
	}
	if err := json.Unmarshal(blob, &state); err != nil {
		return state, fmt.Errorf("解析 FanoutYUNDAN Mihomo 状态失败: %w", err)
	}
	if state.Version != mihomoStateVersion {
		return state, fmt.Errorf("不支持的 Mihomo 状态版本 %d", state.Version)
	}
	return state, nil
}

func loadOrMigrateMihomoState(path, configPath string, config map[string]any) (mihomoState, error) {
	state, err := loadMihomoState(path)
	if err != nil || len(state.Bindings) > 0 {
		return state, err
	}
	legacyPath := filepath.Join(filepath.Dir(configPath), "fanout-bindings.db")
	blob, err := os.ReadFile(legacyPath)
	if os.IsNotExist(err) || len(strings.TrimSpace(string(blob))) == 0 {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("读取旧版 Mihomo fanout 绑定失败: %w", err)
	}
	templates := mihomoTemplates(configPath, config)
	byName := map[string]mihomoTemplate{}
	for _, template := range templates {
		byName[template.Name] = template
	}
	for _, line := range strings.Split(string(blob), "\n") {
		fields := strings.Split(line, "|")
		if len(fields) < 4 {
			continue
		}
		template, ok := byName[fields[1]]
		if !ok {
			continue
		}
		socksPort, _ := strconv.Atoi(fields[3])
		if socksPort < 1 {
			continue
		}
		created, _ := strconv.ParseInt(valueAt(fields, 4), 10, 64)
		sum := sha256.Sum256([]byte(fields[0] + "|" + fields[2]))
		binding := mihomoInbound{ID: hex.EncodeToString(sum[:6]), Name: fields[0], UUID: fields[2], Template: template.Name, Protocol: template.Protocol, Mode: template.Mode, SocksPort: socksPort, ListenerPort: template.ListenerPort, PublicAddress: template.PublicAddress, PublicPort: template.PublicPort, Host: template.Host, SNI: template.SNI, Path: template.Path, PublicKey: template.PublicKey, ShortID: template.ShortID, CreatedUnix: created}
		binding.Link = bindingLink(binding)
		state.Bindings = append(state.Bindings, binding)
	}
	if len(state.Bindings) > 0 {
		if err := saveMihomoState(path, state); err != nil {
			return state, fmt.Errorf("迁移旧版 Mihomo fanout 状态失败: %w", err)
		}
	}
	return state, nil
}

func valueAt(values []string, index int) string {
	if index >= 0 && index < len(values) {
		return values[index]
	}
	return ""
}

func saveMihomoState(path string, state mihomoState) error {
	state.Version = mihomoStateVersion
	blob, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(blob, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadNodeMetadata(configPath string) map[string]nodeMetadata {
	result := map[string]nodeMetadata{}
	blob, err := os.ReadFile(filepath.Join(filepath.Dir(configPath), "nodes.db"))
	if err != nil {
		return result
	}
	for _, line := range strings.Split(string(blob), "\n") {
		fields := strings.Split(line, "|")
		if len(fields) < 9 {
			continue
		}
		port, _ := strconv.Atoi(fields[2])
		meta := nodeMetadata{Protocol: fields[0], Name: fields[1], Port: port}
		switch fields[0] {
		case "vless-ws":
			meta.Path, meta.Host, meta.Mode = fields[4], fields[5], fields[6]
			meta.PublicAddress = fields[7]
			meta.PublicPort, _ = strconv.Atoi(fields[8])
		case "vless-reality":
			meta.SNI, meta.PublicKey, meta.ShortID = fields[4], fields[7], fields[8]
			meta.PublicPort = port
		}
		result[meta.Name] = meta
	}
	return result
}

func mihomoTemplates(configPath string, config map[string]any) []mihomoTemplate {
	metadata := loadNodeMetadata(configPath)
	publicAddress := publicIPv4()
	var result []mihomoTemplate
	for _, raw := range anySlice(config["listeners"]) {
		listener := anyMap(raw)
		if stringValue(listener["type"]) != "vless" {
			continue
		}
		name := stringValue(listener["name"])
		port := intValue(listener["port"])
		if name == "" || port < 1 {
			continue
		}
		tpl := mihomoTemplate{Name: name, Protocol: "vless-ws", Mode: "direct", ListenerPort: port, PublicAddress: publicAddress, PublicPort: port, Ready: true}
		if path := stringValue(listener["ws-path"]); path != "" {
			tpl.Path = path
		} else if reality := anyMap(listener["reality-config"]); len(reality) > 0 {
			tpl.Protocol, tpl.Mode = "vless-reality", "reality"
			if names := anySlice(reality["server-names"]); len(names) > 0 {
				tpl.SNI = stringValue(names[0])
			}
		} else {
			continue
		}
		if meta, ok := metadata[name]; ok {
			if meta.Protocol == "vless-ws" {
				tpl.Path, tpl.Host = meta.Path, meta.Host
				if meta.Mode != "" {
					tpl.Mode = meta.Mode
				}
				if meta.PublicAddress != "" {
					tpl.PublicAddress = meta.PublicAddress
				}
				if meta.PublicPort > 0 {
					tpl.PublicPort = meta.PublicPort
				} else if tpl.Mode == "cdn" || tpl.Mode == "argo" {
					tpl.PublicPort = 443
				}
				if tpl.Mode == "cdn" || tpl.Mode == "argo" {
					tpl.SNI = tpl.Host
				}
			} else if meta.Protocol == "vless-reality" {
				tpl.SNI, tpl.PublicKey, tpl.ShortID = meta.SNI, meta.PublicKey, meta.ShortID
			}
		}
		if tpl.PublicAddress == "" {
			tpl.Ready, tpl.Reason = false, "无法确定公网入口地址"
		}
		if tpl.Protocol == "vless-reality" && (tpl.PublicKey == "" || tpl.ShortID == "") {
			tpl.Ready, tpl.Reason = false, "Reality 分享链接缺少公钥或 Short ID"
		}
		result = append(result, tpl)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func publicIPv4() string {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	ip := strings.TrimSpace(string(buf[:n]))
	if parsed := net.ParseIP(ip); parsed != nil && parsed.To4() != nil {
		return ip
	}
	return ""
}

func anySlice(value any) []any {
	if value == nil {
		return nil
	}
	if out, ok := value.([]any); ok {
		return out
	}
	return nil
}

func anyMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if out, ok := value.(map[string]any); ok {
		return out
	}
	return nil
}

func stringValue(value any) string {
	if out, ok := value.(string); ok {
		return out
	}
	return ""
}

func intValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case uint64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(b)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func bindingLink(binding mihomoInbound) string {
	name := url.PathEscape(binding.Name)
	address := binding.PublicAddress
	if strings.Contains(address, ":") && !strings.HasPrefix(address, "[") {
		address = "[" + address + "]"
	}
	switch binding.Protocol {
	case "vless-reality":
		q := url.Values{"encryption": {"none"}, "security": {"reality"}, "sni": {binding.SNI}, "fp": {"chrome"}, "pbk": {binding.PublicKey}, "sid": {binding.ShortID}, "type": {"tcp"}}
		return fmt.Sprintf("vless://%s@%s:%d?%s#%s", binding.UUID, address, binding.PublicPort, q.Encode(), name)
	default:
		q := url.Values{"encryption": {"none"}, "type": {"ws"}, "path": {binding.Path}}
		if binding.Mode == "cdn" || binding.Mode == "argo" {
			q.Set("security", "tls")
			q.Set("sni", binding.SNI)
			q.Set("fp", "chrome")
			q.Set("host", binding.Host)
		} else {
			q.Set("security", "none")
			if binding.Host != "" {
				q.Set("host", binding.Host)
			}
		}
		return fmt.Sprintf("vless://%s@%s:%d?%s#%s", binding.UUID, address, binding.PublicPort, q.Encode(), name)
	}
}

func outboundName(id string) string { return "fy-out-" + id }

func addBindingToConfig(config map[string]any, binding mihomoInbound) error {
	listeners := anySlice(config["listeners"])
	found := false
	for _, raw := range listeners {
		listener := anyMap(raw)
		if stringValue(listener["name"]) != binding.Template {
			continue
		}
		users := anySlice(listener["users"])
		for _, userRaw := range users {
			user := anyMap(userRaw)
			if stringValue(user["username"]) == binding.Name {
				user["uuid"] = binding.UUID
				found = true
				break
			}
		}
		if !found {
			listener["users"] = append(users, map[string]any{"username": binding.Name, "uuid": binding.UUID})
			found = true
		}
		break
	}
	if !found {
		return fmt.Errorf("Mihomo 入站模板 %s 不存在", binding.Template)
	}
	proxies := anySlice(config["proxies"])
	proxyName := outboundName(binding.ID)
	legacyProxyName := "fanout-" + binding.Name
	proxyFound := false
	filteredProxies := make([]any, 0, len(proxies))
	for _, raw := range proxies {
		name := stringValue(anyMap(raw)["name"])
		if name == legacyProxyName {
			continue
		}
		filteredProxies = append(filteredProxies, raw)
		if name == proxyName {
			proxy := anyMap(raw)
			proxy["server"], proxy["port"] = "127.0.0.1", binding.SocksPort
			proxyFound = true
		}
	}
	proxies = filteredProxies
	if !proxyFound {
		config["proxies"] = append(proxies, map[string]any{"name": proxyName, "type": "socks5", "server": "127.0.0.1", "port": binding.SocksPort, "udp": false})
	} else {
		config["proxies"] = proxies
	}
	rule := "IN-USER," + binding.Name + "," + proxyName
	rules := anySlice(config["rules"])
	filteredRules := make([]any, 0, len(rules))
	for _, raw := range rules {
		if strings.HasSuffix(stringValue(raw), ","+legacyProxyName) {
			continue
		}
		filteredRules = append(filteredRules, raw)
	}
	rules = filteredRules
	for _, raw := range rules {
		if stringValue(raw) == rule {
			return nil
		}
	}
	config["rules"] = append([]any{rule}, rules...)
	return nil
}

func removeBindingFromConfig(config map[string]any, binding mihomoInbound) {
	for _, raw := range anySlice(config["listeners"]) {
		listener := anyMap(raw)
		if stringValue(listener["name"]) != binding.Template {
			continue
		}
		var users []any
		for _, userRaw := range anySlice(listener["users"]) {
			if stringValue(anyMap(userRaw)["username"]) != binding.Name {
				users = append(users, userRaw)
			}
		}
		listener["users"] = users
	}
	proxyName := outboundName(binding.ID)
	legacyProxyName := "fanout-" + binding.Name
	var proxies []any
	for _, raw := range anySlice(config["proxies"]) {
		name := stringValue(anyMap(raw)["name"])
		if name != proxyName && name != legacyProxyName {
			proxies = append(proxies, raw)
		}
	}
	config["proxies"] = proxies
	var rules []any
	for _, raw := range anySlice(config["rules"]) {
		rule := stringValue(raw)
		if !strings.HasSuffix(rule, ","+proxyName) && !strings.HasSuffix(rule, ","+legacyProxyName) {
			rules = append(rules, raw)
		}
	}
	config["rules"] = rules
}

func applyMihomoConfig(path string, config map[string]any, previous []byte) error {
	blob, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("生成 Mihomo 配置失败: %w", err)
	}
	tmp := path + ".fanout-yundan.tmp"
	backup := path + ".fanout-yundan.previous"
	if err := os.WriteFile(tmp, blob, 0600); err != nil {
		return err
	}
	defer os.Remove(tmp)
	bin, err := exec.LookPath("mihomo")
	if err != nil {
		bin = "/usr/local/bin/mihomo"
	}
	if out, err := exec.Command(bin, "-t", "-d", filepath.Dir(path), "-f", tmp).CombinedOutput(); err != nil {
		return fmt.Errorf("Mihomo 配置检查失败: %s", strings.TrimSpace(string(out)))
	}
	if err := os.WriteFile(backup, previous, 0600); err != nil {
		return fmt.Errorf("备份 Mihomo 配置失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if err := restartMihomo(); err != nil {
		_ = os.WriteFile(path, previous, 0600)
		_ = restartMihomo()
		return fmt.Errorf("Mihomo 重启失败，已恢复原配置: %w", err)
	}
	return nil
}

func restartMihomo() error {
	var cmd *exec.Cmd
	if _, err := exec.LookPath("rc-service"); err == nil {
		cmd = exec.Command("rc-service", "mihomo", "restart")
	} else if _, err := exec.LookPath("systemctl"); err == nil {
		cmd = exec.Command("systemctl", "restart", "mihomo")
	} else {
		return fmt.Errorf("未找到 Mihomo 服务管理器")
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func apiMihomoStatus(m *Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path, err := findMihomoConfig()
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"available": false, "reason": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"available": true, "config": path, "managed_by": "FanoutYUNDAN"})
	}
}

func apiMihomoTemplates(m *Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path, err := findMihomoConfig()
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		config, _, err := loadMihomoConfig(path)
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, mihomoTemplates(path, config))
	}
}

func apiMihomoInbounds(m *Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.mihomoMu.Lock()
		defer m.mihomoMu.Unlock()
		path, err := findMihomoConfig()
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		config, _, err := loadMihomoConfig(path)
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		state, err := loadOrMigrateMihomoState(m.mihomoStatePath(), path, config)
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		for i := range state.Bindings {
			state.Bindings[i].Link = bindingLink(state.Bindings[i])
		}
		writeJSON(w, 200, state.Bindings)
	}
}

func apiMihomoAdd(m *Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.mihomoMu.Lock()
		defer m.mihomoMu.Unlock()
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		socksPort, _ := strconv.Atoi(r.URL.Query().Get("port"))
		templateName := strings.TrimSpace(r.URL.Query().Get("template"))
		if name == "" || socksPort < 1 || socksPort > 65535 {
			writeJSON(w, 400, map[string]string{"error": "节点名称或 SOCKS 端口无效"})
			return
		}
		if !validManagedName(name) {
			writeJSON(w, 400, map[string]string{"error": "节点名称只能包含字母、数字、点、下划线和短横线"})
			return
		}
		path, err := findMihomoConfig()
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		config, previous, err := loadMihomoConfig(path)
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		templates := mihomoTemplates(path, config)
		var selected *mihomoTemplate
		for i := range templates {
			if templateName == "" || templates[i].Name == templateName {
				selected = &templates[i]
				if templateName != "" || templates[i].Ready {
					break
				}
			}
		}
		if selected == nil {
			writeJSON(w, 409, map[string]string{"error": "没有可用的 Mihomo VLESS 入站模板"})
			return
		}
		if value := strings.TrimSpace(r.URL.Query().Get("mode")); value != "" {
			if value != "direct" && value != "cdn" && value != "argo" && value != "reality" {
				writeJSON(w, 400, map[string]string{"error": "入口模式无效"})
				return
			}
			selected.Mode = value
		}
		if value := strings.TrimSpace(r.URL.Query().Get("public_address")); value != "" {
			if strings.ContainsAny(value, "/?#") {
				writeJSON(w, 400, map[string]string{"error": "公网入口只能填写 IP 或域名"})
				return
			}
			selected.PublicAddress = value
		}
		if value := strings.TrimSpace(r.URL.Query().Get("public_port")); value != "" {
			port, err := strconv.Atoi(value)
			if err != nil || port < 1 || port > 65535 {
				writeJSON(w, 400, map[string]string{"error": "公网入口端口无效"})
				return
			}
			selected.PublicPort = port
		}
		if value := strings.TrimSpace(r.URL.Query().Get("host")); value != "" {
			selected.Host = value
		}
		if value := strings.TrimSpace(r.URL.Query().Get("sni")); value != "" {
			selected.SNI = value
		}
		if value := strings.TrimSpace(r.URL.Query().Get("path")); value != "" {
			if !strings.HasPrefix(value, "/") {
				value = "/" + value
			}
			selected.Path = value
		}
		if value := strings.TrimSpace(r.URL.Query().Get("public_key")); value != "" {
			selected.PublicKey = value
		}
		if value := strings.TrimSpace(r.URL.Query().Get("short_id")); value != "" {
			selected.ShortID = value
		}
		if selected.PublicAddress == "" || selected.PublicPort < 1 {
			writeJSON(w, 400, map[string]string{"error": "缺少公网入口地址或端口"})
			return
		}
		if (selected.Mode == "cdn" || selected.Mode == "argo") && (selected.Host == "" || selected.SNI == "") {
			writeJSON(w, 400, map[string]string{"error": "CDN/Argo 模式必须填写 Host 和 SNI"})
			return
		}
		if selected.Mode == "reality" && (selected.SNI == "" || selected.PublicKey == "" || selected.ShortID == "") {
			writeJSON(w, 400, map[string]string{"error": "Reality 模式必须填写 SNI、公钥和 Short ID"})
			return
		}
		state, err := loadOrMigrateMihomoState(m.mihomoStatePath(), path, config)
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		for _, binding := range state.Bindings {
			if binding.Name == name {
				writeJSON(w, 409, map[string]string{"error": "Mihomo 入站名称已存在"})
				return
			}
		}
		uuid, err := newUUID()
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		id := strings.ReplaceAll(uuid, "-", "")[:12]
		binding := mihomoInbound{ID: id, Name: name, UUID: uuid, Template: selected.Name, Protocol: selected.Protocol, Mode: selected.Mode, SocksPort: socksPort, ListenerPort: selected.ListenerPort, PublicAddress: selected.PublicAddress, PublicPort: selected.PublicPort, Host: selected.Host, SNI: selected.SNI, Path: selected.Path, PublicKey: selected.PublicKey, ShortID: selected.ShortID, CreatedUnix: time.Now().Unix()}
		binding.Link = bindingLink(binding)
		if err := addBindingToConfig(config, binding); err != nil {
			writeJSON(w, 409, map[string]string{"error": err.Error()})
			return
		}
		if err := applyMihomoConfig(path, config, previous); err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		state.Bindings = append(state.Bindings, binding)
		if err := saveMihomoState(m.mihomoStatePath(), state); err != nil {
			writeJSON(w, 500, map[string]string{"error": "Mihomo 已更新，但保存 FanoutYUNDAN 状态失败: " + err.Error()})
			return
		}
		writeJSON(w, 200, binding)
	}
}

func apiMihomoBind(m *Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.mihomoMu.Lock()
		defer m.mihomoMu.Unlock()
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		port, err := strconv.Atoi(r.URL.Query().Get("port"))
		if name == "" || err != nil || port < 1 || port > 65535 {
			writeJSON(w, 400, map[string]string{"error": "入站名称或 SOCKS 端口无效"})
			return
		}
		allowed := false
		for _, tunnel := range m.Tunnels() {
			if tunnel.Status == "up" && tunnel.Port == port {
				allowed = true
				break
			}
		}
		if !allowed {
			writeJSON(w, 409, map[string]string{"error": "目标 SOCKS 端口不属于已连通的 Fanout 出口"})
			return
		}
		path, err := findMihomoConfig()
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		config, previous, err := loadMihomoConfig(path)
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		state, err := loadOrMigrateMihomoState(m.mihomoStatePath(), path, config)
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		index := -1
		for i := range state.Bindings {
			if state.Bindings[i].Name == name {
				index = i
				break
			}
		}
		if index < 0 {
			writeJSON(w, 404, map[string]string{"error": "未找到由 FanoutYUNDAN 管理的 Mihomo 入站"})
			return
		}
		state.Bindings[index].SocksPort = port
		if err := addBindingToConfig(config, state.Bindings[index]); err != nil {
			writeJSON(w, 409, map[string]string{"error": err.Error()})
			return
		}
		if err := applyMihomoConfig(path, config, previous); err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		if err := saveMihomoState(m.mihomoStatePath(), state); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]string{"ok": "已切换 Mihomo 入站出口"})
	}
}

func (m *Manager) reconcileMihomo() error {
	m.mihomoMu.Lock()
	defer m.mihomoMu.Unlock()
	path, err := findMihomoConfig()
	if err != nil {
		return err
	}
	config, previous, err := loadMihomoConfig(path)
	if err != nil {
		return err
	}
	state, err := loadOrMigrateMihomoState(m.mihomoStatePath(), path, config)
	if err != nil || len(state.Bindings) == 0 {
		return err
	}
	before, _ := yaml.Marshal(config)
	for _, binding := range state.Bindings {
		if err := addBindingToConfig(config, binding); err != nil {
			return err
		}
	}
	after, _ := yaml.Marshal(config)
	if bytes.Equal(before, after) {
		return nil
	}
	return applyMihomoConfig(path, config, previous)
}

func (m *Manager) WatchMihomo() {
	for {
		// Mihomo is optional. Reconcile immediately at startup, then keep
		// checking without producing a warning every minute on SOCKS-only hosts.
		if _, err := findMihomoConfig(); err == nil {
			if err := m.reconcileMihomo(); err != nil {
				fmt.Printf("FanoutYUNDAN Mihomo 配置同步失败: %v\n", err)
			}
		}
		time.Sleep(time.Minute)
	}
}

func (m *Manager) cleanupMihomo(keepState bool) error {
	m.mihomoMu.Lock()
	defer m.mihomoMu.Unlock()
	path, err := findMihomoConfig()
	if err != nil {
		// FanoutYUNDAN can be used without Mihomo. Missing Mihomo must not
		// prevent a normal uninstall; there is no active config to clean.
		if !keepState {
			if removeErr := os.Remove(m.mihomoStatePath()); removeErr != nil && !os.IsNotExist(removeErr) {
				return removeErr
			}
		}
		return nil
	}
	config, previous, err := loadMihomoConfig(path)
	if err != nil {
		return err
	}
	state, err := loadOrMigrateMihomoState(m.mihomoStatePath(), path, config)
	if err != nil {
		return err
	}
	if len(state.Bindings) == 0 {
		if !keepState {
			if err := os.Remove(m.mihomoStatePath()); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		return nil
	}
	for _, binding := range state.Bindings {
		removeBindingFromConfig(config, binding)
	}
	if err := applyMihomoConfig(path, config, previous); err != nil {
		return err
	}
	if !keepState {
		if err := os.Remove(m.mihomoStatePath()); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func apiMihomoDelete(m *Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.mihomoMu.Lock()
		defer m.mihomoMu.Unlock()
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		path, err := findMihomoConfig()
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		config, previous, err := loadMihomoConfig(path)
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		state, err := loadOrMigrateMihomoState(m.mihomoStatePath(), path, config)
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		index := -1
		for i := range state.Bindings {
			if state.Bindings[i].Name == name {
				index = i
				break
			}
		}
		if index < 0 {
			writeJSON(w, 404, map[string]string{"error": "未找到由 FanoutYUNDAN 管理的 Mihomo 入站"})
			return
		}
		removeBindingFromConfig(config, state.Bindings[index])
		if err := applyMihomoConfig(path, config, previous); err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		state.Bindings = append(state.Bindings[:index], state.Bindings[index+1:]...)
		if err := saveMihomoState(m.mihomoStatePath(), state); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]string{"ok": "已删除 Mihomo 入站"})
	}
}

func validManagedName(name string) bool {
	if name == "" || len(name) > 48 {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}
