# FanoutYUNDAN

在一台 VPS 上选择 VPN Gate 出口，并直接创建由 Mihomo 提供的独立 VLESS 节点。

切换日本、美国或其他出口时，Mihomo 的 UUID、配置、固定端口和节点链接都不会变化。

## 一键安装

使用 `root` 登录 VPS，执行：

```sh
curl -fsSL https://raw.githubusercontent.com/tanying-spec/FanoutYUNDAN/main/install.sh | sh
```

支持：

- Alpine Linux 3.20+（OpenRC）
- Debian 12+、Ubuntu 22.04+（systemd）
- AMD64、ARM64
- VPS 必须提供 `/dev/net/tun`

安装完成后，终端会直接显示管理页面和访问口令。以后输入：

```sh
fy info
```

即可再次查看。

## 使用方法

1. 打开安装器输出的管理页面并输入访问口令。
2. 点击“添加节点”，选择一个国家或具体节点。
3. 左侧出口连通后勾选它，点击“从选中出口创建”。
4. 在右侧复制 Mihomo 节点链接。
5. 以后在节点列表点击“切换”，即可更换国家，原链接无需修改。

同机安装了 [Mihomo-lite-argo](https://github.com/tanying-spec/Mihomo-lite-argo) 时，页面会自动启用 Mihomo 入站功能。未安装 Mihomo 时，FanoutYUNDAN 仍可作为本机 SOCKS5 出口管理器使用。

## 常用命令

```sh
fy info       # 查看页面地址和访问口令
fy status     # 查看服务状态
fy restart    # 重启服务
fy log        # 查看实时日志
fy update     # 更新到最新版
fy uninstall  # 卸载提示
```

确认卸载：

```sh
FANOUT_YUNDAN_UNINSTALL_CONFIRM=DELETE fy uninstall
```

保留节点和访问口令后卸载：

```sh
FANOUT_YUNDAN_KEEP_DATA=1 FANOUT_YUNDAN_UNINSTALL_CONFIRM=DELETE fy uninstall
```

卸载不会删除 Mihomo 或 Cloudflare Tunnel。

## 自定义安装

默认管理页面监听 `0.0.0.0:8899`，SOCKS5 始终只监听 `127.0.0.1`。

修改管理端口：

```sh
curl -fsSL https://raw.githubusercontent.com/tanying-spec/FanoutYUNDAN/main/install.sh | FANOUT_YUNDAN_WEB_PORT=18899 sh
```

仅允许本机访问管理页面：

```sh
curl -fsSL https://raw.githubusercontent.com/tanying-spec/FanoutYUNDAN/main/install.sh | FANOUT_YUNDAN_WEB_BIND=127.0.0.1 sh
```

同时运行多个出口：

```sh
curl -fsSL https://raw.githubusercontent.com/tanying-spec/FanoutYUNDAN/main/install.sh | FANOUT_YUNDAN_MAX_SLOTS=3 sh
```

公开管理端口时，请同时使用系统防火墙限制来源 IP。页面口令通过 HTTP 传输，需要公网使用时建议套 Cloudflare Tunnel 或 HTTPS 反向代理。

## 关键特性

- 每个槽位永久使用固定 SOCKS5 端口。
- 原槽位内切换出口，不重建 Mihomo 节点。
- 自动清理遗留 OpenVPN 进程和网络命名空间。
- 节点失效时自动尝试同地区候选节点。
- 安装包提供 SHA-256 校验。
- Web 页面具有随机访问路径和独立登录口令。
- 仅清理本项目创建的服务、进程和数据。

## 构建

```sh
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=dev" -o fanout-yundan .
```

项目基于 [byJoey/fanout](https://github.com/byJoey/fanout) 的思路和代码继续开发，保留原项目 MIT License。
