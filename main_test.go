package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
)

func TestAPINodesReturnsCompleteList(t *testing.T) {
	m := NewManager(1, t.TempDir(), "127.0.0.1")
	for i := 0; i < 250; i++ {
		m.nodes = append(m.nodes, Node{HostName: fmt.Sprintf("node-%03d", i)})
	}
	rec := httptest.NewRecorder()
	apiNodes(m).ServeHTTP(rec, httptest.NewRequest("GET", "/api/nodes", nil))
	var response struct {
		Nodes []Node `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Nodes) != 250 {
		t.Fatalf("complete node list was truncated to %d", len(response.Nodes))
	}
}
