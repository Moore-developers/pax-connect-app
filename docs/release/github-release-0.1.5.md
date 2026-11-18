Pax Connect 0.1.5 Beta，适用于 macOS 26 及以上、Apple Silicon。

- 分流规则可按进程的完整可执行文件路径精确匹配，支持代理、直连或拦截。
- 应用和进程候选按近期传输量排序并直接显示流量。进程默认展示前 30 项，可展开或搜索。
- 补齐 Codex 的 `gpt-6.2-sol`、`gpt-6.2-astra` 官方费率，恢复可回填历史请求的会话费用占比。

退出旧版后用新 App 替换，线路、规则与历史数据会保留。已有 MCP 会话需要重新连接。从 0.1.2 或更早版本升级仍涉及日志转换，升级后的库不支持降级读取。

安装包采用临时签名，尚未通过 Apple 公证。首次打开若被 macOS 拦截，请参照附件中的安装说明。内置 sing-box v1.14.3。沿用既有 GeoLite 来源，条款核实记录见公开源码。

附件提供 DMG、SHA256 校验文件、构建 manifest、离线安装说明和完整版本说明。

[对应源码](https://github.com/Moore-developers/pax-connect-app/tree/v0.1.5) · [反馈问题](https://github.com/Moore-developers/pax-connect-app/issues)
