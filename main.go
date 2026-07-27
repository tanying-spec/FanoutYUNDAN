package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// version 由构建时通过 -ldflags 注入。
var version = "dev"

func main() {
	var (
		webPort   = flag.Int("web", 8899, "Web 管理端口")
		webBind   = flag.String("web-bind", "127.0.0.1", "Web 管理监听地址")
		socksBind = flag.String("socks-bind", "127.0.0.1", "SOCKS5 监听地址")
		maxSlots  = flag.Int("max", 1, "最多同时运行的隧道数")
		workDir   = flag.String("dir", "/var/lib/fanout-yundan", "工作目录")
	)
	showVersion := flag.Bool("version", false, "显示版本后退出")
	cleanupMihomo := flag.Bool("cleanup-mihomo", false, "移除 FanoutYUNDAN 管理的 Mihomo 配置后退出")
	keepMihomoState := flag.Bool("keep-mihomo-state", false, "清理 Mihomo 配置时保留 FanoutYUNDAN 绑定状态")
	flag.Parse()

	if *showVersion {
		fmt.Println("FanoutYUNDAN", version)
		return
	}

	if os.Geteuid() != 0 {
		log.Fatal("需要 root 权限（要创建 netns 和改 iptables）")
	}
	if *webPort < 1 || *webPort > 65535 {
		log.Fatal("Web 管理端口必须在 1-65535 之间")
	}
	if *maxSlots < 1 || *maxSlots > 20 {
		log.Fatal("隧道数量必须在 1-20 之间")
	}
	if net.ParseIP(*webBind) == nil || net.ParseIP(*socksBind) == nil {
		log.Fatal("监听地址必须是有效 IP")
	}
	if err := os.MkdirAll(*workDir, 0700); err != nil {
		log.Fatalf("创建工作目录失败: %v", err)
	}
	if *cleanupMihomo {
		mgr := NewManager(*maxSlots, *workDir, *socksBind)
		if err := mgr.cleanupMihomo(*keepMihomoState); err != nil {
			log.Fatal(err)
		}
		return
	}
	if n := cleanupOrphanOpenVPN(*workDir); n > 0 {
		log.Printf("已清理 %d 个 FanoutYUNDAN 遗留的 OpenVPN 进程", n)
	}
	if err := prepareHost(); err != nil {
		log.Fatal(err)
	}

	mgr := NewManager(*maxSlots, *workDir, *socksBind)
	log.Printf("正在拉取节点列表...")
	if n, err := mgr.RefreshNodes(); err != nil {
		log.Printf("拉取失败（可在 Web 界面重试）: %v", err)
	} else {
		log.Printf("已获取 %d 个节点", n)
	}
	go retryNodeRefresh(mgr)

	if n, err := mgr.restoreState(); err != nil {
		log.Printf("恢复上次状态失败: %v", err)
	} else if n > 0 {
		log.Printf("正在恢复上次的 %d 条隧道", n)
	}

	go mgr.WatchHealth()
	go mgr.WatchMihomo()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		log.Println("正在清理所有隧道...")
		mgr.Shutdown()
		os.Exit(0)
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/api/nodes", apiNodes(mgr))
	mux.HandleFunc("/api/tunnels", apiTunnels(mgr))
	mux.HandleFunc("/api/start", apiStart(mgr))
	mux.HandleFunc("/api/switch", apiSwitch(mgr))
	mux.HandleFunc("/api/stop", apiStop(mgr))
	mux.HandleFunc("/api/refresh", apiRefresh(mgr))
	mux.HandleFunc("/api/mihomo/status", apiMihomoStatus(mgr))
	mux.HandleFunc("/api/mihomo/templates", apiMihomoTemplates(mgr))
	mux.HandleFunc("/api/mihomo/inbounds", apiMihomoInbounds(mgr))
	mux.HandleFunc("/api/mihomo/add", apiMihomoAdd(mgr))
	mux.HandleFunc("/api/mihomo/bind", apiMihomoBind(mgr))
	mux.HandleFunc("/api/mihomo/delete", apiMihomoDelete(mgr))

	auth, created, err := NewAuth(*workDir)
	if err != nil {
		log.Fatalf("初始化访问口令失败: %v", err)
	}
	if created {
		log.Printf("已生成访问口令，见 %s", filepath.Join(*workDir, "password"))
	}

	basePath, bpCreated, err := LoadBasePath(*workDir)
	if err != nil {
		log.Fatalf("初始化访问路径失败: %v", err)
	}
	if bpCreated {
		log.Printf("已生成访问路径，见 %s", filepath.Join(*workDir, "basepath"))
	}

	addr := net.JoinHostPort(*webBind, strconv.Itoa(*webPort))
	log.Printf("管理界面: http://%s%s/", addr, basePath)
	log.Printf("SOCKS5 仅监听 %s，端口在 %d-%d 之间随机分配", *socksBind, randPortMin, randPortMax)
	if err := http.ListenAndServe(addr, StripBasePath(basePath, auth.Wrap(mux))); err != nil {
		log.Fatal(err)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func apiNodes(m *Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodes, fetched := m.Nodes()
		if len(nodes) > 200 {
			nodes = nodes[:200]
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"nodes":     nodes,
			"fetched":   fetched,
			"error":     m.NodeError(),
			"max_slots": m.maxSlots,
		})
	}
}

// retryNodeRefresh handles machines whose network is not ready when the service starts.
func retryNodeRefresh(m *Manager) {
	delay := 30 * time.Second
	for {
		time.Sleep(delay)
		if n, err := m.RefreshNodes(); err != nil {
			log.Printf("自动拉取节点失败，%s 后重试: %v", delay, err)
			if delay < 10*time.Minute {
				delay *= 2
			}
			continue
		} else {
			log.Printf("自动更新了 %d 个节点", n)
		}
		delay = 30 * time.Minute
	}
}

func apiSwitch(m *Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slot, err := strconv.Atoi(r.URL.Query().Get("slot"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "slot 参数无效"})
			return
		}
		host := r.URL.Query().Get("host")
		nodes, _ := m.Nodes()
		for _, node := range nodes {
			if node.HostName != host {
				continue
			}
			t, err := m.Switch(slot, node)
			if err != nil {
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, t)
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "节点不存在，可能列表已过期"})
	}
}

func apiTunnels(m *Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, m.Tunnels())
	}
}

func apiStart(m *Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host := r.URL.Query().Get("host")
		if host == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 host 参数"})
			return
		}
		nodes, _ := m.Nodes()
		for _, n := range nodes {
			if n.HostName == host {
				t, err := m.Start(n)
				if err != nil {
					writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, t)
				return
			}
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "节点不存在，可能列表已过期"})
	}
}

func apiStop(m *Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slot, err := strconv.Atoi(r.URL.Query().Get("slot"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "slot 参数无效"})
			return
		}
		if err := m.Stop(slot); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"ok": "已停止"})
	}
}

func apiRefresh(m *Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n, err := m.RefreshNodes()
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"count": n})
	}
}
