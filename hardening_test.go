package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestMutationRequiresSameOriginPost(t *testing.T) {
	called := false
	h := mutation(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})

	for _, tc := range []struct {
		method, origin string
		want           int
	}{
		{http.MethodGet, "", http.StatusMethodNotAllowed},
		{http.MethodPost, "https://attacker.example", http.StatusForbidden},
		{http.MethodPost, "http://panel.example:8899", http.StatusNoContent},
	} {
		called = false
		req := httptest.NewRequest(tc.method, "http://panel.example:8899/api/stop", nil)
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		res := httptest.NewRecorder()
		h(res, req)
		if res.Code != tc.want {
			t.Fatalf("%s origin %q: got %d, want %d", tc.method, tc.origin, res.Code, tc.want)
		}
		if called != (tc.want == http.StatusNoContent) {
			t.Fatalf("handler call mismatch for %s origin %q", tc.method, tc.origin)
		}
	}
}

func TestTunnelSnapshotConcurrentUpdates(t *testing.T) {
	tunnel := &Tunnel{Port: 10000, Status: "starting", Node: Node{HostName: "first"}}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				tunnel.setState("starting", "test")
				tunnel.setNode(Node{HostName: "next"})
				tunnel.setExitIP("192.0.2.1")
				_ = tunnel.snapshot()
			}
		}()
	}
	wg.Wait()
}

func TestClosedTunnelCannotReconnect(t *testing.T) {
	tunnel := &Tunnel{Status: "up"}
	tunnel.mu.Lock()
	tunnel.closed = true
	tunnel.mu.Unlock()
	if tunnel.beginReconnect() {
		t.Fatal("closed tunnel was allowed to reconnect")
	}
	if got := tunnel.snapshot().Status; got != "up" {
		t.Fatalf("failed reconnect changed status to %q", got)
	}
}

func TestPendingRebindSurvivesSyncFailure(t *testing.T) {
	tunnel := &Tunnel{Node: Node{HostName: "new-node"}, Status: "sync_failed"}
	tunnel.setPendingRebind("old-node")
	if got := tunnel.pendingRebind(); got != "old-node" {
		t.Fatalf("pending rebind = %q, want old-node", got)
	}
	tunnel.setPendingRebind("")
	if got := tunnel.pendingRebind(); got != "" {
		t.Fatalf("cleared pending rebind = %q", got)
	}
}

func TestMihomoTemplatesHideUnsupportedListeners(t *testing.T) {
	dir := t.TempDir()
	config := `listeners:
  - {name: ws, type: vless, listen: 127.0.0.1, port: 10001, ws-path: /cdn}
  - {name: grpc, type: vless, listen: 127.0.0.1, port: 10002, grpc-service-name: svc}
  - {name: reality, type: vless, listen: 127.0.0.1, port: 10003, reality-config: {dest: example.com:443}}
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(config), 0640); err != nil {
		t.Fatal(err)
	}
	backend := &mihomoBackend{configPath: path}
	templates, err := backend.templates()
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) != 1 || templates[0].Name != "ws" || templates[0].Network != "ws" {
		t.Fatalf("unexpected templates: %#v", templates)
	}
}

func TestPreserveFileMode(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "original")
	replacement := filepath.Join(dir, "replacement")
	if err := os.WriteFile(original, []byte("old"), 0640); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := preserveFileMetadata(replacement, info); err != nil {
		t.Fatal(err)
	}
	got, err := os.Stat(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode().Perm() != 0640 {
		t.Fatalf("replacement mode = %o, want 640", got.Mode().Perm())
	}
}

func TestSyncingTunnelIsIncludedInMihomoConfig(t *testing.T) {
	tunnel := testTunnel("vpn-jp", 24536)
	tunnel.Status = "syncing"
	out, err := mergeMihomoConfig([]byte(testMihomoConfig), []*nativeInbound{testInbound("vpn-jp")}, []*Tunnel{tunnel})
	if err != nil {
		t.Fatal(err)
	}
	if !containsAll(string(out), "fy-out-vpn-jp", "port: 24536") {
		t.Fatalf("syncing tunnel missing from config:\n%s", out)
	}
}

func containsAll(s string, values ...string) bool {
	for _, value := range values {
		if !strings.Contains(s, value) {
			return false
		}
	}
	return true
}
