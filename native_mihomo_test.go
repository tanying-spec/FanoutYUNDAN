package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testMihomoConfig = `
listeners:
  - name: vless-ws
    type: vless
    listen: 127.0.0.1
    port: 48005
    users:
      - username: personal
        uuid: 11111111-1111-4111-8111-111111111111
    ws-path: /keep-me
proxies:
  - name: personal-proxy
    type: direct
rules:
  - DOMAIN,example.com,personal-proxy
  - MATCH,DIRECT
`

func testInbound(bound string) *nativeInbound {
	return &nativeInbound{
		ID: 1, StableID: "abc123", Template: "vless-ws", ListenerPort: 48005,
		Protocol: "vless", Remark: "JP-Fanout", Enable: true, BoundTo: bound,
		Clients: []nativeClient{{Email: "client", Username: "fy-abc123-1", ID: "22222222-2222-4222-8222-222222222222", Enable: true}},
	}
}

func testTunnel(host string, port int) *Tunnel {
	return &Tunnel{Port: port, Status: "up", Node: Node{HostName: host}}
}

func TestMergeMihomoConfigPreservesUserConfig(t *testing.T) {
	out, err := mergeMihomoConfig([]byte(testMihomoConfig), []*nativeInbound{testInbound("vpn-jp")}, []*Tunnel{testTunnel("vpn-jp", 24536)})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"username: personal", "uuid: 11111111-1111-4111-8111-111111111111", "ws-path: /keep-me", "name: personal-proxy", "DOMAIN,example.com,personal-proxy", "IN-USER,fy-abc123-1,fy-out-vpn-jp"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Index(s, "DOMAIN,example.com,personal-proxy") > strings.Index(s, "IN-USER,fy-abc123-1") {
		t.Fatal("managed rule changed existing rule priority")
	}
	if strings.Index(s, "IN-USER,fy-abc123-1") > strings.Index(s, "MATCH,DIRECT") {
		t.Fatal("managed rule inserted after MATCH")
	}
}

func TestMergeMihomoConfigIsIdempotent(t *testing.T) {
	inbounds := []*nativeInbound{testInbound("vpn-jp")}
	tunnels := []*Tunnel{testTunnel("vpn-jp", 24536)}
	one, err := mergeMihomoConfig([]byte(testMihomoConfig), inbounds, tunnels)
	if err != nil {
		t.Fatal(err)
	}
	two, err := mergeMihomoConfig(one, inbounds, tunnels)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(one, two) {
		t.Fatalf("second merge changed config:\n%s\n---\n%s", one, two)
	}
	if strings.Count(string(two), "fy-out-vpn-jp") != 2 {
		t.Fatalf("managed proxy/rule duplicated:\n%s", two)
	}
}

func TestMergeMihomoConfigRebindKeepsCredential(t *testing.T) {
	ib := testInbound("vpn-jp")
	one, err := mergeMihomoConfig([]byte(testMihomoConfig), []*nativeInbound{ib}, []*Tunnel{testTunnel("vpn-jp", 24536)})
	if err != nil {
		t.Fatal(err)
	}
	ib.BoundTo = "vpn-us"
	two, err := mergeMihomoConfig(one, []*nativeInbound{ib}, []*Tunnel{testTunnel("vpn-us", 24536)})
	if err != nil {
		t.Fatal(err)
	}
	s := string(two)
	if !strings.Contains(s, ib.Clients[0].ID) {
		t.Fatal("rebind changed or removed UUID")
	}
	if strings.Contains(s, "fy-out-vpn-jp") || !strings.Contains(s, "fy-out-vpn-us") {
		t.Fatalf("outbound was not replaced:\n%s", s)
	}
}

func TestMergeMihomoConfigRejectsMissingTemplate(t *testing.T) {
	ib := testInbound("vpn-jp")
	ib.Template = "missing"
	_, err := mergeMihomoConfig([]byte(testMihomoConfig), []*nativeInbound{ib}, []*Tunnel{testTunnel("vpn-jp", 24536)})
	if err == nil {
		t.Fatal("expected missing listener error")
	}
}

func TestMigrateLegacyMihomoStoreKeepsLinkAndBinding(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"version":1,"bindings":[{"id":"11c866adbc24","name":"JP-Fanout","uuid":"5c60e3ef-7b45-4077-8692-6ea5866a7a69","template":"vless-ws","protocol":"vless-ws","mode":"argo","port":24536,"listener_port":48005,"public_address":"argoukk.example.com","public_port":443,"host":"argoukk.example.com","sni":"argoukk.example.com","path":"/bDFWNgEoYO"}]}`
	state := `{"tunnels":[{"port":24536,"hostname":"vpn356607128"}]}`
	if err := os.WriteFile(filepath.Join(dir, "mihomo-inbounds.json"), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	st, err := loadNativeStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Inbounds) != 1 {
		t.Fatalf("got %d inbounds", len(st.Inbounds))
	}
	ib := st.Inbounds[0]
	if ib.StableID != "11c866adbc24" || ib.BoundTo != "vpn356607128" || ib.ListenerPort != 48005 {
		t.Fatalf("bad migration: %#v", ib)
	}
	link := shareLink(ib, ib.Clients[0], "wrong.example.com")
	for _, want := range []string{"5c60e3ef-7b45-4077-8692-6ea5866a7a69@argoukk.example.com:443", "security=tls", "type=ws", "path=%2FbDFWNgEoYO"} {
		if !strings.Contains(link, want) {
			t.Fatalf("link missing %q: %s", want, link)
		}
	}
}

func TestMigrateLegacyPipeBindings(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	bindingsPath := filepath.Join(dir, "fanout-bindings.db")
	config := `listeners:
  - name: hy2
    type: hysteria2
    listen: 0.0.0.0
    port: 26269
    users:
      personal-hy2: keep-this-password
  - name: vless-ws
    type: vless
    listen: 0.0.0.0
    port: 22039
    users:
      - username: personal
        uuid: 11111111-1111-4111-8111-111111111111
      - username: JP-Fanout
        uuid: d3cc56bb-09e4-40b5-9c47-777692130fa4
    ws-path: /legacy-path
proxies:
  - name: fanout-JP-Fanout
    type: socks5
    server: 127.0.0.1
    port: 28972
rules:
  - IN-USER,JP-Fanout,fanout-JP-Fanout
  - MATCH,DIRECT
`
	state := `{"tunnels":[{"port":28972,"hostname":"vpn-jp"}]}`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bindingsPath, []byte("JP-Fanout|vless-ws|d3cc56bb-09e4-40b5-9c47-777692130fa4|28972|1785140268\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MIHOMO_CONFIG", configPath)
	t.Setenv("FANOUT_YUNDAN_LEGACY_BINDINGS", bindingsPath)

	st, err := loadNativeStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Inbounds) != 1 {
		t.Fatalf("got %d inbounds", len(st.Inbounds))
	}
	ib := st.Inbounds[0]
	if ib.Remark != "JP-Fanout" || ib.Template != "vless-ws" || ib.ListenerPort != 22039 || ib.Path != "/legacy-path" || ib.BoundTo != "vpn-jp" {
		t.Fatalf("bad migration: %#v", ib)
	}
	if ib.Clients[0].ID != "d3cc56bb-09e4-40b5-9c47-777692130fa4" || ib.Clients[0].Username != "JP-Fanout" {
		t.Fatalf("credential changed: %#v", ib.Clients[0])
	}
	if _, err := os.Stat(filepath.Join(dir, "native.json")); err != nil {
		t.Fatalf("migration was not persisted: %v", err)
	}

	out, err := mergeMihomoConfig([]byte(config), st.Inbounds, []*Tunnel{testTunnel("vpn-jp", 28972)})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "fanout-JP-Fanout") || !strings.Contains(s, "fy-out-vpn-jp") {
		t.Fatalf("legacy proxy/rule not replaced:\n%s", s)
	}
	if !strings.Contains(s, "username: personal") || !strings.Contains(s, "uuid: d3cc56bb-09e4-40b5-9c47-777692130fa4") {
		t.Fatalf("listener users were not preserved:\n%s", s)
	}
	if !strings.Contains(s, "personal-hy2: keep-this-password") {
		t.Fatalf("non-VLESS listener was changed:\n%s", s)
	}
}

func TestShareLinkEntryModes(t *testing.T) {
	client := nativeClient{ID: "22222222-2222-4222-8222-222222222222", Enable: true}
	direct := &nativeInbound{Port: 48005, PublicPort: 48005, Protocol: "vless", Network: "ws", Path: "/direct", Security: "none", Remark: "Direct"}
	directLink := shareLink(direct, client, "203.0.113.10")
	for _, want := range []string{"@203.0.113.10:48005", "security=none", "path=%2Fdirect", "type=ws"} {
		if !strings.Contains(directLink, want) {
			t.Fatalf("direct link missing %q: %s", want, directLink)
		}
	}

	cdn := &nativeInbound{Port: 443, PublicAddress: "cdn.example.com", PublicPort: 443, Mode: "cdn", Protocol: "vless", Network: "ws", Path: "/cdn", Host: "cdn.example.com", Security: "tls", TLS: &tlsConfig{ServerName: "cdn.example.com"}, Remark: "CDN"}
	cdnLink := shareLink(cdn, client, "wrong.example.com")
	for _, want := range []string{"@cdn.example.com:443", "security=tls", "sni=cdn.example.com", "host=cdn.example.com", "fp=chrome", "path=%2Fcdn"} {
		if !strings.Contains(cdnLink, want) {
			t.Fatalf("cdn link missing %q: %s", want, cdnLink)
		}
	}
}
