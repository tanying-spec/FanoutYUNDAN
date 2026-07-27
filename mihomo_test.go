package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testMihomoConfig() map[string]any {
	return map[string]any{
		"listeners": []any{
			map[string]any{
				"name": "vless-ws", "type": "vless", "port": 22039,
				"users": []any{map[string]any{"username": "base", "uuid": "base-uuid"}},
				"ws-path": "/ws-path",
			},
		},
		"proxies": []any{},
		"rules":   []any{"MATCH,DIRECT"},
	}
}

func TestMihomoTemplatesUsesCDNMetadata(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("listeners: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	record := "vless-ws|vless-ws|22039|old|/ws-path|cdn.example.com|cdn|cdn.example.com|443\n"
	if err := os.WriteFile(filepath.Join(dir, "nodes.db"), []byte(record), 0600); err != nil {
		t.Fatal(err)
	}
	templates := mihomoTemplates(configPath, testMihomoConfig())
	if len(templates) != 1 {
		t.Fatalf("got %d templates", len(templates))
	}
	got := templates[0]
	if got.Mode != "cdn" || got.PublicAddress != "cdn.example.com" || got.PublicPort != 443 || got.SNI != "cdn.example.com" {
		t.Fatalf("unexpected CDN template: %#v", got)
	}
}

func TestBindingLinkCDN(t *testing.T) {
	binding := mihomoInbound{Name: "JP-Fanout", UUID: "11111111-1111-1111-1111-111111111111", Protocol: "vless-ws", Mode: "cdn", PublicAddress: "cdn.example.com", PublicPort: 443, Host: "cdn.example.com", SNI: "cdn.example.com", Path: "/edge"}
	link := bindingLink(binding)
	for _, want := range []string{"@cdn.example.com:443", "security=tls", "sni=cdn.example.com", "host=cdn.example.com", "path=%2Fedge"} {
		if !strings.Contains(link, want) {
			t.Fatalf("link %q missing %q", link, want)
		}
	}
}

func TestAddAndRemoveBindingConfig(t *testing.T) {
	config := testMihomoConfig()
	binding := mihomoInbound{ID: "abc123", Name: "JP-Fanout", UUID: "new-uuid", Template: "vless-ws", SocksPort: 28972}
	if err := addBindingToConfig(config, binding); err != nil {
		t.Fatal(err)
	}
	listener := anyMap(anySlice(config["listeners"])[0])
	if len(anySlice(listener["users"])) != 2 {
		t.Fatalf("user was not added: %#v", listener["users"])
	}
	if len(anySlice(config["proxies"])) != 1 || len(anySlice(config["rules"])) != 2 {
		t.Fatalf("routing was not added: %#v %#v", config["proxies"], config["rules"])
	}
	removeBindingFromConfig(config, binding)
	if len(anySlice(listener["users"])) != 1 || len(anySlice(config["proxies"])) != 0 || len(anySlice(config["rules"])) != 1 {
		t.Fatalf("binding was not removed cleanly: %#v", config)
	}
}

func TestMihomoStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	want := mihomoState{Version: mihomoStateVersion, Bindings: []mihomoInbound{{ID: "one", Name: "JP-Fanout", SocksPort: 28972}}}
	if err := saveMihomoState(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadMihomoState(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Bindings) != 1 || got.Bindings[0].Name != "JP-Fanout" {
		t.Fatalf("unexpected state: %#v", got)
	}
}

func TestValidManagedName(t *testing.T) {
	if !validManagedName("JP-Fanout_2") || validManagedName("日本节点") || validManagedName("") {
		t.Fatal("managed name validation failed")
	}
}
