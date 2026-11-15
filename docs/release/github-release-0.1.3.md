Pax Connect 0.1.3 Beta，适用于 macOS 26+ / Apple Silicon。

- 日志使用列式存储和有界查询，降低历史查询内存占用。
- 改进 DNS、WebRTC、IPv6、定位及多源 IP 诊断；无法确认的结果明确保留未知。
- 新安装默认开启 WebRTC 公网出口限制，既有设置保持。
- 简化设置页，统一设置与关于页的行距、留白和对齐。

升级会转换旧日志库，保留配置、历史和 AI 用量。日志库连续 24 小时跨午夜运行的观察尚未完成；不承诺降级兼容。

当前为临时签名、未公证 Beta。首次打开可能被 macOS 拦截，请按附件安装说明操作，不必关闭 Gatekeeper。仍保留既有 GeoLite 来源及分发条件待办，详见对应源码的 docs/release/geolite-redistribution.md。

附件包含 DMG、SHA256SUMS、manifest、安装说明与完整版本说明。源码为本发布对应的 v0.1.3 标签；内置内核为 sing-box v1.14.3。
