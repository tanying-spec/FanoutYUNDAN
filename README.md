# FanoutYUNDAN

FanoutYUNDAN 把一台 VPS 上的多个 VPN Gate 出口接入现有 Mihomo 入站。你可以让原来的 VLESS WS 节点改走日本、美国或其他地区的出口，不需要安装 3x-ui。

它最适合这类自用场景：**Mihomo 已经有可用的 VLESS WS 节点，并通过 Cloudflare CDN（小黄云）对外提供 WS-TLS 连接。**

切换或自动更换 VPN Gate 节点时，UUID、域名、公网端口、Host、SNI 和 WS Path 保持不变，客户端不需要删除重建。分享链接 `#` 后面的节点名称可能随出口 IP 更新，这不会影响连接。

## 使用前准备

- 一台 AMD64 或 ARM64 VPS
- Alpine 3.20+、Debian 12+ 或 Ubuntu 22.04+
- `root` 权限
- 可用的 `/dev/net/tun`
- 已安装并能正常启动的 Mihomo
- Mihomo 配置中至少有一个 VLESS WS listener

FanoutYUNDAN 只转发 TCP。VPN Gate 是志愿者网络，出口可能离线、满员或速度波动，不适合要求固定 IP 或稳定带宽的业务。

## 一键安装

使用 `root` 登录 VPS，直接执行，不需要 `sudo`：

```sh
curl -fsSL https://raw.githubusercontent.com/tanying-spec/FanoutYUNDAN/main/install.sh | sh
```

安装器只有在管理接口、已保存出口和 Mihomo 同步都就绪后才会报告成功。完成后会显示管理地址和随机口令，以后输入 `fy` 即可打开中文管理菜单。

## 接管现有 WS-CDN 节点

1. 先确认原来的 Mihomo VLESS WS-CDN 节点可以正常连接。
2. 打开安装器显示的管理页面并登录。
3. 添加一个国家或指定的 VPN Gate 出口，等待状态变为“正常”。
4. 在“未绑定出口的入站”中找到原来的 Mihomo 入站，选择刚添加的出口。
5. 复制页面生成的链接进行测试，并访问 IP 检测网站确认流量已经从所选 VPN Gate IP 出口。

也可以选择多个已连通出口，使用同一个 Mihomo listener 模板批量生成节点。每个出口拥有独立 SOCKS5 端口和绑定关系，互不影响。

当前版本只显示能够完整、安全重建客户端链接的 Mihomo VLESS TCP/WS listener。Reality、gRPC、HTTPUpgrade、XHTTP，以及带 TLS 私钥的 listener 不会作为模板显示，避免生成看似正常但实际不可用的链接。

## Cloudflare 和端口怎么填

使用 Cloudflare CDN WS-TLS 时：

- **公网地址**：客户端实际连接的 Cloudflare 域名，例如 `node.example.com`
- **公网端口**：通常是 `443`
- **Host / SNI**：通常与 Cloudflare 域名相同
- **WS Path**：必须与 Mihomo listener 中的 `ws-path` 一致
- **Mihomo 监听端口**：VPS 内部 listener 使用的端口

公网端口不必等于 Mihomo 监听端口。例如 NAT 把公网 `43954` 映射到内部 listener 端口时，分享链接应填写 `43954`；使用 Cloudflare CDN 时，客户端通常连接 `443`，再由 Cloudflare 回源到 Mihomo。

FanoutYUNDAN 不会替你创建 Cloudflare DNS、CDN 回源规则或端口映射。接管前原节点必须已经能够正常到达 Mihomo listener。

## 状态说明

- **正常（up）**：VPN、固定 SOCKS5 端口和 Mihomo 配置同步全部成功
- **节点同步失败（sync_failed）**：VPN 已连接，但 Mihomo 配置未成功同步；程序会继续重试
- **失败 / 已停止**：VPN 没有形成可用出口
- **未绑定出口**：该入站当前走 Mihomo 原来的直连出口

程序使用三个独立的公网 IP 检测源验证真实出口。检测服务自身暂时不可用时不会误切 VPN；确认出口失效后，会优先自动选择同地区节点。即使程序重启时原节点失效，也会把已有入站迁移到替代节点。

SOCKS5 端口在切换和自动恢复时保持不变，除非该端口已被其他程序占用。域名连接会在对应的网络隔离环境内完成解析，不会绕回母机出口。

## Mihomo 配置

程序会自动寻找：

```text
/etc/mihomo/config.yaml
/etc/mihomo/config.yml
/root/.config/mihomo/config.yaml
/root/.config/mihomo/config.yml
```

配置位于其他位置时，在服务环境中设置：

```sh
MIHOMO_CONFIG=/path/to/config.yaml
```

FanoutYUNDAN 只管理自己添加的 Mihomo 用户、SOCKS5 出站和 `IN-USER` 路由规则，不会重建原有 listener。每次修改前会执行 Mihomo 配置检查，备份文件为：

```text
config.yaml.fanout-yundan.bak
```

写入会保留原配置的权限和所有者。验证或重启失败时，程序会原子恢复原文件并再次启动 Mihomo。

运行状态保存在 `/var/lib/fanout-yundan/native.json`。旧版 `mihomo-inbounds.json` 和 `/etc/mihomo/fanout-bindings.db` 会在首次启动时自动迁移，不需要手工修改。

## 常用命令

```sh
fy info                 # 状态、版本、管理地址和口令
fy list                 # 已保存出口和固定 SOCKS5 端口
fy start|stop|restart   # 启停或重启服务
fy log                  # 查看实时日志
fy port 18899           # 修改管理端口
fy passwd 新口令       # 修改口令；留空则随机生成
fy path new-path        # 修改管理路径；留空则随机生成
fy autostart on|off     # 开关开机自启
fy update               # 更新到最新版
fy uninstall            # 显示卸载确认方式
```

SOCKS5 默认只监听 `127.0.0.1`，不会把无认证代理暴露到公网。管理操作只接受同源 POST 请求，登录具有频率限制；管理页面默认仍是 HTTP，公网长期使用建议增加 HTTPS 反向代理或 Cloudflare Tunnel。

确认卸载：

```sh
FANOUT_YUNDAN_UNINSTALL_CONFIRM=DELETE fy uninstall
```

保留出口、口令和绑定状态后卸载：

```sh
FANOUT_YUNDAN_KEEP_DATA=1 FANOUT_YUNDAN_UNINSTALL_CONFIRM=DELETE fy uninstall
```

正常卸载会先撤销 FanoutYUNDAN 管理的 Mihomo 配置，但不会删除 Mihomo、Cloudflare Tunnel 或其他节点。

## 常见问题

### 管理页面打不开

先执行 `fy info` 确认当前端口和随机路径，再检查 VPS 防火墙、服务商防火墙及 NAT 端口映射。修改管理端口后，应以 `fy info` 显示的新地址为准。

### 页面没有 Mihomo 入站模板

确认 Mihomo 配置中存在 VLESS TCP 或 WS listener，配置路径能被程序找到，并执行 `fy restart`。Reality、gRPC、HTTPUpgrade、XHTTP 和自带 TLS 私钥的 listener 会被主动隐藏；WS-CDN 节点应使用普通 VLESS WS listener，由 Cloudflare 在公网侧提供 TLS。

### 出口显示 `sync_failed`

这表示 VPN 本身已经连接，问题发生在 Mihomo 配置检查或重启阶段。执行 `fy log` 查看具体原因，先修复 Mihomo YAML、配置路径或服务启动问题。不要反复删除节点，程序会自动重试同步。

### 节点能连接，但检测到的是 VPS 直连 IP

确认入站没有出现在“未绑定出口”区域，出口状态必须为“正常”。同时核对客户端实际使用的 UUID、Host、SNI 和 WS Path 是否对应页面中的受管入站，而不是另一个未接管的 listener。

### 提示 TUN 不可用

执行 `ls -l /dev/net/tun`。文件不存在时，需要在 VPS 或容器管理面板开启 TUN/TAP；受限容器可能还需要服务商开放网络命名空间和相关权限。FanoutYUNDAN 无法用应用层设置绕过这些宿主机限制。

## 工作原理

```text
VLESS WS / Cloudflare CDN
          ↓
Mihomo listener → IN-USER 规则 → 固定 SOCKS5 端口
                                      ↓
                         network namespace → OpenVPN → VPN Gate
```

每个 VPN Gate 出口位于独立 Linux network namespace 中，OpenVPN 不会改变母机或其他出口的默认路由。OpenVPN 连接还会验证服务器证书用途，减少接入错误服务端的风险。

当前稳定版：[v2.1.3](https://github.com/tanying-spec/FanoutYUNDAN/releases/tag/v2.1.3) · 版本变化：[CHANGELOG.md](CHANGELOG.md)

项目延续 [byJoey/fanout](https://github.com/byJoey/fanout) 的 MIT 许可和 network namespace 设计。
