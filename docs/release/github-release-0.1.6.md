Pax Connect 0.1.6 Beta，适用于 macOS 26 及以上、Apple Silicon。

- 导入 Clash/Mihomo YAML、sing-box JSON 中受支持的线路，并通过原订阅入口刷新。
- 支持 TLS 或 REALITY 下的 VLESS 单服务 gRPC，可在详细参数中编辑服务名。AnyTLS 支持导入 UDP 参数。
- 加快运行日志切换应用和分页。流量监控与网络概览原位更新变化的单元格。
- 修复 Krill `-ultrafast` 模型费率。

退出旧版后将新 App 拖到「应用程序」完成更新。线路、规则和历史数据会保留，已有 MCP 会话需要重新连接。从 0.1.2 或更早版本升级仍涉及日志转换，升级后的库不支持降级读取。

安装包采用临时签名，尚未通过 Apple 公证。首次打开若被 macOS 拦截，请参照附件中的安装说明。内置 sing-box v1.14.3，沿用既有 GeoLite 来源。

附件提供 DMG、SHA256 校验文件、构建 manifest、离线安装说明和完整版本说明。

[对应源码](https://github.com/Moore-developers/pax-connect-app/tree/v0.1.6) · [反馈问题](https://github.com/Moore-developers/pax-connect-app/issues)
