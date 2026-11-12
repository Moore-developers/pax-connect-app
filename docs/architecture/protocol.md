# 架构：IPC 与协议（§4）

## 4. IPC 与协议

DiagnoseNetwork进度字段及只读查询见[合同](../changes/pax-connect-network-control-plane/network-diagnostics-progress.md)。

引导：[合同](../changes/pax-connect-network-control-plane/first-run-onboarding.md)。Hello新增initial_install（onboarding.v1），启动建库前冻结；AI绑定附endpoint/target_path/requires_model，计划增加initialize/model_id与initializes/target_path，创建仍经Plan→Apply，无Key回传。

用户模式通告`paxd.user`、`proxy.power`，不通告旧`app.session`。Hello字段7 `owner_app_pid`标识所属App。正式App的订阅与每条新命令连接（含退出）都先核对协议、App PID、能力与bundle指纹；指纹算法见下文“后台服务身份”。`PAX_SOCKET`保留开发入口。

| 新增稳定产品码 | 触发 | 传递与呈现 |
|---|---|---|
| `core_required` | ON 时缺失或无响应 core | ACK → OperationResult.error；App 只为未撤销的 ON 授权续接 |
| `core_identity_mismatch` | 内核协议/指纹/App owner 不匹配 | 不发送配置，提示退出其它实例 |
| `core_unavailable` / `core_profile_start_failed` | 内核清理/启动不可确认 | 明确失败，不从 ACK 推断运行成功 |
| `protection_unverified` | 内核 STARTED 尚无实际出口证据 | DEGRADED 保留真实边界 |

`core_profile_start_failed`沿现有`ProductError.message_params`透传一个受控原因（词表见core/start_failure.go），不传上游原文；Darwin安装路由的EEXIST标为route_conflict，端口占用标为address_in_use，未知原因不归咎线路配置。用户paxd沿既有diagnostics.jsonl记录core_prepare/core_apply/core_release/core_rollback及operation ID和耗时；这些是生命周期证据，不是已验证出口。

`ShutdownAppSession` 同时用于用户子进程，排空后退出。用户 socket 为 `<os.UserHomeDir()>/Library/Application Support/Pax Connect/run/paxd.sock`；含 NUL 不得超过104字节，超长时使用已验证属于 euid 且0700的私有 os.TempDir() 下 `paxd/p.sock`，拒绝 `/tmp`、`/private/tmp`。唯一规则为 ipc.UserSocketPath；App 调 bundled daemon 的 `--print-user-socket`（只打印路径，不启动或读数据）后显式传 `--socket`。目录0700、socket0600；每次 accept 复用 LOCAL_PEERCRED、只准入 euid，绝不走 root 用户登记。旧Listen合同保留。

`api/pax_core.proto` 定义独立 `pax.core.v1.PaxCore`（内部协议版本由 paxd 与 core 的 Hello 互验），只用于用户 paxd ↔ root pax-core，**不是 App-facing**，只生成 Go。RPC 为 Hello（运行可执行文件 SHA-256、版本、owner PID、上游状态）、ApplyProfile（JSON/revision）、ClearProfile、IdentifyTUNOwner（只识别）、Shutdown。错误 message 使用稳定 snake_case 产品 reason，拒绝配置前不调用内核；白名单与 reason 唯一实现见 `internal/singbox/core_profile.go`。同一 UDS 上暴露 `daemon.StartedService`，unary/stream interceptor 只准入票48列出的11个读取/选路/连接方法，其余 PermissionDenied。socket parent 为 root:wheel 0755，socket 为 owner-uid 0600；accept 核对 LOCAL_PEERCRED uid。owner PID/UID/内核启动时间绑定看门狗，1秒轮询；Clear 保留进程，Shutdown/owner退出/信号先清实例再关RPC和socket。root state 0700，cache 固定 `<state>/cache.db`。root验收见[tests/proxy-core](../../tests/proxy-core/RESULTS.md)。

ApplyProfile 的 core 重写合同：`log.disabled` 强制为 false，`log.level` 收敛到 info/warn/error（fatal/panic → error，其余 → info）；`cache_file` 强制为 enabled 和 `<state>/cache.db`，调用方 `store_*` 不保留；`clash_api` 整体清除，包括 `store_selected`、`default_mode`。paxd 必须持有已选线路并在 Apply/reload 后重新应用，不依赖 sing-box selector 持久化。配置校验策略唯一实现为 `internal/singbox/core_profile.go`。系统代理仅允许一个 mixed inbound，固定 `127.0.0.1:<--mixed-port>`，使异常退出后的启动清理可覆盖它；不设置系统代理的 mixed 仍限 IPv4/IPv6 loopback。这些是配置处理合同，无 wire schema 变更。

旧app.session已退役；当前启动、退出与身份合同见[runtime §17](runtime.md#17-构建签名与安装)。

**传输**：grpc-swift 2（`GRPCNIOTransportHTTP2` 的 Posix 传输，UDS target）↔ Go `grpc-go`。

**Socket 与鉴权（旧 root 路径，保留的 Listen 合同；用户 paxd 的 socket 规则见上）**：

- 路径 `/var/run/pax-connect/pax.sock`；目录 `root:admin 0750`，socket `0660`。
- daemon 在 accept 时用 `LOCAL_PEERCRED` 取对端 uid，只放行已登记的用户 uid。
- P1：用 `LOCAL_PEERTOKEN` 取 audit token，校验客户端代码签名的 Team ID / bundle ID（需 cgo 调 Security.framework）。

**T3 快照同步**：Snapshot追加pending_operation_id（字段8）和degraded_reason（字段9），重连可恢复未完成开关与实际降级原因。ACK只代表命令入队，最终OperationResult与快照决定状态。

**后台服务身份**：`HelloResponse.service_fingerprint`（字段4）提供运行中的已签名daemon字节与bundle plist字节拼接的SHA-256。App用bundle指纹核对实际服务；旧daemon无此字段且版本相同属于身份未知，不自动重注册，要求显式重新登记。具体更新事务与错误行为以T2 spec为准。

**接口形态**：签名与字段见 `api/*.proto`，各族合同见 §4.1。

- 命令只返回 `OperationAck{operation_id}`，最终结果通过 `Watch` 的 `OperationResult` 回来；UI 永远以 daemon 推送为准。
- 重连：退避 → `Hello` → `GetSnapshot` → `Watch(from_seq = snapshot.snapshot_version)`；丢弃 已消费的重复 `seq` 与早于快照版本的增量。
- 快照版本在 T2 为一致快照覆盖的事件高水位；读取/订阅不产生版本变化。Watch 首帧提供当前一致快照确认订阅，客户端此后才显示已连接；daemon 新实例从0开始，客户端新会话重置游标。具体截止、去重和恢复契约见变更 spec 的 T2 节。
- 大列表（日志页、分流页）走 `Query*` 分页，不从 stream 里全量拿。何时推送、何时变号、App 何时拉取见 §4.2。

`ai.usage.limits.summary.v1`：汇总/日桶/同期追加可选input_tokens，缺失为未知，口径见P07d §4.1。[合同](../changes/pax-connect-network-control-plane/ui/specs/P07d-ai-session-limits.md)。上限查询可成对给`comparison_from_ms/to_ms`指定上一完整日历周期；须为正数、跨度≤367天且结束不晚于当前起点。daemon按本机日历映射当前已过天数与时刻，超过上期末则不比较；未给则等长回移。任一期过期、截断、缺样本或未定价，不回同期比较。

会话分析与目录合同见[P07d](../changes/pax-connect-network-control-plane/ui/specs/P07d-ai-sessions.md)及[本机目录](../changes/pax-connect-network-control-plane/native-session-catalog.md)。`ai.usage.session_agents.v1`门控父子聚合与代理摘要，`ai.sessions.catalog.activity.v1`门控含请求的日期候选和目录/用量双版本分页。代理元数据只推进catalog_revision，经AIUsageState失效会话页；追加字段见api/ai_usage.proto。

AI日志可选`min_official_cost_micro`/`max_official_cost_micro`及能力`ai.usage.cost_range.v1`；范围/校验见[采集合同](../changes/pax-connect-network-control-plane/ai-usage-collection.md)§7。

### 4.1 协议演进规则与 RPC 族

**演进规则**（所有族通用）：

- 产品协议版本以 `controller.ProtocolVersion` 与 Swift `PaxClient.protocolVersion` 为准（须一致），内部 core 版本以 `api/pax_core.proto` 的握手校验为准；文档不再记版本号。字段编号、RPC 签名唯一见 `api/*.proto`，只加字段 / RPC，不改编号；删除字段或 payload 须 reserved 其编号与名称。
- 能力门控：`Hello.capabilities` 只通告已装配的能力，缺失即 App 禁用对应动作；旧后台不伪称支持新能力。
- 命令只返回 `OperationAck`，终态走 `Watch` 的 `OperationResult`；会改目录 / 偏好 / 来源的请求带 `expected_*revision` 做 CAS，`client_id` + `request_id` 保证重试幂等（有界保留终态），冲突返回 `catalog_stale` 等稳定产品码并附当前 revision。
- 长读取走 `Query*` 分页并绑定读令牌，不进 `Watch` 全量推送；产品错误一律 `ProductError{code, message_params}`，不透传内核原文、凭据或完整 URL。
- 跨端模型（proto / Go `config` 标准值 / Swift `PaxClient`）同一提交更新；上游 sing-box 类型不越过 `internal/singbox`。

**RPC 族**（能力名以 `daemon/internal/controller` 通告为准）：

[新增结果合同](../changes/pax-connect-network-control-plane/manual-proxy-routes.md)。

| 族 | 主要 RPC | 能力前缀 | 合同 |
|---|---|---|---|
| 握手 / 快照 / 事件 | Hello、GetSnapshot、Watch、SetViewVisibility（可见页面集合，§4.2 R7） | `proxy.*`、`paxd.user` | 本文 §4 |
| 开关 / 选路 / 电源 | SetProxyEnabled、SelectRoute、CancelOperation、NotifyPowerEvent；ReconnectProxy 仅留 wire 兼容（返回 Unimplemented，不通告） | `proxy.core`、`proxy.power_events`、`routes.select.*` | [runtime.md](runtime.md) §2.1 |
| 线路目录 / 编辑 / 组合 | PreviewRoutes、ConfirmImportRoutes、Add / Update / DeleteRoute、GetRouteEditor、SaveRouteGroups、SaveChainRoute、UpdateRouteTLS、ConfirmRouteTLSBatch | `routes.catalog` / `.import` / `.groups` / `.chains` / `.tls.batch` | [spec D-02](../changes/pax-connect-network-control-plane/spec.md#d-02-最小协议增量r-001r-041)、[node-tls-verification](../changes/pax-connect-network-control-plane/decisions/node-tls-verification.md) |
| 订阅 | RefreshSubscription、DeleteSubscription、RestoreSubscriptionRoute | `subscriptions.*` | spec 订阅章节；刷新 URL 仅 daemon 持有，可借已保存线路取数 |
| 线路测量 | TestRoute（latency / download / diagnostic，批量 `targets`）、Get / Save / EvaluateMeasurementPreferences、GetPhysicalEgress | `routes.test.*`、`routes.measure.*`、`routes.download.*`、`measurement.*`、`network.physical_egress` | [route-measurements](../changes/pax-connect-network-control-plane/route-measurements.md) |
| CF 优选 | QueryCFCandidates、ValidateCFCandidate、QueryCFWorkspace、RunCFTask | `routes.cf.*` | [cf-result-feedback](../changes/pax-connect-network-control-plane/cf-result-feedback.md) |
| 诊断 | GetTrafficPathDiagnostics、DiagnoseConnection、DiagnoseNetwork、InspectEnvironment、RecheckCurrentPath | `network.*diagnostics*`、`connection.current_path.*` | [基础连接](../changes/pax-connect-network-control-plane/protocol-connection-plan.md)、[网络诊断](../changes/pax-connect-network-control-plane/network-diagnostics.md) |
| 日志 / 流量 / 历史 | QueryLogs、QueryLogObjects、GetLogDetail、QueryLogUpdates、ClearLogs、QueryTraffic、QueryTrafficDetail、QueryObjectIcon、QueryRouteHistory、QueryRouteUsage | `history.*`、`objects.icons.v1`、`routes.history` / `.usage` | [history-storage](../changes/pax-connect-network-control-plane/history-storage.md)、[traffic-system-latency](../changes/pax-connect-network-control-plane/traffic-system-latency.md)、[traffic-source-classification](../changes/pax-connect-network-control-plane/traffic-source-classification.md) |
| 规则 | QueryRoutingRules、QueryRoutingLibrary、MutateRoutingLibrary、QueryGeoRules | `routing.*` | [routing-precedence](../changes/pax-connect-network-control-plane/decisions/routing-precedence.md)、[geo-dns-protection](../changes/pax-connect-network-control-plane/decisions/geo-dns-protection.md) |
| 网络保护 / 设置 | GetNetworkPrivacy、SaveNetworkPrivacy、UpdateSettings（仅日志保留 field mask） | `network.privacy.v1`、`history.retention.v1` | [network-privacy](../changes/pax-connect-network-control-plane/network-privacy.md) |
| AI 配置 | `api/ai_profiles.proto` 的 AIProfilesService | `ai.profiles.v1` / `ai.cli_apply.v1` | [P07](../changes/pax-connect-network-control-plane/ui/specs/P07-ai-usage.md) |
| AI 本地用量 | `api/ai_usage.proto` 的 AIUsageService：QueryAIUsageBuckets、SummarizeAIUsage、ListAIUsageLogs、RepriceAIUsage | `ai.usage.v1` / `ai.usage.cost_range.v1` | [AI采集](../changes/pax-connect-network-control-plane/ai-usage-collection.md) §3–7 |

TrafficRow.latest_match为可选最近匹配证据；语义见[最近命中](../changes/pax-connect-network-control-plane/latest-routing-match.md)。近期响应见[统一延迟](../changes/pax-connect-network-control-plane/traffic-system-latency.md)。

网络诊断提供者证据见`api/network_diagnostics.proto`：NetworkDiagnosticProvider保留确切查询IP/地址族、已返回能力、逐源状态/错误/HTTP/耗时/时间与备用能力/原因；source/fact以provider_id归因获取渠道。旧报告缺providers不冒充直接请求；provider_error只表示整体无可用资料，不能遮住部分成功。补充原因independent_coverage_missing兼容原备用历史；实际来源及预算以[诊断合同](../changes/pax-connect-network-control-plane/network-diagnostics.md#独立资料来源票1572026-11-09用户批准)为准。

### 4.2 推送与版本号规则

**规则**：

- **R1**：一类数据一个版本号加一个作废代数：状态与目录、日志、流量、线路历史、AI 用量。
- **R2**：只有某客户端可见的查询结果会不同（新增 / 修改 / 删除行）才加一；空轮询、心跳、内部记账、重试、读取不加。
- **R3**：删除改作废代数。日志可带`log_removal_before_ms`：时间前缀删除边界；同summary/journal且from≥边界保留日志令牌/缓存；0、清空、重建全撤销。流量证据仍失效，已打开流量详情保留。见[读模型](../changes/pax-connect-network-control-plane/history-read-model.md)§4。
- **R4**：单调；与数据同事务落库，重启不回退。
- **R5**：推送只带（域、版本号、代数）和小摘要；明细带已知版本号查询，未变返回 `not_modified`。AI 用量查询只带 `known_revision`，故其代数变化时 revision 必须同时加一。
- **R6**：每域每秒最多推一次，合并到最新值。
- **R7**：App 每域声明消费页面。`SetViewVisibility(views)` 整体替换可见集合，id 为 `dashboard` / `traffic` / `logs` / `ai_sessions` / `ai_overview` / `ai_logs` / `ai_profiles`；所有 Watch 订阅断开即视为全部不可见，重连后 App 重报。可见且版本更新才拉，隐藏只记脏、回来再拉，每页可设最小拉取间隔。
- **R8**：状态类字段（`collection_state`、`partial`、`backfilling`、`error_code` 等）随值本身送达，不动版本号。
- **R9**：测试覆盖三条不变式：空轮询不变号、删除动代数、重启单调。

域表：

| 域 | 版本号 | 作废代数 | 写入方与触发 | 推送 | App 消费页 |
|---|---|---|---|---|---|
| 状态与目录 | `Snapshot.snapshot_version`、`catalog_revision` | 无；daemon 新实例从 0 | controller 每次状态 / 目录 / 设置提交（`commitLocked`） | `snapshot` | 全部（ConnectionStore、RoutesStore） |
| 日志 | `logs_revision`；`log_head_sequence` 是增量游标，不是版本号 | `log_deletion_generation`、`clear_generation`、`journal_generation`、`summary_generation` | logs.db 入库 / 整理事务提交可见行才加；清空、删除、到期清理、重建改代数（`history_logs_*.go`）。空批次不写库、不加号；入库有新行即唤醒日志整理，不等定时 | `history_delta` | `logs`、`dashboard`、`traffic`（近期响应） |
| 流量 | `traffic_revision`（无 `quality_revision`：字段 13 已 reserved，状态类字段按 R8 随值送达） | `traffic_deletion_generation`（过期清理、容量淘汰时与 revision 同事务加一，`history_traffic_maintenance.go`） | 流量与延迟行入库事务（`history_traffic_apply.go`）；只有存入会改变查询结果的观测才加号，poll / gap 行不加 | `history_delta`（状态字段值变化也推，不变号） | `traffic`、`dashboard` |
| 线路历史 | `route_history_revision` | 无：线路历史无清空操作，只有 7 天 / 条数裁剪；`clear_generation` 属 logs.db，不复用 | `config.RouteHistory.Revision`：自动延迟新点（30 s）、测量结果、裁剪都加一，与 `Items` 同文件落盘；映射入 `HistoryState` | `history_delta`；App 版本变化即整窗重拉 | `dashboard`（线路趋势）、线路页 |
| AI 用量 | `AIUsageState.revision` | `AIUsageState.generation`（93 天清理删行、重建时与 revision 同事务加一，R5） | aiusage 入库 / 重算事务落库；回填、扫描错误、skipped 只走状态字段（R8） | `ai_usage`（版本号、代数或状态字段变化，每秒最多一次）；首帧 `Snapshot.ai_usage` | `ai_overview`、`ai_logs`、`ai_profiles` |

`HistoryState.revision`仅作概览已有统计标记（`>0`），不作版本号。`SetViewVisibility` 仅影响采集与探测节奏，不改变推送；Watch 订阅全部结束即清空；`ai_overview` / `ai_logs` / `dashboard` 任一可见时 AI 用量采集走前台节奏。

内部 `pax_core.proto` 另含 UpdateRouting（只改路由匹配表，不重启实例、不关旧连接）、ReadObservations / ReadUsage（只读、不传连接明细）、IdentifyTUNOwner（只识别）。

AI日志跨generation返回`ai_usage_busy`。

AI用量RPC限用户socket，5秒超时（重算30秒）；桶/日志/分析范围≤367天，不含正文/密钥。桶查询带可选known_revision（`visible` 租约已 reserved，改由 SetViewVisibility 上报）；未变化只返回state/not_modified，不重读用量。日志10条分页，结果计数排除自身筛选；可选价格/性能以proto presence表达。桶查询另返回last_used（每CLI最近请求时间，不受时段限制）。全时段汇总读永久小时桶，不读过期明细。

AI请求字段/presence/reserved编号见api/ai_usage.proto，归属与重建见AI采集§3–7；DSH只读。

`AIProfileSummary.model_multipliers`按请求模型覆盖默认倍率，保存时整表替换，客户端须原样带回；值范围0–100、至多3位小数，否则`ai_profile_invalid`。`RepriceAIUsage`支持模型、含两端的本地日期或全部范围、试算；字段见`api/ai_usage.proto`。仅剩小时桶时按桶重估，试算override_id为0。倍率取daemon该Profile当前值；错误：范围/参数`ai_usage_range_invalid`（InvalidArgument）、Profile不存在`ai_profile_not_found`（NotFound）、采集超时`ai_usage_busy`（Unavailable）。语义与覆盖优先级见AI采集§4。

追加字段：`RouteExitSummary.error_params=8`（repeated string，与 ProductError.params 同义，ws 握手失败时放 HTTP 状态码等参数）；`Snapshot.external_tun=33`（`ExternalTUN{interface=1, target=2, at=3}`，定义在 `privacy.proto`；仅当非 Pax 的 utun 承接公网默认路由时非空，如 utun6 / 128.0/1）。

`RoutingPackage.kind` 新值 `adblock`，见[广告包合同](../changes/pax-connect-network-control-plane/adblock.md)。

环境检测：InspectEnvironment的start/read/cancel/sources持有45秒短会话；EnvironmentCheck附带固定目标/状态/受控错误/耗时/时间，NetworkDiagnosticProbe附state/stage/redirect_host；full与局部检测合同统一见网络诊断。EnvironmentDiagnosticReport独立保存Mac/真实浏览器核心样本及本机回传URL，取消/关窗关闭loopback。不会读取所选线路；字段与RPC唯一见api/network_diagnostics.proto，细则见[网络诊断](../changes/pax-connect-network-control-plane/network-diagnostics.md)。基础ConnectionDiagnostic/TrafficPath消息同属该proto，类型全名与字段编号不变。
