#!/bin/sh
set -eu

REPO="${FANOUT_YUNDAN_REPO:-tanying-spec/FanoutYUNDAN}"
BIN=/usr/local/bin/fanout-yundan
CLI=/usr/local/bin/fy
WORK_DIR="${FANOUT_YUNDAN_DIR:-/var/lib/fanout-yundan}"
[ ! -r "$WORK_DIR/install.env" ] || . "$WORK_DIR/install.env"
WEB_PORT="${FANOUT_YUNDAN_WEB_PORT:-${WEB_PORT:-8899}}"
WEB_BIND="${FANOUT_YUNDAN_WEB_BIND:-${WEB_BIND:-0.0.0.0}}"
MAX_SLOTS="${FANOUT_YUNDAN_MAX_SLOTS:-${MAX_SLOTS:-1}}"
ACTION="${1:-install}"
LEGACY_UPGRADE=0
LEGACY_MIHOMO_CONFIG=""
LEGACY_MIHOMO_BACKUP="/tmp/fanout-yundan-mihomo-upgrade.yaml"

say() { printf '%s\n' "$*"; }
die() { printf '错误：%s\n' "$*" >&2; exit 1; }
need_root() { [ "$(id -u)" = 0 ] || die "请切换到 root 后重新执行安装命令"; }

detect_system() {
  if command -v apk >/dev/null 2>&1; then SYSTEM=openrc
  elif command -v apt-get >/dev/null 2>&1 && command -v systemctl >/dev/null 2>&1; then SYSTEM=systemd
  else die "仅支持 Alpine/OpenRC 和 Debian/Ubuntu/systemd"; fi
  case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) die "暂不支持的 CPU 架构：$(uname -m)" ;;
  esac
}

validate_config() {
  case "$WORK_DIR" in
    /var/lib/fanout-yundan|/var/lib/fanout-yundan-*) ;;
    *) die "数据目录必须是 /var/lib/fanout-yundan 或 /var/lib/fanout-yundan-*" ;;
  esac
  case "$WEB_PORT" in *[!0-9]*|'') die "Web 端口必须是数字" ;; esac
  [ "$WEB_PORT" -ge 1 ] && [ "$WEB_PORT" -le 65535 ] || die "Web 端口必须在 1-65535 之间"
  case "$MAX_SLOTS" in *[!0-9]*|'') die "出口数量必须是数字" ;; esac
  [ "$MAX_SLOTS" -ge 1 ] && [ "$MAX_SLOTS" -le 20 ] || die "出口数量必须在 1-20 之间"
  case "$WEB_BIND" in 0.0.0.0|127.0.0.1) ;; *) die "Web 监听地址仅支持 0.0.0.0 或 127.0.0.1" ;; esac
}

install_deps() {
  if [ "$SYSTEM" = openrc ]; then
    apk add --no-cache ca-certificates curl iproute2 iptables jq openvpn util-linux-misc >/dev/null
  else
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    apt-get install -y -qq ca-certificates curl iproute2 iptables jq openvpn util-linux >/dev/null
  fi
  [ -c /dev/net/tun ] || die "当前 VPS 没有可用的 /dev/net/tun，请向服务商开启 TUN"
}

download_binary() {
  tmp="$(mktemp -d /tmp/fanout-yundan.XXXXXX)"
  trap 'rm -rf "$tmp"' EXIT HUP INT TERM
  asset="fanout-yundan-linux-${ARCH}"
  base="${FANOUT_YUNDAN_DOWNLOAD_BASE:-https://github.com/${REPO}/releases/latest/download}"
  say "正在下载 FanoutYUNDAN (${ARCH})..."
  curl -fsSL --retry 3 --connect-timeout 15 "${base}/${asset}" -o "$tmp/$asset" || die "下载程序失败"
  curl -fsSL --retry 3 --connect-timeout 15 "${base}/checksums.txt" -o "$tmp/checksums.txt" || die "下载校验文件失败"
  expected="$(awk -v f="$asset" '$2==f{print $1}' "$tmp/checksums.txt")"
  [ -n "$expected" ] || die "发布版本缺少 ${asset} 的校验值"
  actual="$(sha256sum "$tmp/$asset" | awk '{print $1}')"
  [ "$expected" = "$actual" ] || die "SHA-256 校验失败"
  chmod 0755 "$tmp/$asset"
}

write_cli() {
  cat > "$CLI" <<'EOF'
#!/bin/sh
set -eu
DIR="${FANOUT_YUNDAN_DIR:-/var/lib/fanout-yundan}"
[ ! -r "$DIR/install.env" ] || . "$DIR/install.env"
service_cmd() {
  if command -v rc-service >/dev/null 2>&1; then rc-service fanout-yundan "$1"
  else systemctl "$1" fanout-yundan; fi
}
service_running() {
  if command -v rc-service >/dev/null 2>&1; then rc-service fanout-yundan status >/dev/null 2>&1
  else systemctl is-active --quiet fanout-yundan; fi
}
autostart_enabled() {
  if command -v rc-update >/dev/null 2>&1; then rc-update show default 2>/dev/null | grep -q fanout-yundan
  else systemctl is-enabled --quiet fanout-yundan; fi
}
random_hex() { od -An -N "$1" -tx1 /dev/urandom | tr -d ' \n'; }
if [ "$#" -eq 0 ]; then
  printf '%s\n' \
    'FanoutYUNDAN 管理菜单' \
    '1. 查看状态和管理地址' \
    '2. 查看出口列表' \
    '3. 启动服务' \
    '4. 停止服务' \
    '5. 重启服务' \
    '6. 查看实时日志' \
    '7. 修改管理端口' \
    '8. 修改访问口令' \
    '9. 修改访问路径' \
    '10. 切换开机自启' \
    '11. 更新' \
    '12. 卸载'
  printf '请选择 [1-12]：'
  read -r choice
  case "$choice" in
    1) set -- info ;; 2) set -- list ;; 3) set -- start ;; 4) set -- stop ;;
    5) set -- restart ;; 6) set -- log ;;
    7) printf '新端口：'; read -r value; set -- port "$value" ;;
    8) printf '新口令（至少 8 个字符）：'; read -r value; set -- passwd "$value" ;;
    9) printf '新路径（6-48 个字母、数字、_、-）：'; read -r value; set -- path "$value" ;;
    10) set -- autostart toggle ;; 11) set -- update ;; 12) set -- uninstall ;;
    *) printf '无效选择。\n' >&2; exit 1 ;;
  esac
fi
case "$1" in
  info)
    service_running && printf '服务状态：运行中\n' || printf '服务状态：已停止\n'
    printf '程序版本：'; /usr/local/bin/fanout-yundan -version 2>/dev/null || printf '未知\n'
    autostart_enabled && printf '开机自启：开启\n' || printf '开机自启：关闭\n'
    count="$(jq -r '.tunnels | length' "$DIR/state.json" 2>/dev/null || printf 0)"
    printf '已保存出口：%s 条\n' "$count"
    ip="$(curl -fsS --max-time 8 https://api.ipify.org 2>/dev/null || printf '<服务器IP>')"
    path="$(tr -d '[:space:]' < "$DIR/basepath" 2>/dev/null || true)"
    printf '管理页面：http://%s:%s/%s/\n' "$ip" "${WEB_PORT:-8899}" "$path"
    printf '访问口令：'; cat "$DIR/password" 2>/dev/null || true
    if command -v mihomo >/dev/null 2>&1 || [ -x /usr/local/bin/mihomo ] || [ -f /etc/mihomo/config.yaml ] || [ -f /etc/mihomo/config.yml ]; then
      printf 'Mihomo：已检测到，可在页面创建入站\n'
    else
      printf 'Mihomo：未安装，页面仅管理出口\n'
    fi
    ;;
  status|start|stop|restart) service_cmd "$1" ;;
  list)
    if [ -s "$DIR/state.json" ]; then
      jq -r '.tunnels[]? | "槽位 \(.slot)  SOCKS=\(.port)  \(.country_code)  \(.hostname)"' "$DIR/state.json"
    else printf '当前没有已保存的出口。\n'; fi
    ;;
  port)
    port="${2:-}"
    case "$port" in ''|*[!0-9]*) printf '端口必须是数字。\n' >&2; exit 1 ;; esac
    [ "$port" -ge 1 ] && [ "$port" -le 65535 ] || { printf '端口必须在 1-65535 之间。\n' >&2; exit 1; }
    if [ "$port" != "${WEB_PORT:-8899}" ] && ss -lnt 2>/dev/null | awk '{print $4}' | grep -Eq "(^|:)${port}$"; then
      printf '端口 %s 已被占用。\n' "$port" >&2; exit 1
    fi
    curl -fsSL "https://raw.githubusercontent.com/tanying-spec/FanoutYUNDAN/main/install.sh" | env FANOUT_YUNDAN_DIR="$DIR" FANOUT_YUNDAN_WEB_PORT="$port" sh
    ;;
  passwd)
    value="${2:-}"
    [ -n "$value" ] || value="$(random_hex 9)"
    [ "${#value}" -ge 8 ] || { printf '口令至少需要 8 个字符。\n' >&2; exit 1; }
    umask 077; printf '%s\n' "$value" > "$DIR/password"; service_cmd restart
    ;;
  path)
    value="${2:-}"
    [ -n "$value" ] || value="$(random_hex 8)"
    case "$value" in ''|*[!A-Za-z0-9_-]*) printf '路径只能包含字母、数字、下划线和短横线。\n' >&2; exit 1 ;; esac
    [ "${#value}" -ge 6 ] && [ "${#value}" -le 48 ] || { printf '路径长度必须为 6-48。\n' >&2; exit 1; }
    umask 077; printf '%s\n' "$value" > "$DIR/basepath"; service_cmd restart
    ;;
  autostart)
    value="${2:-status}"
    if [ "$value" = toggle ]; then
      if autostart_enabled; then value=off; else value=on; fi
    fi
    if command -v rc-update >/dev/null 2>&1; then
      case "$value" in on) rc-update add fanout-yundan default ;; off) rc-update del fanout-yundan default ;; status) rc-update show default | grep -q fanout-yundan && printf '开机自启：开启\n' || printf '开机自启：关闭\n' ;; *) exit 1 ;; esac
    else
      case "$value" in on) systemctl enable fanout-yundan ;; off) systemctl disable fanout-yundan ;; status) systemctl is-enabled fanout-yundan ;; *) exit 1 ;; esac
    fi
    ;;
  log)
    if command -v rc-service >/dev/null 2>&1; then tail -n 100 -f /var/log/fanout-yundan.log
    else journalctl -u fanout-yundan -n 100 -f; fi
    ;;
  update) curl -fsSL "https://raw.githubusercontent.com/tanying-spec/FanoutYUNDAN/main/install.sh" | env FANOUT_YUNDAN_DIR="$DIR" sh ;;
  uninstall) curl -fsSL "https://raw.githubusercontent.com/tanying-spec/FanoutYUNDAN/main/install.sh" | sh -s -- uninstall ;;
  *) printf '用法：fy [info|list|status|start|stop|restart|log|port|passwd|path|autostart|update|uninstall]\n' >&2; exit 1 ;;
esac
EOF
  chmod 0755 "$CLI"
}

write_service() {
  mkdir -p "$WORK_DIR"
  chmod 0700 "$WORK_DIR"
  cat > "$WORK_DIR/install.env" <<EOF
WEB_PORT=$WEB_PORT
WEB_BIND=$WEB_BIND
MAX_SLOTS=$MAX_SLOTS
EOF
  chmod 0600 "$WORK_DIR/install.env"
  if [ "$SYSTEM" = openrc ]; then
    cat > /etc/init.d/fanout-yundan <<EOF
#!/sbin/openrc-run
description="FanoutYUNDAN VPN exit gateway"
supervisor="supervise-daemon"
command="$BIN"
command_args="-web $WEB_PORT -web-bind $WEB_BIND -socks-bind 127.0.0.1 -max $MAX_SLOTS -dir $WORK_DIR"
respawn_delay=5
respawn_max=0
output_log="/var/log/fanout-yundan.log"
error_log="/var/log/fanout-yundan.log"
depend() { need net; after firewall; }
start_pre() { checkpath --directory --mode 0700 "$WORK_DIR"; checkpath --file --mode 0600 /var/log/fanout-yundan.log; }
EOF
    chmod 0755 /etc/init.d/fanout-yundan
    rc-update add fanout-yundan default >/dev/null
    rc-service fanout-yundan restart >/dev/null
  else
    cat > /etc/systemd/system/fanout-yundan.service <<EOF
[Unit]
Description=FanoutYUNDAN VPN exit gateway
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
ExecStart=$BIN -web $WEB_PORT -web-bind $WEB_BIND -socks-bind 127.0.0.1 -max $MAX_SLOTS -dir $WORK_DIR
Restart=on-failure
RestartSec=5
TimeoutStopSec=30
KillMode=mixed
[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    systemctl enable --now fanout-yundan >/dev/null
  fi
}

wait_ready() {
  i=0
  while [ "$i" -lt 90 ]; do
    running=0
    if [ "$SYSTEM" = openrc ]; then rc-service fanout-yundan status >/dev/null 2>&1 && running=1
    else systemctl is-active --quiet fanout-yundan && running=1; fi
    if [ "$running" = 1 ] && [ -s "$WORK_DIR/password" ] && [ -s "$WORK_DIR/basepath" ]; then
      base="$(tr -d '[:space:]' < "$WORK_DIR/basepath")"
      password="$(tr -d '\r\n' < "$WORK_DIR/password")"
      cookie="$tmp/ready.cookie"
      if curl -fsS --max-time 3 -c "$cookie" --data-urlencode "password=$password" "http://127.0.0.1:${WEB_PORT}/${base}/login" >/dev/null 2>&1; then
        exits="$(curl -fsS --max-time 3 -b "$cookie" "http://127.0.0.1:${WEB_PORT}/${base}/api/exits" 2>/dev/null || true)"
        saved_count="$(jq -r '.tunnels | length' "$WORK_DIR/state.json" 2>/dev/null || printf 0)"
        ready_count="$(printf '%s' "$exits" | jq -r '[.exits[]? | select(.status == "up")] | length' 2>/dev/null || printf 0)"
        panel_error="$(printf '%s' "$exits" | jq -r '.panel // ""' 2>/dev/null || printf invalid)"
        if [ -n "$exits" ] && [ "$panel_error" = "" ] && [ "$ready_count" -ge "$saved_count" ]; then
          return 0
        fi
      fi
    fi
    i=$((i+1)); sleep 1
  done
  return 1
}

rollback_current_upgrade() {
  [ -n "${backup:-}" ] && [ -f "$backup" ] || return 0
  cp "$backup" "$BIN"
  chmod 0755 "$BIN"
  if [ -f "$tmp/install.env.previous" ]; then cp "$tmp/install.env.previous" "$WORK_DIR/install.env"; fi
  if [ "$SYSTEM" = openrc ]; then
    [ ! -f "$tmp/service.previous" ] || cp "$tmp/service.previous" /etc/init.d/fanout-yundan
    rc-service fanout-yundan restart >/dev/null 2>&1 || true
  else
    [ ! -f "$tmp/service.previous" ] || cp "$tmp/service.previous" /etc/systemd/system/fanout-yundan.service
    systemctl daemon-reload
    systemctl restart fanout-yundan >/dev/null 2>&1 || true
  fi
}

rollback_install() {
  if [ -n "${backup:-}" ] && [ -f "$backup" ]; then
    rollback_current_upgrade
    return
  fi
  if [ "$SYSTEM" = openrc ]; then
    rc-service fanout-yundan stop >/dev/null 2>&1 || true
    rc-update del fanout-yundan default >/dev/null 2>&1 || true
    rm -f /etc/init.d/fanout-yundan
  else
    systemctl stop fanout-yundan >/dev/null 2>&1 || true
    systemctl disable fanout-yundan >/dev/null 2>&1 || true
    rm -f /etc/systemd/system/fanout-yundan.service
    systemctl daemon-reload
  fi
  rm -f "$BIN" "$CLI"
}

prepare_legacy_upgrade() {
  [ "$WORK_DIR" != /var/lib/fanout ] || return 0
  [ -d /var/lib/fanout ] || return 0
  if [ "$SYSTEM" = openrc ]; then
    [ -e /etc/init.d/fanout ] || return 0
    rc-service fanout stop >/dev/null 2>&1 || true
  else
    [ -e /etc/systemd/system/fanout.service ] || return 0
    systemctl stop fanout >/dev/null 2>&1 || true
  fi
  mkdir -p "$WORK_DIR"
  if [ ! -s "$WORK_DIR/state.json" ]; then
    cp -a /var/lib/fanout/. "$WORK_DIR/"
  fi
  chmod 0700 "$WORK_DIR"
  for candidate in /etc/mihomo/config.yaml /etc/mihomo/config.yml /usr/local/etc/mihomo/config.yaml /root/.config/mihomo/config.yaml; do
    if [ -f "$candidate" ]; then
      LEGACY_MIHOMO_CONFIG="$candidate"
      cp "$candidate" "$LEGACY_MIHOMO_BACKUP"
      chmod 0600 "$LEGACY_MIHOMO_BACKUP"
      break
    fi
  done
  LEGACY_UPGRADE=1
  say "已迁移旧版 fanout 状态，固定 SOCKS 端口和管理口令将保持不变。"
}

rollback_legacy_upgrade() {
  [ "$LEGACY_UPGRADE" = 1 ] || return 0
  if [ "$SYSTEM" = openrc ]; then
    rc-service fanout-yundan stop >/dev/null 2>&1 || true
    if [ -n "$LEGACY_MIHOMO_CONFIG" ] && [ -f "$LEGACY_MIHOMO_BACKUP" ]; then
      cp "$LEGACY_MIHOMO_BACKUP" "$LEGACY_MIHOMO_CONFIG"
      rc-service mihomo restart >/dev/null 2>&1 || true
    fi
    rc-service fanout start >/dev/null 2>&1 || true
  else
    systemctl stop fanout-yundan >/dev/null 2>&1 || true
    if [ -n "$LEGACY_MIHOMO_CONFIG" ] && [ -f "$LEGACY_MIHOMO_BACKUP" ]; then
      cp "$LEGACY_MIHOMO_BACKUP" "$LEGACY_MIHOMO_CONFIG"
      systemctl restart mihomo >/dev/null 2>&1 || true
    fi
    systemctl start fanout >/dev/null 2>&1 || true
  fi
  rm -f "$LEGACY_MIHOMO_BACKUP"
}

finish_legacy_upgrade() {
  [ "$LEGACY_UPGRADE" = 1 ] || return 0
  if [ "$SYSTEM" = openrc ]; then
    rc-update del fanout default >/dev/null 2>&1 || true
    rm -f /etc/init.d/fanout
  else
    systemctl disable fanout >/dev/null 2>&1 || true
    rm -f /etc/systemd/system/fanout.service
    systemctl daemon-reload
  fi
  rm -f /usr/local/bin/fanout /usr/local/bin/f
  rm -rf /var/lib/fanout
  rm -f "$LEGACY_MIHOMO_BACKUP"
}

uninstall() {
  need_root; detect_system; validate_config
  [ "${FANOUT_YUNDAN_UNINSTALL_CONFIRM:-}" = DELETE ] || die "确认卸载请执行：FANOUT_YUNDAN_UNINSTALL_CONFIRM=DELETE fy uninstall"
  if [ "$SYSTEM" = openrc ]; then
    rc-service fanout-yundan stop >/dev/null 2>&1 || true
  else
    systemctl stop fanout-yundan >/dev/null 2>&1 || true
  fi
  if [ "${FANOUT_YUNDAN_KEEP_DATA:-0}" != 1 ]; then
    if ! "$BIN" -dir "$WORK_DIR" -cleanup-mihomo >/dev/null 2>&1; then
      if [ "$SYSTEM" = openrc ]; then rc-service fanout-yundan start >/dev/null 2>&1 || true
      else systemctl start fanout-yundan >/dev/null 2>&1 || true; fi
      die "卸载停止：清理 Mihomo 受管入站失败，服务已恢复"
    fi
  fi
  if [ "$SYSTEM" = openrc ]; then
    rc-update del fanout-yundan default >/dev/null 2>&1 || true
    rm -f /etc/init.d/fanout-yundan /var/log/fanout-yundan.log
  else
    systemctl disable fanout-yundan >/dev/null 2>&1 || true
    rm -f /etc/systemd/system/fanout-yundan.service
    systemctl daemon-reload
  fi
  rm -f "$BIN" "$CLI"
  if [ "${FANOUT_YUNDAN_KEEP_DATA:-0}" != 1 ]; then rm -rf "$WORK_DIR"; fi
  say "FanoutYUNDAN 已卸载。Mihomo 配置未被删除。"
}

[ "$ACTION" = uninstall ] && { uninstall; exit 0; }
need_root
detect_system
validate_config
install_deps
download_binary
prepare_legacy_upgrade
backup=""
if [ -x "$BIN" ]; then
  backup="${BIN}.previous"
  cp "$BIN" "$backup"
  [ ! -f "$WORK_DIR/install.env" ] || cp "$WORK_DIR/install.env" "$tmp/install.env.previous"
  if [ "$SYSTEM" = openrc ]; then
    [ ! -f /etc/init.d/fanout-yundan ] || cp /etc/init.d/fanout-yundan "$tmp/service.previous"
  else
    [ ! -f /etc/systemd/system/fanout-yundan.service ] || cp /etc/systemd/system/fanout-yundan.service "$tmp/service.previous"
  fi
fi
install -m 0755 "$tmp/fanout-yundan-linux-${ARCH}" "$BIN"
write_cli
if ! write_service; then
  rollback_install
  rollback_legacy_upgrade
  die "服务安装失败，已恢复旧程序"
fi
if ! wait_ready; then
	rollback_install
	rollback_legacy_upgrade
  die "新服务未能就绪，旧版 fanout 已恢复"
fi
finish_legacy_upgrade
sysctl -qw net.ipv4.ip_forward=1
say
say "FanoutYUNDAN 安装完成。"
fy info
say "以后输入 fy 查看、更新或卸载。"
