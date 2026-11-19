# 产品RPC服务索引

本表由[协议§4](protocol.md#4-ipc-与协议)索引；只维护服务边界，字段编号以api/*.proto为准。

`history.application_workbench_page.v1` 门控 `QueryLogs` 的应用工作台分页模式。显式选择时省略筛选选项，总数绑定令牌和筛选，游标按排序时间和记录键定位。令牌过期后通过 `recovery_offset` 在新快照恢复目标页，越界取末页。默认请求保留原有字段和游标。

**RPC 族**（能力名以 `daemon/internal/controller` 通告为准）：

[新增结果合同](../changes/pax-connect-network-control-plane/manual-proxy-routes.md)。

| 族 | 主要 RPC | 能力前缀 | 合同 |
|---|---|---|---|
| 握手 / 快照 / 事件 | Hello、GetSnapshot、Watch、SetViewVisibility（可见页面集合，§4.2 R7） | `proxy.*`、`paxd.user` | 本文 §4 |
| 开关 / 选路 / 电源 | SetProxyEnabled、SelectRoute、CancelOperation、NotifyPowerEvent；ReconnectProxy 仅留 wire 兼容（返回 Unimplemented，不通告） | `proxy.core`、`proxy.power_events`、`routes.select.*` | [runtime.md](runtime.md) §2.1 |
| 线路目录 / 编辑 / 组合 | PreviewRoutes、ConfirmImportRoutes、Add / Update / DeleteRoute、GetRouteEditor、SaveRouteGroups、SaveChainRoute、UpdateRouteTLS、ConfirmRouteTLSBatch | `routes.catalog` / `.import` / `.groups` / `.chains` / `.tls.batch` | [spec D-02](../changes/pax-connect-network-control-plane/spec.md#d-02-最小协议增量r-001r-041)、[node-tls-verification](../changes/pax-connect-network-control-plane/decisions/node-tls-verification.md) |
| 订阅 | RefreshSubscription、DeleteSubscription、RestoreSubscriptionRoute | `subscriptions.*` | spec 订阅章节；刷新 URL 仅 daemon 持有，可借已保存线路取数 |
| 线路测量 | TestRoute（latency / download / diagnostic，批量 `targets`）、Get / Save / EvaluateMeasurementPreferences、GetPhysicalEgress | `routes.test.*`、`routes.measure.*`、`routes.download.*`、`measurement.*`、`network.physical_egress` | [route-measurements](../changes/pax-connect-network-control-plane/route-measurements.md) |
| CF 优选 | QueryCFCandidates、ValidateCFCandidate、QueryCFWorkspace、RunCFTask、StartCFValidation、SaveValidatedCFRoute | `routes.cf.*` | [cf-result-feedback](../changes/pax-connect-network-control-plane/cf-result-feedback.md) |
| 诊断 | GetTrafficPathDiagnostics、DiagnoseConnection / StartConnectionDiagnostic、DiagnoseNetwork / StartNetworkDiagnostic、InspectEnvironment / StartEnvironmentSources、RecheckCurrentPath | `network.*diagnostics*`、`network.operations.v1`、`connection.current_path.*` | [基础连接](../changes/pax-connect-network-control-plane/protocol-connection-plan.md)、[网络诊断](../changes/pax-connect-network-control-plane/network-diagnostics.md) |
| 日志 / 流量 / 历史 | QueryLogs、QueryLogObjects、GetLogDetail、QueryLogUpdates、ClearLogs、QueryTraffic、QueryTrafficDetail、QueryObjectIcon、QueryRouteHistory、QueryRouteUsage | `history.*`、`objects.icons.v1`、`routes.history` / `.usage` | [history-storage](../changes/pax-connect-network-control-plane/history-storage.md)、[traffic-system-latency](../changes/pax-connect-network-control-plane/traffic-system-latency.md)、[traffic-source-classification](../changes/pax-connect-network-control-plane/traffic-source-classification.md) |
| 规则 | QueryRoutingRules、QueryRoutingLibrary、MutateRoutingLibrary、QueryGeoRules | `routing.*` | [routing-precedence](../changes/pax-connect-network-control-plane/decisions/routing-precedence.md)、[geo-dns-protection](../changes/pax-connect-network-control-plane/decisions/geo-dns-protection.md) |
| 网络保护 / 设置 | GetNetworkPrivacy、SaveNetworkPrivacy、UpdateSettings（仅日志保留 field mask） | `network.privacy.v1`、`history.retention.v1` | [network-privacy](../changes/pax-connect-network-control-plane/network-privacy.md) |
| AI 配置 | `api/ai_profiles.proto` 的 AIProfilesService | `ai.profiles.v1` / `ai.cli_apply.v1` / `ai.profiles.operations.v1` | [P07](../changes/pax-connect-network-control-plane/ui/specs/P07-ai-usage.md)、[M6](../changes/pax-mcp/tools-ai-settings.md#m6profile与客户端配置) |
| AI 本地用量 | `api/ai_usage.proto` 的 AIUsageService：QueryAIUsageBuckets、SummarizeAIUsage、ListAIUsageLogs、RepriceAIUsage | `ai.usage.v1` / `ai.usage.cost_range.v1` | [AI采集](../changes/pax-connect-network-control-plane/ai-usage-collection.md) §3–7 |

TrafficRow.latest_match为可选最近匹配证据；语义见[最近命中](../changes/pax-connect-network-control-plane/latest-routing-match.md)。近期响应见[统一延迟](../changes/pax-connect-network-control-plane/traffic-system-latency.md)。

网络诊断提供者证据见`api/network_diagnostics.proto`：NetworkDiagnosticProvider保留确切查询IP/地址族、已返回能力、逐源状态/错误/HTTP/耗时/时间与备用能力/原因；source/fact以provider_id归因获取渠道。旧报告缺providers不冒充直接请求；provider_error只表示整体无可用资料，不能遮住部分成功。补充原因independent_coverage_missing兼容原备用历史；实际来源及预算以[诊断合同](../changes/pax-connect-network-control-plane/network-diagnostics.md#独立资料来源票1572026-11-09用户批准)为准。

