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
    apk add --no-cache ca-certificates curl iproute2 iptables openvpn util-linux-misc >/dev/null
  else
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    apt-get install -y -qq ca-certificates curl iproute2 iptables openvpn util-linux >/dev/null
  fi
  [ -c /dev/net/tun ] || die "当前 VPS 没有可用的 /dev/net/tun，请向服务商开启 TUN"
}

download_binary() {
  tmp="$(mktemp -d /tmp/fanout-yundan.XXXXXX)"
  trap 'rm -rf "$tmp"' EXIT HUP INT TERM
  asset="fanout-yundan-linux-${ARCH}"
  base="https://github.com/${REPO}/releases/latest/download"
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
case "${1:-info}" in
  info)
    ip="$(curl -fsS --max-time 8 https://api.ipify.org 2>/dev/null || printf '<服务器IP>')"
    path="$(tr -d '[:space:]' < "$DIR/basepath" 2>/dev/null || true)"
    printf '管理页面：http://%s:%s/%s/\n' "$ip" "${WEB_PORT:-8899}" "$path"
    printf '访问口令：'; cat "$DIR/password" 2>/dev/null || true
    command -v mh >/dev/null 2>&1 && printf 'Mihomo：已检测到，可在页面创建入站\n' || printf 'Mihomo：未安装，页面仅管理出口\n'
    ;;
  status|start|stop|restart) service_cmd "$1" ;;
  log)
    if command -v rc-service >/dev/null 2>&1; then tail -n 100 -f /var/log/fanout-yundan.log
    else journalctl -u fanout-yundan -n 100 -f; fi
    ;;
  update) curl -fsSL "https://raw.githubusercontent.com/tanying-spec/FanoutYUNDAN/main/install.sh" | sh ;;
  uninstall) curl -fsSL "https://raw.githubusercontent.com/tanying-spec/FanoutYUNDAN/main/install.sh" | sh -s -- uninstall ;;
  *) printf '用法：fy [info|status|start|stop|restart|log|update|uninstall]\n' >&2; exit 1 ;;
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
    [ -s "$WORK_DIR/password" ] && [ -s "$WORK_DIR/basepath" ] && return 0
    i=$((i+1)); sleep 1
  done
  die "服务未在 90 秒内就绪，请执行 fy log 查看原因"
}

uninstall() {
  need_root; detect_system; validate_config
  [ "${FANOUT_YUNDAN_UNINSTALL_CONFIRM:-}" = DELETE ] || die "确认卸载请执行：FANOUT_YUNDAN_UNINSTALL_CONFIRM=DELETE fy uninstall"
  if [ "$SYSTEM" = openrc ]; then
    rc-service fanout-yundan stop >/dev/null 2>&1 || true
    rc-update del fanout-yundan default >/dev/null 2>&1 || true
    rm -f /etc/init.d/fanout-yundan /var/log/fanout-yundan.log
  else
    systemctl disable --now fanout-yundan >/dev/null 2>&1 || true
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
backup=""
if [ -x "$BIN" ]; then backup="${BIN}.previous"; cp "$BIN" "$backup"; fi
install -m 0755 "$tmp/fanout-yundan-linux-${ARCH}" "$BIN"
write_cli
if ! write_service; then
  [ -n "$backup" ] && cp "$backup" "$BIN"
  die "服务安装失败，已恢复旧程序"
fi
wait_ready
sysctl -qw net.ipv4.ip_forward=1
say
say "FanoutYUNDAN 安装完成。"
fy info
say "以后输入 fy 查看、更新或卸载。"
