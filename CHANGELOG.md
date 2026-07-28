# Changelog

## v2.1.1 - 2026-07-28

- 修复旧 `mh fanout` 的 `/etc/mihomo/fanout-bindings.db` 未迁移，导致安装后节点列表为空。
- 迁移时保留旧节点名称、UUID、Mihomo listener、WS Path 和已有出口绑定。
- 修复合并 VLESS 用户时误改 Hysteria2、AnyTLS 等非 VLESS listener 的 `users` 配置。
- 首次接管后自动把旧 `fanout-*` 出站和规则替换为 FanoutYUNDAN 的 `fy-out-*` 管理项。

## v2.1.0 - 2026-07-27

- 基于最新版原版 Fanout 恢复完整界面、VPN Gate 切换与健康检查工作流。
- 使用已有 Mihomo listener 创建独立节点，切换出口时保持 UUID、端口和分享链接不变。
- 修复受限 LXC 中 network namespace、DNS 探测、失败进程清理和切换期母机 IP 泄漏。
- 默认只在 `127.0.0.1` 监听无认证 SOCKS5，并提供独立的 Web/SOCKS 监听参数。
- 恢复 POSIX `sh` 一键安装、`fy` 管理、校验、迁移、回滚和安全卸载流程。

## v2.0.1

- 从旧版 `fanout` 自动迁移服务、数据、固定 SOCKS5 端口、管理路径和口令。
- 新服务启动失败时自动恢复旧服务，避免升级后节点中断。

## v2.0.0

- 入站管理从 `mh.sh` 完全迁移到 FanoutYUNDAN。
- 使用原生 YAML 配置层管理 Mihomo 用户、SOCKS5 出站和 `IN-USER` 规则。
- 支持直连 WS、Cloudflare CDN WS-TLS、Cloudflare Argo WS-TLS 和 VLESS Reality。
- 支持 NAT 公网端口与内部监听端口分离。
- 支持旧 `fanout-bindings.db` 自动迁移和定时配置对账。
- 配置写入增加 Mihomo 自检、备份、失败回滚和卸载清理。
- 移除全部 3x-ui 和 `mh fanout` 代码。
- 补齐中文管理菜单、状态、出口列表、端口、口令、路径、开机自启、更新和卸载功能。
