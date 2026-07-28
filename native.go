package main

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"sync"
)

// Native 是 fanout 自己跑 Xray 的后端，用在本机没装 3x-ui 的场合。
//
// 入站数据存在 native.json，Xray 的运行配置每次改动后整份重新生成。
// 全量重写比增量改省心：配置是纯函数产物，不会出现改了一半的中间态。
type Native struct {
	mu     sync.Mutex
	dir    string
	store  *nativeStore
	mihomo *mihomoBackend
}

func openNative(workDir string) (*Native, error) {
	if workDir == "" {
		return nil, fmt.Errorf("自建模式缺少工作目录")
	}
	backend, err := findMihomo()
	if err != nil {
		return nil, err
	}
	store, err := loadNativeStore(workDir)
	if err != nil {
		return nil, err
	}
	if restored, ok, recoverErr := recoverNativeStoreBackup(workDir, backend.configPath, store); recoverErr != nil {
		return nil, recoverErr
	} else if ok {
		store = restored
		if err := store.save(workDir); err != nil {
			return nil, fmt.Errorf("保存恢复的入站状态失败: %w", err)
		}
		log.Printf("已从备份恢复 %d 个 Mihomo 入站", len(store.Inbounds))
	}
	n := &Native{
		dir:    workDir,
		store:  store,
		mihomo: backend,
	}
	return n, nil
}

func (n *Native) Kind() string { return "native" }

func (n *Native) Describe() string {
	return fmt.Sprintf("直接管理 Mihomo（%s）", n.mihomo.configPath)
}

// apply 重新生成配置并重启 Xray，然后落盘。
// 调用方必须已持有 n.mu。
func (n *Native) apply(tunnels []*Tunnel) error {
	if err := n.mihomo.apply(n.store.sorted(), tunnels); err != nil {
		return err
	}
	return n.store.save(n.dir)
}

// OnTunnelsChanged 在隧道集合变化后重建配置。自建模式下出站直接由隧道列表
// 推导，所以隧道一变就要重新生成，否则新出口没有对应的 socks 出站。
func (n *Native) OnTunnelsChanged(tunnels []*Tunnel) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.apply(tunnels)
}

// Close 停掉自己拉起的 Xray。
func (n *Native) Close() {
	// Mihomo 是用户已有的系统服务，FanoutYUNDAN 退出时绝不停止它。
}

func (n *Native) Cleanup() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	disabled := make([]*nativeInbound, 0, len(n.store.Inbounds))
	for _, ib := range n.store.Inbounds {
		copy := *ib
		copy.Enable = false
		disabled = append(disabled, &copy)
	}
	return n.mihomo.apply(disabled, nil)
}

func (n *Native) Inbounds(live map[string]bool) ([]Inbound, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	list := n.store.sorted()
	out := make([]Inbound, 0, len(list))
	for _, ib := range list {
		out = append(out, Inbound{
			ID: ib.ID, Port: ib.Port, Protocol: ib.Protocol,
			Remark: ib.Remark, Enable: ib.Enable, Tag: ib.tag(),
			BoundTo: ib.BoundTo, BoundUp: live[ib.BoundTo],
		})
	}
	return out, nil
}

func (n *Native) InboundDetail(id int, publicHost string) (*InboundDetail, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	ib := n.store.byID(id)
	if ib == nil {
		return nil, fmt.Errorf("入站 %d 不存在", id)
	}
	detail := &InboundDetail{
		Inbound: Inbound{
			ID: ib.ID, Port: ib.Port, Protocol: ib.Protocol,
			Remark: ib.Remark, Enable: ib.Enable, Tag: ib.tag(),
			BoundTo: ib.BoundTo,
		},
		Listen:  "127.0.0.1",
		Network: ib.netOrTCP(),
		TLS:     ib.securityOrNone(),
	}
	for _, c := range ib.Clients {
		id := c.ID
		if ib.Protocol == "trojan" {
			id = c.Password
		}
		detail.Clients = append(detail.Clients, ClientInfo{Email: c.Email, ID: id, Enable: c.Enable})
		detail.Links = append(detail.Links, shareLink(ib, c, publicHost))
	}
	return detail, nil
}

func (n *Native) InboundLinks(ids []int, publicHost string) ([]string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	var out []string
	for _, id := range ids {
		ib := n.store.byID(id)
		if ib == nil {
			continue
		}
		for _, c := range ib.Clients {
			out = append(out, shareLink(ib, c, publicHost))
		}
	}
	return out, nil
}

func (n *Native) Bind(inboundTag string, hostname string, tunnels []*Tunnel) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	var target *Tunnel
	if hostname != "" {
		for _, t := range tunnels {
			if t.snapshot().Node.HostName == hostname {
				target = t
				break
			}
		}
		if target == nil {
			return fmt.Errorf("节点 %s 没有运行中的隧道", hostname)
		}
		targetSnap := target.snapshot()
		if !tunnelRoutable(targetSnap.Status) {
			return fmt.Errorf("节点 %s 的隧道还没连通（当前 %s）", hostname, targetSnap.Status)
		}
	}

	var found *nativeInbound
	for _, ib := range n.store.Inbounds {
		if ib.tag() == inboundTag {
			found = ib
			break
		}
	}
	if found == nil {
		return fmt.Errorf("入站 %s 不存在", inboundTag)
	}

	if target == nil {
		found.BoundTo = ""
	} else {
		found.BoundTo = sanitizeTag(target.snapshot().Node.HostName)
	}
	return n.apply(tunnels)
}

func (n *Native) Rebind(oldHost string, target *Tunnel, tunnels []*Tunnel) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	oldTag := sanitizeTag(oldHost)
	newTag := sanitizeTag(target.snapshot().Node.HostName)
	newLabel := exitLabel(target)
	for _, ib := range n.store.Inbounds {
		if ib.BoundTo != oldTag {
			continue
		}
		ib.BoundTo = newTag
		// 备注里带着旧出口的地区和 IP 尾段，换了节点要跟着改
		ib.Remark = renameExitSuffix(ib.Remark, newLabel)
	}
	return n.apply(tunnels)
}

func (n *Native) ResyncOutbound(t *Tunnel, tunnels []*Tunnel) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.apply(tunnels)
}

// CloneToTunnels 以某个入站为模板，为每条指定隧道复制一个入站并绑好出口。
//
// 客户端凭据整套沿用模板：同一个 UUID 能走所有出口，用户只改端口。
func (n *Native) CloneToTunnels(templateID int, hosts []string, tunnels []*Tunnel) ([]int, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	tpl := n.store.byID(templateID)
	if tpl == nil {
		return nil, fmt.Errorf("模板入站 %d 不存在", templateID)
	}

	byHost := map[string]*Tunnel{}
	for _, t := range tunnels {
		byHost[t.snapshot().Node.HostName] = t
	}

	created := []int{}
	for _, host := range hosts {
		t := byHost[host]
		if t == nil || t.snapshot().Status != "up" {
			continue
		}
		clients := make([]nativeClient, 0, len(tpl.Clients))
		for _, c := range tpl.Clients {
			c.ID, c.Password, c.Username = newUUID(), randomHex(8), ""
			clients = append(clients, c)
		}
		clone := &nativeInbound{
			ID:            n.store.NextID,
			StableID:      randomHex(6),
			Port:          tpl.Port,
			Template:      tpl.Template,
			ListenerPort:  tpl.ListenerPort,
			PublicAddress: tpl.PublicAddress,
			PublicPort:    tpl.PublicPort,
			Mode:          tpl.Mode,
			Protocol:      tpl.Protocol,
			Network:       tpl.Network,
			Path:          tpl.Path,
			Host:          tpl.Host,
			// 安全层必须跟着复制：漏掉的话从 REALITY/TLS 模板复制出来的
			// 入站会变成明文，而分享链接照样标着模板的协议，很难发现
			Security: tpl.Security,
			TLS:      tpl.TLS,
			Reality:  tpl.Reality,
			Remark:   cloneRemark(tpl.Remark, exitLabel(t)),
			Enable:   true,
			Clients:  clients,
			BoundTo:  sanitizeTag(t.snapshot().Node.HostName),
		}
		n.store.NextID++
		n.store.Inbounds = append(n.store.Inbounds, clone)
		created = append(created, clone.ID)
	}

	if len(created) == 0 {
		return created, fmt.Errorf("没有可用的隧道")
	}
	if err := n.apply(tunnels); err != nil {
		return created, err
	}
	return created, nil
}

func (n *Native) DeleteInbounds(ids []int, tunnels []*Tunnel) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	drop := map[int]bool{}
	for _, id := range ids {
		drop[id] = true
	}
	kept := make([]*nativeInbound, 0, len(n.store.Inbounds))
	for _, ib := range n.store.Inbounds {
		if !drop[ib.ID] {
			kept = append(kept, ib)
		}
	}
	n.store.Inbounds = kept
	return n.apply(tunnels)
}

// UpdateInbound 改端口、备注与启停。
//
// 端口变了 inboundTag 也跟着变（tag 里含端口），所以路由规则要一起重写；
// apply 是整份重建，天然覆盖了这点。
func (n *Native) UpdateInbound(id int, patch InboundPatch, tunnels []*Tunnel) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	ib := n.store.byID(id)
	if ib == nil {
		return fmt.Errorf("入站 %d 不存在", id)
	}

	if patch.Port != nil && *patch.Port != ib.Port {
		port := *patch.Port
		if port < 1 || port > 65535 {
			return fmt.Errorf("端口 %d 不在合法范围", port)
		}
		for _, other := range n.store.Inbounds {
			if other.ID != id && other.Port == port {
				return fmt.Errorf("端口 %d 已被入站 %q 占用", port, other.Remark)
			}
		}
		ib.Port = port
	}
	if patch.Remark != nil {
		if r := strings.TrimSpace(*patch.Remark); r != "" {
			ib.Remark = r
		}
	}
	if patch.Enable != nil {
		ib.Enable = *patch.Enable
	}
	return n.apply(tunnels)
}

// AddClient 给入站加一个客户端。同一入站上可以有多套凭据，便于分发给不同人。
func (n *Native) AddClient(id int, email string, tunnels []*Tunnel) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	ib := n.store.byID(id)
	if ib == nil {
		return fmt.Errorf("入站 %d 不存在", id)
	}

	email = strings.TrimSpace(email)
	if email == "" {
		email = fmt.Sprintf("%s-%d-%s", ib.Protocol, ib.Port, randomHex(3))
	}
	for _, c := range ib.Clients {
		if c.Email == email {
			return fmt.Errorf("客户端 %q 已存在", email)
		}
	}

	ib.Clients = append(ib.Clients, nativeClient{
		Email:    email,
		ID:       newUUID(),
		Password: randomHex(8),
		Enable:   true,
		Flow:     visionFlow(ib),
	})
	ib.clientUsername(len(ib.Clients)-1, &ib.Clients[len(ib.Clients)-1])
	return n.apply(tunnels)
}

// DeleteClient 摘掉一个客户端。留下最后一个是有意的：
// 没有任何客户端的入站在 Xray 里虽然合法，但谁也连不上，只会让人以为坏了。
func (n *Native) DeleteClient(id int, email string, tunnels []*Tunnel) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	ib := n.store.byID(id)
	if ib == nil {
		return fmt.Errorf("入站 %d 不存在", id)
	}
	if len(ib.Clients) <= 1 {
		return fmt.Errorf("这是最后一个客户端，删掉就没人能连了")
	}

	kept := make([]nativeClient, 0, len(ib.Clients))
	for _, c := range ib.Clients {
		if c.Email != email {
			kept = append(kept, c)
		}
	}
	if len(kept) == len(ib.Clients) {
		return fmt.Errorf("客户端 %q 不存在", email)
	}
	ib.Clients = kept
	return n.apply(tunnels)
}

// ResetClient 换一套新凭据，旧链接立即失效。
func (n *Native) ResetClient(id int, email string, tunnels []*Tunnel) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	ib := n.store.byID(id)
	if ib == nil {
		return fmt.Errorf("入站 %d 不存在", id)
	}
	for i := range ib.Clients {
		if ib.Clients[i].Email == email {
			ib.Clients[i].ID = newUUID()
			ib.Clients[i].Password = randomHex(8)
			return n.apply(tunnels)
		}
	}
	return fmt.Errorf("客户端 %q 不存在", email)
}

// visionFlow 沿用入站已有客户端的 flow，让新加的客户端与其余保持一致。
func visionFlow(ib *nativeInbound) string {
	for _, c := range ib.Clients {
		if c.Flow != "" {
			return c.Flow
		}
	}
	return ""
}

// NewInboundSpec 是自建模式下新建入站的参数。
//
// 留空的字段都有合理默认：端口随机、备注按协议加端口自动生成、
// 路径随机、REALITY 的密钥与 shortId 自动生成。
type NewInboundSpec struct {
	Template      string
	PublicAddress string
	PublicPort    int
	Mode          string
	Protocol      string
	Network       string
	Port          int
	Remark        string
	Path          string
	Host          string
	Security      string
	// Vision 请求给 VLESS 客户端启用 xtls-rprx-vision
	Vision bool

	// TLS：留空 CertFile 就生成自签证书
	ServerName string
	CertFile   string
	KeyFile    string

	// REALITY
	Dest        string
	ServerNames string // 逗号分隔，留空则从 Dest 推出来
	ShortID     string
	Fingerprint string
}

// nativeProtocols 是自建模式支持的协议，与前端下拉保持一致。
var nativeProtocols = map[string]bool{"vless": true}

// CreateInbound 新建一个入站，端口留空时随机分配。
func (n *Native) CreateInbound(spec NewInboundSpec, tunnels []*Tunnel) (*nativeInbound, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	templates, err := n.mihomo.templates()
	if err != nil {
		return nil, err
	}
	var selected *MihomoTemplate
	for i := range templates {
		if templates[i].Name == spec.Template || (spec.Template == "" && selected == nil) {
			selected = &templates[i]
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("找不到可用的 Mihomo VLESS listener 模板")
	}
	spec.Template = selected.Name
	mode := strings.ToLower(strings.TrimSpace(spec.Mode))
	if mode == "argo" || mode == "cdn" {
		if strings.TrimSpace(spec.PublicAddress) == "" {
			return nil, fmt.Errorf("CDN/Argo 模式必须填写公网域名")
		}
		spec.Security, spec.Host, spec.ServerName = "tls", spec.PublicAddress, spec.PublicAddress
		if spec.PublicPort == 0 {
			spec.PublicPort = 443
		}
	}
	proto := "vless"
	if proto == "" {
		proto = "vless"
	}
	if !nativeProtocols[proto] {
		return nil, fmt.Errorf("不支持的协议 %q", spec.Protocol)
	}
	network := selected.Network
	if !nativeNetworks[network] {
		return nil, fmt.Errorf("不支持的传输方式 %q", spec.Network)
	}
	security := strings.ToLower(strings.TrimSpace(spec.Security))
	if security == "" {
		security = "none"
	}
	if !nativeSecurities[security] {
		return nil, fmt.Errorf("不支持的安全层 %q", spec.Security)
	}
	// REALITY 靠模仿 TLS 握手工作，套在 ws/grpc 这类已有自己头部的传输上没有意义，
	// Xray 也不接受这种组合
	if security == "reality" && network != "tcp" && network != "xhttp" && network != "grpc" {
		return nil, fmt.Errorf("REALITY 不支持 %s 传输", network)
	}
	// VMess 自带加密，但 TLS 在这里是为了流量伪装而不是加密强度，
	// vmess+ws+tls 是很常见的组合，不该拦。

	port := spec.PublicPort
	if port == 0 {
		port = spec.Port
	}
	if port == 0 {
		port = selected.Port
	}

	path := selected.Path
	if path == "" {
		switch network {
		case "ws", "httpupgrade", "xhttp":
			path = "/" + randomHex(6)
		case "grpc":
			path = randomHex(6)
		}
	}

	remark := strings.TrimSpace(spec.Remark)
	if remark == "" {
		remark = fmt.Sprintf("%s-%d", proto, port)
	}

	ib := &nativeInbound{
		ID:            n.store.NextID,
		StableID:      randomHex(6),
		Port:          port,
		Template:      spec.Template,
		ListenerPort:  selected.Port,
		PublicAddress: strings.TrimSpace(spec.PublicAddress),
		PublicPort:    port,
		Mode:          mode,
		Protocol:      proto,
		Network:       network,
		Path:          path,
		Host:          strings.TrimSpace(spec.Host),
		Security:      security,
		Remark:        remark,
		Enable:        true,
	}

	switch security {
	case "tls":
		if mode == "argo" || mode == "cdn" {
			ib.TLS = &tlsConfig{ServerName: spec.ServerName}
		} else {
			conf, err := n.buildTLS(spec)
			if err != nil {
				return nil, err
			}
			ib.TLS = conf
		}
	case "reality":
		conf, err := n.buildReality(spec)
		if err != nil {
			return nil, err
		}
		ib.Reality = conf
	}

	flow := ""
	if spec.Vision {
		if !visionCapable(proto, network, security) {
			return nil, fmt.Errorf("xtls-rprx-vision 只能用于 VLESS + TCP + TLS/REALITY")
		}
		flow = "xtls-rprx-vision"
	}
	ib.Clients = []nativeClient{{
		Email:    fmt.Sprintf("%s-%d", proto, port),
		ID:       newUUID(),
		Password: randomHex(8),
		Flow:     flow,
		Enable:   true,
	}}
	ib.clientUsername(0, &ib.Clients[0])

	n.store.NextID++
	n.store.Inbounds = append(n.store.Inbounds, ib)

	if err := n.apply(tunnels); err != nil {
		// 起不来就别把坏入站留在库里
		n.store.Inbounds = n.store.Inbounds[:len(n.store.Inbounds)-1]
		n.store.NextID--
		_ = n.apply(tunnels)
		return nil, err
	}
	return ib, nil
}

// cloneRemark 给复制出来的入站起名，与 3x-ui 模式同一套规则。
func cloneRemark(base, label string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return label
	}
	return base + "-" + label
}

// shareLink 生成客户端可直接导入的分享链接。
func shareLink(ib *nativeInbound, c nativeClient, host string) string {
	if ib.PublicAddress != "" {
		host = ib.PublicAddress
	}
	port := ib.Port
	if ib.PublicPort != 0 {
		port = ib.PublicPort
	}
	net := ib.netOrTCP()
	sec := ib.securityOrNone()

	q := url.Values{}
	q.Set("type", net)
	q.Set("security", sec)

	switch net {
	case "ws", "httpupgrade", "xhttp":
		q.Set("path", ib.Path)
		if ib.Host != "" {
			q.Set("host", ib.Host)
		}
	case "grpc":
		q.Set("serviceName", strings.TrimPrefix(ib.Path, "/"))
	}

	switch sec {
	case "tls":
		if ib.Mode == "argo" || ib.Mode == "cdn" {
			q.Set("fp", "chrome")
		}
		if ib.TLS != nil {
			if ib.TLS.ServerName != "" {
				q.Set("sni", ib.TLS.ServerName)
			}
			// 自签证书验不过 CA。Xray 26.x 移除了 allowInsecure，
			// 改用证书指纹让客户端固定信任这一张。
			if ib.TLS.SelfSigned && ib.TLS.CertSha256 != "" {
				q.Set("pinSHA256", ib.TLS.CertSha256)
			}
		}
	case "reality":
		if ib.Reality != nil {
			if len(ib.Reality.ServerNames) > 0 {
				q.Set("sni", ib.Reality.ServerNames[0])
			}
			// pbk 是分享链接的通用写法，各家客户端都认；
			// 注意 Xray 26.x 自己的配置文件里这个字段叫 password 而不是 publicKey
			q.Set("pbk", ib.Reality.PublicKey)
			if len(ib.Reality.ShortIDs) > 0 {
				q.Set("sid", ib.Reality.ShortIDs[0])
			}
			if ib.Reality.Fingerprint != "" {
				q.Set("fp", ib.Reality.Fingerprint)
			}
		}
	}

	if c.Flow != "" && ib.Protocol == "vless" {
		q.Set("flow", c.Flow)
	}

	frag := url.PathEscape(ib.Remark)

	switch ib.Protocol {
	case "trojan":
		return fmt.Sprintf("trojan://%s@%s:%d?%s#%s", c.Password, host, port, q.Encode(), frag)
	case "vmess":
		// vmess 的 base64 形式各家客户端解析不一，用通用的 URI 形式
		q.Set("encryption", "auto")
		return fmt.Sprintf("vmess://%s@%s:%d?%s#%s", c.ID, host, port, q.Encode(), frag)
	default:
		q.Set("encryption", "none")
		return fmt.Sprintf("vless://%s@%s:%d?%s#%s", c.ID, host, port, q.Encode(), frag)
	}
}

// buildTLS 组装 TLS 配置。没给证书路径就生成一张自签的。
func (n *Native) buildTLS(spec NewInboundSpec) (*tlsConfig, error) {
	name := strings.TrimSpace(spec.ServerName)
	if name == "" {
		name = "localhost"
	}
	conf := &tlsConfig{ServerName: name}

	cert, key := strings.TrimSpace(spec.CertFile), strings.TrimSpace(spec.KeyFile)
	// 只填一个多半是漏填，静默退回自签会让用户以为用上了自己的证书
	if (cert == "") != (key == "") {
		return nil, fmt.Errorf("证书和私钥要成对填写，或者都留空用自签证书")
	}
	if cert != "" && key != "" {
		if _, err := os.Stat(cert); err != nil {
			return nil, fmt.Errorf("证书文件不可读: %w", err)
		}
		if _, err := os.Stat(key); err != nil {
			return nil, fmt.Errorf("私钥文件不可读: %w", err)
		}
		conf.CertFile, conf.KeyFile = cert, key
		return conf, nil
	}

	// 自签证书验不过 CA，靠链接里的证书指纹让客户端固定信任
	c, k, err := selfSignedCert(n.dir, name)
	if err != nil {
		return nil, err
	}
	conf.CertFile, conf.KeyFile, conf.SelfSigned = c, k, true
	// 指纹是自签证书唯一能让客户端验过的凭据，算不出来就没法生成可用链接
	fp, err := certFingerprint(c)
	if err != nil {
		return nil, err
	}
	conf.CertSha256 = fp
	return conf, nil
}

// buildReality 组装 REALITY 配置，密钥和 shortId 都自动生成。
func (n *Native) buildReality(spec NewInboundSpec) (*realityConfig, error) {
	dest := strings.TrimSpace(spec.Dest)
	if dest == "" {
		// REALITY 要跟 dest 完成一次真实 TLS1.3 握手，dest 不稳会让所有连接
		// 静默回落。microsoft.com 在部分机房握手经常走不完，这里选更可靠的。
		dest = "www.tesla.com:443"
	}
	if !strings.Contains(dest, ":") {
		dest += ":443"
	}

	var names []string
	for _, s := range strings.Split(spec.ServerNames, ",") {
		if s = strings.TrimSpace(s); s != "" {
			names = append(names, s)
		}
	}
	if len(names) == 0 {
		// 默认用 dest 的主机名：REALITY 要求 SNI 与被借用的站点一致
		names = []string{strings.SplitN(dest, ":", 2)[0]}
	}

	return nil, fmt.Errorf("Mihomo 后端当前只开放已验证的 VLESS 模板，不支持新建 REALITY")
	/*
		priv, pub, err := realityKeys("")
		if err != nil { return nil, err }
		if err := checkRealityDest(dest, names[0]); err != nil {
			return nil, fmt.Errorf("REALITY 目标站点不可用，换一个 dest: %w", err)
		}

		short := strings.TrimSpace(spec.ShortID)
		if short == "" {
			short = randomShortID()
		}
		fp := strings.TrimSpace(spec.Fingerprint)
		if fp == "" {
			fp = "chrome"
		}

		return &realityConfig{
			Dest:        dest,
			ServerNames: names,
			PrivateKey:  priv,
			PublicKey:   pub,
			ShortIDs:    []string{short},
			Fingerprint: fp,
		}, nil
	*/
}
