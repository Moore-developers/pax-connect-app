Pax Connect 0.1.4 Beta，适用于 macOS 26+ / Apple Silicon。

- 新增本机MCP：查询网络、线路、日志、AI用量与会话；显式授权后可管理线路/订阅、诊断网络、切换API配置、CF优选及修改设置。
- 设置页可检查MCP就绪状态并接入Codex、Claude Code及DeepSeek Harness。默认只读，可选查询与诊断；保留已有权限，冲突不覆盖，配置写入前备份。
- 操作支持任务回查与取消、版本冲突检查及敏感字段脱敏。不会自动启动Pax或启用代理。升级后请重新连接已有MCP会话。

查询所得日志、域名或会话数据可能由AI客户端发送至其服务。入口验证通过不代表目标客户端已经连接。

沿用临时签名、未公证Beta。首次打开可能被macOS拦截，按附件安装说明操作，无需关闭Gatekeeper。保留既有GeoLite来源及分发条件待办，见源码docs/release/geolite-redistribution.md。

附件包含DMG、SHA256SUMS、manifest、安装说明与完整版本说明。源码为v0.1.4标签；内置sing-box v1.14.3。保留用户配置和历史；从0.1.2及更旧版本升级仍涉及日志转换，不支持降级。
