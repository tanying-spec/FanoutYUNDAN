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

func TestBindingConfigReconcileIsIdempotent(t *testing.T) {
	config := testMihomoConfig()
	binding := mihomoInbound{ID: "abc123", Name: "JP-Fanout", UUID: "new-uuid", Template: "vless-ws", SocksPort: 28972}
	if err := addBindingToConfig(config, binding); err != nil {
		t.Fatal(err)
	}
	listener := anyMap(anySlice(config["listeners"])[0])
	user := anyMap(anySlice(listener["users"])[1])
	user["uuid"] = "overwritten"
	binding.SocksPort = 29999
	if err := addBindingToConfig(config, binding); err != nil {
		t.Fatal(err)
	}
	if len(anySlice(listener["users"])) != 2 || stringValue(user["uuid"]) != "new-uuid" {
		t.Fatalf("managed user was duplicated or not restored: %#v", listener["users"])
	}
	proxy := anyMap(anySlice(config["proxies"])[0])
	if len(anySlice(config["proxies"])) != 1 || intValue(proxy["port"]) != 29999 || len(anySlice(config["rules"])) != 2 {
		t.Fatalf("reconcile was not idempotent: %#v", config)
	}
}

func TestRemoveBindingCleansLegacyRouting(t *testing.T) {
	config := testMihomoConfig()
	binding := mihomoInbound{ID: "abc123", Name: "JP-Fanout", UUID: "new-uuid", Template: "vless-ws", SocksPort: 28972}
	listener := anyMap(anySlice(config["listeners"])[0])
	listener["users"] = append(anySlice(listener["users"]), map[string]any{"username": binding.Name, "uuid": binding.UUID})
	config["proxies"] = []any{map[string]any{"name": "fanout-JP-Fanout", "type": "socks5", "port": 28972}}
	config["rules"] = []any{"IN-USER,JP-Fanout,fanout-JP-Fanout", "MATCH,DIRECT"}
	removeBindingFromConfig(config, binding)
	if len(anySlice(listener["users"])) != 1 || len(anySlice(config["proxies"])) != 0 || len(anySlice(config["rules"])) != 1 {
		t.Fatalf("legacy routing was not removed: %#v", config)
	}
}

func TestBindingLinksForSupportedModes(t *testing.T) {
	tests := []struct {
		name string
		binding mihomoInbound
		want []string
	}{
		{"direct", mihomoInbound{Name: "Direct", UUID: "uuid", Protocol: "vless-ws", Mode: "direct", PublicAddress: "203.0.113.1", PublicPort: 80, Path: "/ws"}, []string{"@203.0.113.1:80", "security=none", "type=ws"}},
		{"cdn", mihomoInbound{Name: "CDN", UUID: "uuid", Protocol: "vless-ws", Mode: "cdn", PublicAddress: "edge.example", PublicPort: 443, Host: "origin.example", SNI: "edge.example", Path: "/ws"}, []string{"security=tls", "host=origin.example", "sni=edge.example"}},
		{"argo", mihomoInbound{Name: "Argo", UUID: "uuid", Protocol: "vless-ws", Mode: "argo", PublicAddress: "tunnel.example", PublicPort: 443, Host: "tunnel.example", SNI: "tunnel.example", Path: "/argo"}, []string{"security=tls", "host=tunnel.example", "path=%2Fargo"}},
		{"reality", mihomoInbound{Name: "Reality", UUID: "uuid", Protocol: "vless-reality", Mode: "reality", PublicAddress: "203.0.113.2", PublicPort: 443, SNI: "www.example.com", PublicKey: "public-key", ShortID: "abcd"}, []string{"security=reality", "pbk=public-key", "sid=abcd", "type=tcp"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			link := bindingLink(tc.binding)
			for _, want := range tc.want {
				if !strings.Contains(link, want) {
					t.Fatalf("link %q missing %q", link, want)
				}
			}
		})
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

func TestMigrateLegacyFanoutState(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("listeners: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nodes.db"), []byte("vless-ws|vless-ws|22039|old|/ws|edge.example|cdn|edge.example|443\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fanout-bindings.db"), []byte("JP-Fanout|vless-ws|11111111-1111-1111-1111-111111111111|28972|123\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config := testMihomoConfig()
	state, err := loadOrMigrateMihomoState(filepath.Join(dir, "state.json"), configPath, config)
	if err != nil || len(state.Bindings) != 1 {
		t.Fatalf("migration failed: %#v %v", state, err)
	}
	if state.Bindings[0].Mode != "cdn" || state.Bindings[0].PublicPort != 443 || state.Bindings[0].SocksPort != 28972 {
		t.Fatalf("unexpected migrated binding: %#v", state.Bindings[0])
	}
}

func TestValidManagedName(t *testing.T) {
	if !validManagedName("JP-Fanout_2") || validManagedName("日本节点") || validManagedName("") {
		t.Fatal("managed name validation failed")
	}
}
