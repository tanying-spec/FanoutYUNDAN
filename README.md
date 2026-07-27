# FanoutYUNDAN

FanoutYUNDAN 在一台 VPS 上运行多个独立的 VPN Gate 出口，并直接把选中的出口接入 Mihomo 入站。切换日本、美国或其他出口时，Mihomo 节点的 UUID、端口和分享链接保持不变。

它不需要 3x-ui，也不依赖 `mh fanout`。Mihomo-lite-argo 只是可选的 Mihomo 安装来源，不是运行依赖。

## 一键安装

用 `root` 登录 VPS 后执行：

```sh
curl -fsSL https://raw.githubusercontent.com/tanying-spec/FanoutYUNDAN/main/install.sh | sh
```

支持 Alpine 3.20+、Debian 12+、Ubuntu 22.04+，以及 AMD64、ARM64。VPS 必须提供 `/dev/net/tun`。

安装结束会直接显示管理地址和访问口令。以后输入：

```sh
fy
```

即可打开中文管理菜单。

## 创建节点

1. 打开安装器显示的管理页面并登录。
2. 点击“添加节点”，选择国家或具体 VPN Gate 节点。
3. 出口连通后，在左侧勾选一个或多个出口。
4. 点击“从选中出口创建”，选择 Mihomo 入口模板和入口模式。
5. 在右侧复制节点链接，导入 Clash Meta、Mihomo Party 或其他 VLESS 客户端。

右侧下拉框可以随时切换出口。FanoutYUNDAN 只修改自己创建的用户、SOCKS5 出站和 `IN-USER` 规则，不会重建原有 listener。

## 入口模式

| 模式 | 适用情况 | 需要填写 |
| --- | --- | --- |
| 直连 WS | 客户端直接连接 VPS 或 NAT 映射端口 | 公网 IP/域名、公网端口、WS Path |
| Cloudflare CDN WS-TLS | 域名开启 Cloudflare 代理 | CDN 域名、443、Host、SNI、WS Path |
| Cloudflare Argo WS-TLS | 已有 Cloudflare Tunnel 域名 | Tunnel 域名、443、Host、SNI、WS Path |
| VLESS Reality | Mihomo 已存在 Reality listener | 公网地址、端口、SNI、公钥、Short ID |

公网端口可以与 Mihomo 内部监听端口不同，适合 NAT 端口映射。CDN 和 Argo 模式必须填写浏览器实际访问的域名，不能填写 VPS 内网地址。

## Mihomo 配置

程序自动寻找以下配置：

```text
/etc/mihomo/config.yaml
/etc/mihomo/config.yml
/usr/local/etc/mihomo/config.yaml
/root/.config/mihomo/config.yaml
```

其他位置可在服务环境中设置：

```sh
FANOUT_YUNDAN_MIHOMO_CONFIG=/path/to/config.yaml
```

每次写入前都会用 Mihomo 自检临时配置，并备份为 `config.yaml.fanout-yundan.previous`。验证或重启失败会恢复原文件。程序每分钟对账一次，受管配置被其他工具覆盖后会自动补回。

旧版 `/etc/mihomo/fanout-bindings.db` 会在首次读取时自动迁移到：

```text
/var/lib/fanout-yundan/mihomo-inbounds.json
```

## 常用命令

```sh
fy info                 # 状态、版本、管理地址和口令
fy list                 # 已保存的出口和 SOCKS5 端口
fy start|stop|restart   # 管理服务
fy log                  # 实时日志
fy port 18899           # 修改管理端口
fy passwd 新口令       # 修改口令；留空随机生成
fy path new-path        # 修改路径；留空随机生成
fy autostart on|off     # 开关开机自启
fy update               # 更新
fy uninstall            # 显示卸载确认方式
```

SOCKS5 默认只监听 `127.0.0.1`，供同机 Mihomo 使用，不会把无认证代理暴露到公网。

确认卸载：

```sh
FANOUT_YUNDAN_UNINSTALL_CONFIRM=DELETE fy uninstall
```

保留出口、口令和绑定状态后卸载：

```sh
FANOUT_YUNDAN_KEEP_DATA=1 FANOUT_YUNDAN_UNINSTALL_CONFIRM=DELETE fy uninstall
```

卸载会先撤销 FanoutYUNDAN 管理的 Mihomo 用户、代理和规则，但不会删除 Mihomo、Cloudflare Tunnel 或其他节点。

## 工作原理

每个 VPN Gate 节点运行在独立 Linux network namespace 中。OpenVPN 只改变自己的 namespace 路由，母机和其他出口不受影响。母机上的固定 SOCKS5 端口通过 `setns` 从对应 namespace 建立 TCP 连接。

```text
Mihomo 入站 -> IN-USER 规则 -> 固定 SOCKS5 端口 -> netns -> OpenVPN -> VPN Gate 出口
```

健康检查会验证实际出口 IP。连续失败后自动选择同地区候选节点重连，槽位和 SOCKS5 端口保持不变，因此 Mihomo 链接无需删除重建。

## 限制

- 只转发 TCP；SOCKS5 域名在母机解析。
- VPN Gate 是志愿者网络，节点可能离线、满员或速度变化。
- 管理页面使用随机路径和口令，但默认是 HTTP；公网使用建议配置 HTTPS 反向代理或 Cloudflare Tunnel。

版本变化见 [CHANGELOG.md](CHANGELOG.md)。项目延续 [byJoey/fanout](https://github.com/byJoey/fanout) 的 MIT 许可和 network namespace 设计。
