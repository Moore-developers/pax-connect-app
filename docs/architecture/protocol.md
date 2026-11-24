# 架构：IPC 与协议（§4）

## 4. IPC 与协议

`routes.usage.overview.v1` 单线路今日查询可附 `alert_progress`（截止、覆盖、双向字节及设置）。`traffic.alerts.v1`见[合同](../changes/pax-connect-network-control-plane/traffic-alerts.md)。

明细见[合同](../changes/pax-connect-network-control-plane/connection-target-details-1121.md)。

MCP：Hello.daemon_instance标识进程，timezone为日历查询时区。`operations.query.v1`提供`GetOperation`，按operation_id或client_id/request_id回读受控状态；实例变化、过期、未找到均是unknown，不能推断失败或重发。`OperationContext`绑定预期实例、稳定客户端和带时间戳UUID请求身份；SetProxyEnabled、SelectRoute、TestRoute、RecheckCurrentPath、CancelOperation、异步诊断/环境启动及M2导入确认、线路保存/删除/TLS/分组/组合、订阅刷新/删除/恢复已接入。旧App不传context仍走既有路径，Watch不重放旧结果。保留/权限合同见[MCP工具](../changes/pax-mcp/tool-contracts.md)§3。

context操作在受理前预留256项有界结果名额，活跃和10分钟内终态不淘汰，满则拒绝；相同请求同内容返回原operation_id，异内容request_conflict，旧时间戳不重新执行。结果variant包含RouteSaveResult、ProxyOperationResult、RouteMeasurementsResult、CurrentPathCheck及线路/网络/环境诊断报告；在原owner提交处冻结，保留选定/运行线路区别及批次逐项失败。OperationProgress的completed/total有presence，未知不造0。单结果≤1MiB、总预算32MiB在入队前预留，超预算拒绝新任务。为避免循环import，ProtectionStatus、RouteTestSummary、DownloadTestSummary、CurrentPathCheck移入operation.proto，EnvironmentDiagnosticRequest移入pax.proto；类型全名及原字段编号不变，不传任意业务JSON。


`ai.profiles.operations.v1`将Profile mutation、Plan、Apply及StartAIProfileModels接入公共OperationContext/GetOperation。导入增加客户端生成的稳定UUID；旧App缺省ID仍由owner生成。目录刷新在AIProfilesServer拥有的有界任务中运行，stdio/RPC断开不撤销受理工作，关闭时取消目录读取并排空本地事务；只目录读取可取消。save/import先冻结保存回执，再独立发现模型，不能把目录失败说成未保存。AIProfileOperationResult含workspace/plan、保存或删除ID与应用outcome，引用ai_profile_state.proto无凭据投影，保留原消息与字段号。Go标准Workspace/Plan与Swift AIProfilesWorkspace/AIProfilePlan、DaemonAIProfileOperation同步；没有第二份配置写入或结果数据库。完整权限、DSH拒写与计划合同见[M6](../changes/pax-mcp/tools-ai-settings.md#m6profile与客户端配置)。

`routes.edit_view.v1`：GetRouteEditView在daemon移除凭据，仅返回非秘密单outbound与JSON Pointer存在性；组合返回成员引用。SaveRouteEdit在单写队列内先CAS、同协议合并凭据/显式SecretUpdates，再typed校验并提交。非秘密JSON是整体替换；订阅覆盖沿既有DocumentOverride保存。UpdateRouteTLS.expected_route_revision供MCP绑定可见线路版本，原expected_revision仍是App使用的TLS策略版本；两者在owner内校验。预览context绑定实例、客户端和有界TTL内请求摘要，确认沿公共OperationContext。完整行为见[M2合同](../changes/pax-mcp/tools-routes.md)。

`QueryRoutingProcesses`附应用和进程的字节排行。

设置：[协议](settings-operations.md)。

CF接口合同见[CF操作协议](cf-operations.md)。

`network.operations.v1`提供StartConnectionDiagnostic、StartNetworkDiagnostic与StartEnvironmentSources；原同步RPC继续服务App，新入口仍由原pathDiagnostics/testOwner管理准入、取消、45秒/20秒预算和关停。InspectEnvironment(start)返回operation_id/expires_at；已有在途环境或资料重试返回environment_busy，不关闭其他会话。仅start返回短期browser_url，终态缓存清除URL；缺浏览器与App元数据的MCP投影见[M4合同](../changes/pax-mcp/tools-actions.md)。显式App代理/选路/测量/通道复查也登记owner=app，供跨客户端查取消；不把client_id作为鉴权。

DiagnoseNetwork进度字段及只读查询见[合同](../changes/pax-connect-network-control-plane/network-diagnostics-progress.md)。

HistoryState.error_code=history_building：日志建立中，采集状态独立，无proto变更。

引导：[合同](../changes/pax-connect-network-control-plane/first-run-onboarding.md)。Hello新增initial_install（onboarding.v1），启动建库前冻结；AI绑定附endpoint/target_path/requires_model，计划增加initialize/model_id与initializes/target_path，创建仍经Plan→Apply，无Key回传。

用户模式通告`paxd.user`、`proxy.power`，不通告旧`app.session`。Hello字段7 `owner_app_pid`标识所属App。正式App订阅与每条命令（含退出）先核对协议、App PID、能力与bundle指纹；指纹算法见下文“后台服务身份”。`PAX_SOCKET`保留开发入口。

| 新增稳定产品码 | 触发 | 传递与呈现 |
|---|---|---|
| `core_required` | ON 时缺失或无响应 core | ACK → OperationResult.error；App 只为未撤销的 ON 授权续接 |
| `core_identity_mismatch` | 内核协议/指纹/App owner 不匹配 | 不发送配置，提示退出其它实例 |
| `core_unavailable` / `core_profile_start_failed` | 内核清理/启动不可确认 | 明确失败，不从 ACK 推断运行成功 |
| `protection_unverified` | 内核 STARTED 尚无实际出口证据 | DEGRADED 保留真实边界 |

`core_profile_start_failed`沿现有`ProductError.message_params`透传一个受控原因（词表见core/start_failure.go），不传上游原文；Darwin安装路由的EEXIST标为route_conflict，端口占用标为address_in_use，未知原因不归咎线路配置。用户paxd沿既有diagnostics.jsonl记录core_prepare/core_apply/core_release/core_rollback及operation ID和耗时；这些是生命周期证据，不是已验证出口。

`ShutdownAppSession` 同时用于用户子进程，排空后退出。用户 socket 为 `<os.UserHomeDir()>/Library/Application Support/Pax Connect/run/paxd.sock`；含 NUL 不得超过104字节，超长时使用已验证属于 euid 且0700的私有 os.TempDir() 下 `paxd/p.sock`，拒绝 `/tmp`、`/private/tmp`。唯一规则为 ipc.UserSocketPath；App 调 bundled daemon 的 `--print-user-socket`（只打印路径，不启动或读数据）后显式传 `--socket`。目录0700、socket0600；每次 accept 复用 LOCAL_PEERCRED、只准入 euid，绝不走 root 用户登记。旧Listen合同保留。

`api/pax_core.proto` 定义独立 `pax.core.v1.PaxCore`（内部协议版本由 paxd 与 core 的 Hello 互验），只用于用户 paxd ↔ root pax-core，**不是 App-facing**，只生成 Go。RPC 为 Hello（运行可执行文件 SHA-256、版本、owner PID、上游状态）、ApplyProfile（JSON/revision）、ClearProfile、IdentifyTUNOwner（只识别）、Shutdown。错误 message 使用稳定 snake_case 产品 reason，拒绝配置前不调用内核；白名单与 reason 唯一实现见 `internal/singbox/core_profile.go`。同一UDS暴露 `daemon.StartedService`，unary/stream interceptor 只准入票48列出的11个读取/选路/连接方法，其余 PermissionDenied。socket parent 为 root:wheel 0755，socket 为 owner-uid 0600；accept 核对 LOCAL_PEERCRED uid。owner PID/UID/内核启动时间绑定看门狗，1秒轮询；Clear 保留进程，Shutdown/owner退出/信号先清实例再关RPC和socket。root state 0700，cache 固定 `<state>/cache.db`。root验收见[tests/proxy-core](../../tests/proxy-core/RESULTS.md)。

ApplyProfile 的 core 重写合同：`log.disabled` 强制为 false，`log.level` 收敛到 info/warn/error（fatal/panic → error，其余 → info）；`cache_file` 强制为 enabled 和 `<state>/cache.db`，调用方 `store_*` 不保留；`clash_api` 整体清除，包括 `store_selected`、`default_mode`。paxd 必须持有已选线路并在 Apply/reload 后重新应用，不依赖 sing-box selector 持久化。配置校验策略唯一实现为 `internal/singbox/core_profile.go`。系统代理仅允许一个 mixed inbound，固定 `127.0.0.1:<--mixed-port>`，使异常退出后的启动清理可覆盖它；不设置系统代理的 mixed 仍限 IPv4/IPv6 loopback。这些是配置处理合同，无 wire schema 变更。

旧app.session已退役；当前启动、退出与身份合同见[runtime §17](runtime.md#17-构建签名与安装)。

**传输**：grpc-swift 2（`GRPCNIOTransportHTTP2` 的 Posix 传输，UDS target）↔ Go `grpc-go`。

Socket及身份见[runtime](runtime.md)§17；仅限本机UDS。

**T3 快照同步**：Snapshot追加pending_operation_id（字段8）和degraded_reason（字段9），重连可恢复未完成开关与实际降级原因。ACK只代表命令入队，最终OperationResult与快照决定状态。

**后台服务身份**：`HelloResponse.service_fingerprint`（字段4）提供运行中的已签名daemon字节与bundle plist字节拼接的SHA-256。App用bundle指纹核对实际服务；旧daemon无此字段且版本相同属于身份未知，不自动重注册，要求显式重新登记。更新与错误行为见T2 spec。

跨端呈现语义及会话上限行见[呈现合同](presentation.md)。

**接口形态**：字段见`api/*.proto`；RPC族见§4.1。

- 命令只返回 `OperationAck{operation_id}`，最终结果通过 `Watch` 的 `OperationResult` 回来；UI 永远以 daemon 推送为准。
- 重连：退避 → `Hello` → `GetSnapshot` → `Watch(from_seq = snapshot.snapshot_version)`；丢弃 已消费的重复 `seq` 与早于快照版本的增量。
- 快照版本在 T2 为一致快照覆盖的事件高水位；读取/订阅不产生版本变化。Watch 首帧提供当前一致快照确认订阅，客户端此后才显示已连接；daemon 新实例从0开始，客户端新会话重置游标。具体截止、去重和恢复契约见变更 spec 的 T2 节。
- 大列表（日志页、分流页）走 `Query*` 分页，不从 stream 里全量拿。何时推送、何时变号、App 何时拉取见 §4.2。

`ai.usage.limits.summary.v1`：汇总/日桶/同期追加可选input_tokens，缺失为未知，口径见P07d §4.1。[合同](../changes/pax-connect-network-control-plane/ui/specs/P07d-ai-session-limits.md)。上限查询可成对给`comparison_from_ms/to_ms`指定上一完整日历周期；须为正数、跨度≤367天且结束不晚于当前起点。daemon按本机日历映射当前已过天数与时刻，超过上期末则不比较；未给则等长回移。任一期过期、截断、缺样本或未定价，不回同期比较。

会话分析与目录合同见[P07d](../changes/pax-connect-network-control-plane/ui/specs/P07d-ai-sessions.md)及[本机目录](../changes/pax-connect-network-control-plane/native-session-catalog.md)。`ai.usage.session_agents.v1`门控父子聚合与代理摘要，`ai.sessions.catalog.activity.v1`门控含请求的日期候选和目录/用量双版本分页。代理元数据只推进catalog_revision，经AIUsageState失效会话页；追加字段见api/ai_usage.proto。

AI日志可选`min_official_cost_micro`/`max_official_cost_micro`及能力`ai.usage.cost_range.v1`；范围/校验见[采集合同](../changes/pax-connect-network-control-plane/ai-usage-collection.md)§7。

### 4.1 协议演进规则与 RPC 族

**演进规则**（所有族通用）：

- 产品协议版本以 Go `ipc.ProtocolVersion` 与 Swift `DaemonConnection.protocolVersion` 为准（须一致），内部 core 版本以 `api/pax_core.proto` 的握手校验为准；文档不再记版本号。签名见 `api/*.proto`。只增字段/RPC，删字段须 reserved 编号及名称。
- 客户端配置历史按游标分页。
- 能力门控：`Hello.capabilities` 只通告已装配的能力，缺失即 App 禁用对应动作；旧后台不伪称支持新能力。
- 命令只返回 `OperationAck`，终态走 `Watch` 的 `OperationResult`；会改目录 / 偏好 / 来源的请求带 `expected_*revision` 做 CAS，`client_id` + `request_id` 保证重试幂等（有界保留终态），冲突返回 `catalog_stale` 等稳定产品码并附当前 revision。
- 长读取走 `Query*` 分页并绑定读令牌，不进 `Watch` 全量推送；产品错误一律 `ProductError{code, message_params}`，不透传内核原文、凭据或完整 URL。
- 跨端模型（proto / Go `config` 标准值 / Swift `PaxClient`）同一提交更新；上游 sing-box 类型不越过 `internal/singbox`。

RPC族、流量查询与能力见[服务索引](rpc-services.md)。

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
| 流量 | `traffic_revision`（无 `quality_revision`：字段 13 已 reserved，状态类字段按 R8 随值送达） | `traffic_deletion_generation`（过期清理时与 revision 同事务加一，`history_traffic_maintenance.go`） | 流量与延迟行入库事务（`history_traffic_apply.go`）；只有存入会改变查询结果的观测才加号，poll / gap 行不加 | `history_delta`（状态字段值变化也推，不变号） | `traffic`、`dashboard` |
| 线路历史 | `route_history_revision` | 无：线路历史无清空操作，只有 7 天 / 条数裁剪；`clear_generation` 属 logs.db，不复用 | `config.RouteHistory.Revision`：自动延迟新点（30 s）、测量结果、裁剪都加一，与 `Items` 同文件落盘；映射入 `HistoryState` | `history_delta`；App 版本变化即整窗重拉 | `dashboard`（线路趋势）、线路页 |
| AI 用量 | `AIUsageState.revision` | `AIUsageState.generation`（93 天清理删行、重建时与 revision 同事务加一，R5） | aiusage 入库 / 重算事务落库；回填、扫描错误、skipped 只走状态字段（R8） | `ai_usage`（版本号、代数或状态字段变化，每秒最多一次）；首帧 `Snapshot.ai_usage` | `ai_overview`、`ai_logs`、`ai_profiles` |

`HistoryState.revision`仅作概览已有统计标记（`>0`），不作版本号。`SetViewVisibility` 仅影响采集与探测节奏，不改变推送；Watch 订阅全部结束即清空；`ai_overview` / `ai_logs` / `dashboard` 任一可见时 AI 用量采集走前台节奏。

`HistoryState.sqlite_storage` 每 5 秒读一次磁盘占用，随快照和历史推送更新。缺省为未知。大小变化不推进日志版本。口径见 [设置](../changes/pax-connect-network-control-plane/ui/specs/P05-settings.md#日志)。

内部 `pax_core.proto` 另含 UpdateRouting（只改路由匹配表，不重启实例、不关旧连接）、ReadObservations / ReadUsage（只读、不传连接明细）、IdentifyTUNOwner（只识别）。

`ai.usage.buckets.timezone.v1`支持QueryAIUsageBuckets可选IANA timezone，日桶在原owner按该时区分组；缺省保留本机口径。AI日志在同一reader事务读取revision/generation与计数/合计/行。`ai.usage.logs.snapshot.v1`支持可选成对expected_revision/expected_generation，续页或缓存余项回源核验不符返回`query_changed`；不带水位保留App独立页合同。读取后跨generation仍返回`ai_usage_busy`，响应state保留该页事务的水位，不冒用稍后追加版本。

AI用量RPC限用户socket、5秒（重算30秒）、范围≤367天；不传正文/密钥。known_revision未变只回state/not_modified；可见性由SetViewVisibility上报。日志10条分页，结果计数排除自身筛选；可选值用presence。last_used不受时段限制；全时段汇总读永久小时桶。

协议46：AI追加service_source、渠道金额覆盖状态及对应sources.v1能力；字段见api/ai_usage.proto与ai_profiles.proto，语义见[来源](../changes/pax-connect-network-control-plane/ai-service-sources.md)。

`AIProfileSummary.model_multipliers`按请求模型覆盖默认倍率，保存时整表替换，客户端须原样带回；值范围0–100、至多3位小数，否则`ai_profile_invalid`。`RepriceAIUsage`支持模型、含两端的本地日期或全部范围、试算；字段见`api/ai_usage.proto`。仅剩小时桶时按桶重估，试算override_id为0。倍率取daemon该Profile当前值；错误：范围/参数`ai_usage_range_invalid`（InvalidArgument）、Profile不存在`ai_profile_not_found`（NotFound）、采集超时`ai_usage_busy`（Unavailable）。语义与覆盖优先级见AI采集§4。

追加字段：`RouteExitSummary.error_params=8`（repeated string，与 ProductError.params 同义，ws 握手失败时放 HTTP 状态码等参数）；`Snapshot.external_tun=33`（`ExternalTUN{interface=1, target=2, at=3}`，定义在 `privacy.proto`；仅当非 Pax 的 utun 承接公网默认路由时非空，如 utun6 / 128.0/1）。

`RoutingPackage.kind` 新值 `adblock`，见[广告包合同](../changes/pax-connect-network-control-plane/adblock.md)。

环境检测：InspectEnvironment(start/read/cancel/sources)为45秒短会话，Mac/真实浏览器独立采样，不绑定所选线路；取消/关窗关闭loopback。EnvironmentCheck/Probe字段见proto。字段/RPC唯一见api/network_diagnostics.proto，亦含ConnectionDiagnostic/TrafficPath，编号不变；行为见[网络诊断](../changes/pax-connect-network-control-plane/network-diagnostics.md)。本轮DNS复用Check的dns_path/dns_upstream及core观测JSON的DNSFact可选上游字段，不改proto；旧core缺字段保留未知，见[DNS路径](../changes/pax-connect-network-control-plane/network-diagnostics-dns-path.md)。
