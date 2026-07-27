package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseMihomoBindingsUsesDatabaseAsSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fanout-bindings.db")
	data := "JP-Fanout|ws-main|11111111-1111-1111-1111-111111111111|24536|123\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	out := "\x1b[1;33mchanged heading\x1b[0m\nvless://11111111-1111-1111-1111-111111111111@example.com:443?type=ws#JP-Fanout\n"
	got, err := parseMihomoBindings(path, out)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "JP-Fanout" || got[0].Port != 24536 || got[0].Link == "" {
		t.Fatalf("unexpected bindings: %#v", got)
	}
}

func TestFindMihomoSourceNodePrefersWS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.db")
	data := "vless-reality|reality-main|443|rest\nvless-ws|ws-main|8443|rest\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := findMihomoSourceNode(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "ws-main" {
		t.Fatalf("got %q, want ws-main", got)
	}
}

func TestFindMihomoSourceNodeRejectsEmptyConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.db")
	if err := os.WriteFile(path, []byte("hysteria2|hy2-main|443|rest\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := findMihomoSourceNode(path); err == nil {
		t.Fatal("expected an error for config without VLESS source")
	}
}
