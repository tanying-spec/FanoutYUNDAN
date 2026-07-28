package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// nativeClient 是一个可连接的客户端凭据。
// 复制入站时同一个 client 会挂到所有出口上，用户换出口只需要改端口。
type nativeClient struct {
	Email    string `json:"email"`
	Username string `json:"username,omitempty"`
	ID       string `json:"id"`       // vless/vmess 用 UUID
	Password string `json:"password"` // trojan 用密码
	Enable   bool   `json:"enable"`
	// Flow 只对 VLESS 有意义，取值 "" 或 xtls-rprx-vision。
	// Vision 要求底层是 TCP + TLS/REALITY，其他组合下 Xray 会直接拒绝启动。
	Flow string `json:"flow,omitempty"`
}

// nativeInbound 是自建模式下的一个入站。
//
// 字段刻意贴着 3x-ui 的入站语义，这样两种后端在界面上表现一致。
type nativeInbound struct {
	ID            int    `json:"id"`
	StableID      string `json:"stable_id,omitempty"`
	Port          int    `json:"port"`
	Template      string `json:"template,omitempty"`
	ListenerPort  int    `json:"listener_port,omitempty"`
	PublicAddress string `json:"public_address,omitempty"`
	PublicPort    int    `json:"public_port,omitempty"`
	Mode          string `json:"mode,omitempty"`
	Protocol      string `json:"protocol"` // vless | vmess | trojan
	Network       string `json:"network"`  // tcp | ws | grpc | httpupgrade | xhttp
	Path          string `json:"path"`     // ws/httpupgrade/xhttp 路径，grpc 用作 serviceName
	Host          string `json:"host"`     // ws/httpupgrade/xhttp 的 Host 头
	// Security 是传输层安全：none | tls | reality
	Security string         `json:"security"`
	TLS      *tlsConfig     `json:"tls,omitempty"`
	Reality  *realityConfig `json:"reality,omitempty"`
	Remark   string         `json:"remark"`
	Enable   bool           `json:"enable"`
	Clients  []nativeClient `json:"clients"`
	// BoundTo 是绑定的节点主机名经 sanitizeTag 后的形式，空表示直连
	BoundTo string `json:"bound_to"`
}

// tlsConfig 是标准 TLS 的配置。证书要么由用户提供路径，要么 fanout 生成自签的。
type tlsConfig struct {
	ServerName string `json:"server_name"`
	CertFile   string `json:"cert_file"`
	KeyFile    string `json:"key_file"`
	// SelfSigned 记录证书是 fanout 生成的，分享链接要带 allowInsecure
	SelfSigned bool `json:"self_signed"`
	// CertSha256 是证书的 SHA-256 指纹（十六进制）。
	// 自签证书客户端验不过，Xray 26.x 起 allowInsecure 已被移除，
	// 改为在链接里带指纹让客户端固定信任这一张证书。
	CertSha256 string `json:"cert_sha256,omitempty"`
}

// realityConfig 是 REALITY 的配置。
//
// PublicKey 服务端用不到，但客户端必须填，所以一并存下来供生成分享链接。
type realityConfig struct {
	Dest        string   `json:"dest"` // 借用的真实站点，如 www.microsoft.com:443
	ServerNames []string `json:"server_names"`
	PrivateKey  string   `json:"private_key"`
	PublicKey   string   `json:"public_key"`
	ShortIDs    []string `json:"short_ids"`
	Fingerprint string   `json:"fingerprint"` // 客户端指纹，如 chrome
}

// tag 复原这个入站在 Xray 里的 inboundTag，格式与 3x-ui 保持一致。
func (n *nativeInbound) tag() string {
	return "fy-in-" + n.stableID()
}

func (n *nativeInbound) stableID() string {
	if n.StableID != "" {
		return n.StableID
	}
	return fmt.Sprintf("%d", n.ID)
}

func (n *nativeInbound) clientUsername(index int, c *nativeClient) string {
	if c.Username == "" {
		c.Username = fmt.Sprintf("fy-%s-%d", n.stableID(), index+1)
	}
	return c.Username
}

func (n *nativeInbound) netOrTCP() string {
	if n.Network == "" {
		return "tcp"
	}
	return n.Network
}

func (n *nativeInbound) securityOrNone() string {
	if n.Security == "" {
		return "none"
	}
	return n.Security
}

// nativeStore 是自建模式的持久状态。
type nativeStore struct {
	NextID   int              `json:"next_id"`
	Inbounds []*nativeInbound `json:"inbounds"`
}

func nativeStatePath(dir string) string { return filepath.Join(dir, "native.json") }

func loadNativeStore(dir string) (*nativeStore, error) {
	blob, err := os.ReadFile(nativeStatePath(dir))
	if os.IsNotExist(err) {
		st, migrateErr := migrateLegacyMihomoStore(dir)
		if migrateErr == nil && len(st.Inbounds) > 0 {
			migrateErr = st.save(dir)
		}
		return st, migrateErr
	}
	if err != nil {
		return nil, err
	}
	var st nativeStore
	if err := json.Unmarshal(blob, &st); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", nativeStatePath(dir), err)
	}
	if st.NextID < 1 {
		st.NextID = 1
	}
	for _, ib := range st.Inbounds {
		if ib.StableID == "" {
			ib.StableID = randomHex(6)
		}
		if ib.ListenerPort == 0 {
			ib.ListenerPort = ib.Port
		}
		if ib.PublicPort == 0 {
			ib.PublicPort = ib.Port
		}
		for i := range ib.Clients {
			ib.clientUsername(i, &ib.Clients[i])
		}
	}
	return &st, nil
}

type legacyMihomoState struct {
	Bindings []struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		UUID          string `json:"uuid"`
		Template      string `json:"template"`
		Protocol      string `json:"protocol"`
		Mode          string `json:"mode"`
		Port          int    `json:"port"`
		ListenerPort  int    `json:"listener_port"`
		PublicPort    int    `json:"public_port"`
		PublicAddress string `json:"public_address"`
		Host          string `json:"host"`
		SNI           string `json:"sni"`
		Path          string `json:"path"`
	} `json:"bindings"`
}

func migrateLegacyMihomoStore(dir string) (*nativeStore, error) {
	st := &nativeStore{NextID: 1}
	blob, err := os.ReadFile(filepath.Join(dir, "mihomo-inbounds.json"))
	if os.IsNotExist(err) {
		return migrateLegacyPipeBindings(dir)
	}
	if err != nil {
		return nil, err
	}
	var legacy legacyMihomoState
	if err := json.Unmarshal(blob, &legacy); err != nil {
		return nil, fmt.Errorf("解析旧 Mihomo 入站状态失败: %w", err)
	}

	portsToHost := map[int]string{}
	if stateBlob, readErr := os.ReadFile(filepath.Join(dir, "state.json")); readErr == nil {
		var state struct {
			Tunnels []struct {
				Port     int    `json:"port"`
				HostName string `json:"hostname"`
			} `json:"tunnels"`
		}
		if json.Unmarshal(stateBlob, &state) == nil {
			for _, tunnel := range state.Tunnels {
				portsToHost[tunnel.Port] = sanitizeTag(tunnel.HostName)
			}
		}
	}
	for _, old := range legacy.Bindings {
		id := old.ID
		if id == "" {
			id = randomHex(6)
		}
		name := old.Name
		if name == "" {
			name = "Fanout-" + id
		}
		listenerPort := old.ListenerPort
		if listenerPort == 0 {
			listenerPort = old.PublicPort
		}
		publicPort := old.PublicPort
		if publicPort == 0 {
			publicPort = listenerPort
		}
		st.Inbounds = append(st.Inbounds, &nativeInbound{
			ID: st.NextID, StableID: id, Port: publicPort, Template: old.Template,
			ListenerPort: listenerPort, PublicAddress: old.PublicAddress, PublicPort: publicPort, Mode: old.Mode,
			Protocol: "vless", Network: "ws", Path: old.Path, Host: old.Host, Security: securityForMode(old.Mode),
			TLS: &tlsConfig{ServerName: old.SNI}, Remark: name, Enable: true, BoundTo: portsToHost[old.Port],
			Clients: []nativeClient{{Email: name, Username: name, ID: old.UUID, Enable: true}},
		})
		st.NextID++
	}
	return st, nil
}

func securityForMode(mode string) string {
	switch strings.ToLower(mode) {
	case "argo", "cdn", "tls":
		return "tls"
	}
	return "none"
}

// migrateLegacyPipeBindings imports the original mh fanout database. That
// integration stored one pipe-delimited record per Mihomo user.
func migrateLegacyPipeBindings(dir string) (*nativeStore, error) {
	st := &nativeStore{NextID: 1}
	bindingsPath := os.Getenv("FANOUT_YUNDAN_LEGACY_BINDINGS")
	if bindingsPath == "" {
		bindingsPath = "/etc/mihomo/fanout-bindings.db"
	}
	blob, err := os.ReadFile(bindingsPath)
	if os.IsNotExist(err) {
		return st, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取旧 fanout 绑定失败: %w", err)
	}

	configPath := os.Getenv("MIHOMO_CONFIG")
	if configPath == "" {
		for _, candidate := range []string{"/etc/mihomo/config.yaml", "/etc/mihomo/config.yml", "/root/.config/mihomo/config.yaml"} {
			if _, statErr := os.Stat(candidate); statErr == nil {
				configPath = candidate
				break
			}
		}
	}
	if configPath == "" {
		return nil, fmt.Errorf("迁移旧 fanout 绑定时找不到 Mihomo 配置")
	}
	configBlob, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("读取 Mihomo 配置失败: %w", err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(configBlob, &cfg); err != nil {
		return nil, fmt.Errorf("解析 Mihomo 配置失败: %w", err)
	}

	listeners := map[string]map[string]any{}
	listenerList, _ := cfg["listeners"].([]any)
	for _, raw := range listenerList {
		if listener, ok := raw.(map[string]any); ok {
			listeners[fmt.Sprint(listener["name"])] = listener
		}
	}
	portsToHost := persistedPortsToHosts(dir)
	for _, line := range strings.Split(string(blob), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 4 {
			return nil, fmt.Errorf("旧 fanout 绑定格式无效: %q", line)
		}
		socksPort, parseErr := strconv.Atoi(parts[3])
		if parseErr != nil {
			return nil, fmt.Errorf("旧 fanout SOCKS 端口无效: %q", parts[3])
		}
		name, template, uuid := parts[0], parts[1], parts[2]
		listener := listeners[template]
		if listener == nil {
			return nil, fmt.Errorf("旧 fanout 模板 %q 在 Mihomo 中不存在", template)
		}
		network, path := "tcp", ""
		if wsPath := strings.TrimSpace(fmt.Sprint(listener["ws-path"])); wsPath != "" && wsPath != "<nil>" {
			network, path = "ws", wsPath
		}
		listenerPort := yamlInt(listener["port"])
		st.Inbounds = append(st.Inbounds, &nativeInbound{
			ID: st.NextID, StableID: randomHex(6), Port: listenerPort,
			Template: template, ListenerPort: listenerPort, PublicPort: listenerPort,
			Protocol: "vless", Network: network, Path: path, Security: "none",
			Remark: name, Enable: true, BoundTo: portsToHost[socksPort],
			Clients: []nativeClient{{Email: name, Username: name, ID: uuid, Enable: true}},
		})
		st.NextID++
	}
	return st, nil
}

func persistedPortsToHosts(dir string) map[int]string {
	out := map[int]string{}
	stateBlob, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return out
	}
	var state struct {
		Tunnels []struct {
			Port     int    `json:"port"`
			HostName string `json:"hostname"`
		} `json:"tunnels"`
	}
	if json.Unmarshal(stateBlob, &state) == nil {
		for _, tunnel := range state.Tunnels {
			out[tunnel.Port] = sanitizeTag(tunnel.HostName)
		}
	}
	return out
}

func (s *nativeStore) save(dir string) error {
	blob, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := nativeStatePath(dir) + ".tmp"
	if err := os.WriteFile(tmp, blob, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, nativeStatePath(dir))
}

func (s *nativeStore) byID(id int) *nativeInbound {
	for _, ib := range s.Inbounds {
		if ib.ID == id {
			return ib
		}
	}
	return nil
}

func (s *nativeStore) usedPorts() map[int]bool {
	used := map[int]bool{}
	for _, ib := range s.Inbounds {
		used[ib.Port] = true
	}
	return used
}

// sorted 返回按端口排序的入站，让界面顺序稳定。
func (s *nativeStore) sorted() []*nativeInbound {
	out := make([]*nativeInbound, len(s.Inbounds))
	copy(out, s.Inbounds)
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

// newUUID 生成 Xray 认的 UUID v4。
func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// 随机源不可用时退回一个仍然唯一的形式，避免建站直接失败
		return fmt.Sprintf("00000000-0000-4000-8000-%012x", os.Getpid())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return strings.Join([]string{h[0:8], h[8:12], h[12:16], h[16:20], h[20:32]}, "-")
}

// randomHex 生成 n 字节的随机十六进制串，用作 trojan 密码与 ws 路径。
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(fmt.Sprint(os.Getpid())))
	}
	return hex.EncodeToString(b)
}
