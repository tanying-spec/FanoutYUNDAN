package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
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
	result, err := parseMihomoBindings("/etc/mihomo/fanout-bindings.db", out)
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, result)
}

func parseMihomoBindings(path, mhOutput string) ([]mihomoInbound, error) {
	links := map[string]string{}
	for _, link := range vlessLinkRE.FindAllString(mhOutput, -1) {
		parsed, err := url.Parse(link)
		if err != nil || parsed.Fragment == "" {
			continue
		}
		name, err := url.PathUnescape(parsed.Fragment)
		if err == nil {
			links[name] = link
		}
	}
	blob, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []mihomoInbound{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取 Mihomo fanout 绑定失败: %v", err)
	}
	result := []mihomoInbound{}
	for _, line := range strings.Split(string(blob), "\n") {
		fields := strings.Split(line, "|")
		if len(fields) < 4 {
			continue
		}
		port := 0
		if _, err := fmt.Sscanf(fields[3], "%d", &port); err != nil || port < 1 {
			continue
		}
		result = append(result, mihomoInbound{
			Name: fields[0], Node: fields[1], UUID: fields[2], Port: port, Link: links[fields[0]],
		})
	}
	return result, nil
}

func apiMihomoAdd(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	port := strings.TrimSpace(r.URL.Query().Get("port"))
	if name == "" || port == "" {
		writeJSON(w, 400, map[string]string{"error": "缺少节点名称或 SOCKS 端口"})
		return
	}
	if !regexp.MustCompile(`^[A-Za-z0-9._-]{1,48}$`).MatchString(name) {
		writeJSON(w, 400, map[string]string{"error": "节点名称只能包含字母、数字、点、下划线和短横线"})
		return
	}
	node, err := findMihomoSourceNode("/etc/mihomo/nodes.db")
	if err != nil {
		writeJSON(w, 409, map[string]string{"error": err.Error()})
		return
	}
	out, err := runMH("fanout", "add", name, node, port)
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "已创建 Mihomo 入站", "output": out})
}

func findMihomoSourceNode(path string) (string, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("无法读取 Mihomo 节点配置: %v", err)
	}
	var fallback string
	for _, line := range strings.Split(string(blob), "\n") {
		fields := strings.Split(line, "|")
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "vless-ws":
			return fields[1], nil
		case "vless-reality":
			if fallback == "" {
				fallback = fields[1]
			}
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("Mihomo 尚无 VLESS WS/Reality 基础入站，请先用 mh 创建一个 VLESS 节点")
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
