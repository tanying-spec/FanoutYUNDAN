package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var vlessLinkRE = regexp.MustCompile(`vless://\S+`)

type mihomoInbound struct {
	Name string `json:"name"`
	Node string `json:"node"`
	UUID string `json:"uuid"`
	Port int    `json:"port"`
	Link string `json:"link"`
}

func runMH(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	path := "/usr/local/bin/mh"
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("未找到 Mihomo 管理命令 mh")
	}
	cmd := exec.CommandContext(ctx, path, args...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return "", fmt.Errorf("Mihomo 操作超时")
	}
	if err != nil {
		return "", fmt.Errorf("Mihomo 操作失败: %s", strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func apiMihomoInbounds(w http.ResponseWriter, r *http.Request) {
	out, err := runMH("fanout", "list")
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	var result []mihomoInbound
	var pending *mihomoInbound
	for _, line := range strings.Split(out, "\n") {
		link := vlessLinkRE.FindString(line)
		if link != "" && pending != nil {
			pending.Link = link
			result = append(result, *pending)
			pending = nil
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		port := 0
		fmt.Sscanf(fields[2], "SOCKS=%d", &port)
		if port > 0 {
			pending = &mihomoInbound{Name: strings.TrimSpace(fields[0]), Node: strings.TrimPrefix(fields[1], "复用节点="), Port: port}
		}
	}
	writeJSON(w, 200, result)
}

func apiMihomoAdd(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	node := strings.TrimSpace(r.URL.Query().Get("node"))
	port := strings.TrimSpace(r.URL.Query().Get("port"))
	if name == "" || node == "" || port == "" {
		writeJSON(w, 400, map[string]string{"error": "缺少节点名称、复用节点或 SOCKS 端口"})
		return
	}
	if !regexp.MustCompile(`^[A-Za-z0-9._-]{1,48}$`).MatchString(name) {
		writeJSON(w, 400, map[string]string{"error": "节点名称只能包含字母、数字、点、下划线和短横线"})
		return
	}
	out, err := runMH("fanout", "add", name, "vless-ws", port)
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "已创建 Mihomo 入站", "output": out})
}

func apiMihomoDelete(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeJSON(w, 400, map[string]string{"error": "缺少入站名称"})
		return
	}
	old := os.Getenv("MIHOMO_FANOUT_DELETE_CONFIRM")
	_ = os.Setenv("MIHOMO_FANOUT_DELETE_CONFIRM", "DELETE")
	defer os.Setenv("MIHOMO_FANOUT_DELETE_CONFIRM", old)
	if _, err := runMH("fanout", "delete", name); err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "已删除 Mihomo 入站"})
}
