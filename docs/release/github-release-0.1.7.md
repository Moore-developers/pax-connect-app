Pax Connect 0.1.7 Beta，适用于 macOS 26 及以上、Apple Silicon。

- 设置新增简体中文和 English 即时切换，支持跟随系统、导入和导出语言包。部分旧记录保留原文。
- 新增默认关闭的 Claude 分流包。启用时也会匹配 Google 登录、Stripe 和 Persona 等共享域名。
- Pax 接管期间，指定线路未载入或确认故障时，拒绝明确命中的新连接。确认故障后会关闭已归属该线路的连接，恢复后允许重连。可确认归属的 DNS 查询遵守同一指定线路。Pax 崩溃或 TUN 接管消失时，流量仍可能直连。
- 移除日志及流量数据库的容量上限，继续按保留时间清理。诊断详情可在 App 内查看，报告预览、复制和导出入口已移除。

退出旧版后将新 App 拖到「应用程序」完成更新。既有线路、规则和历史数据保存在本机。已有 MCP 会话需要重新连接。

安装包采用临时签名，尚未通过 Apple 公证。首次打开若被 macOS 拦截，请参照附件中的安装说明。内置 sing-box v1.14.3，沿用既有 GeoLite 来源。

附件提供 DMG、SHA256 校验文件、构建 manifest、离线安装说明和完整版本说明。

[对应源码](https://github.com/Moore-developers/pax-connect-app/tree/v0.1.7) · [反馈问题](https://github.com/Moore-developers/pax-connect-app/issues)
