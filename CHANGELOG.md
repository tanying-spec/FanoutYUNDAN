# Changelog

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
