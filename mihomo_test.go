package main

import (
	"os"
	"path/filepath"
	"testing"
)

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
