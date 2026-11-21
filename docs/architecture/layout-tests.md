# 架构：目录结构与测试（§16、§18）

## 16. 目录结构

发布入口：AGENTS.md指向docs/release/WORKFLOW.md；BUILD.md定义本地打包。scripts/public-allowlist.txt包含导出所需的release安装/版本材料，私有执行记录留在tests/release/。

术语入口为根GLOSSARY.md；网络观测、界面语言补充定义在docs/glossary/network-observation.md与interface-language.md。



```text
pax-connect/
├── api/
│   ├── pax.proto                        # App ↔ 后台产品 gRPC
│   ├── pax_core.proto                   # paxd ↔ root core 内部 gRPC，仅Go
│   └── ai_profile_state / ai_profiles / ai_usage / network_diagnostics / operation / operation_context / privacy / route_edit / route_exit / route_usage / traffic .proto   # 按族拆出的产品 gRPC 消息
├── patches/{sing-box,sing-tun}/          # 固定版本上游补丁（观测、主动准入、路由/DNS热更、DNS进程归属、代理关闭归属、路由保护）及无root回归
├── daemon/
│   ├── go.mod                           # 锁定 sing-box Go module
│   ├── go.sum
│   ├── cmd/pax-core/                    # root StartedService + PaxCore
│   ├── cmd/pax-core-probe/              # 手动root验收客户端，direct-only
│   ├── cmd/pax-connectd/                # --user 默认装配；旧root/App-session兼容入口
│   ├── cmd/pax-mcp/                     # stdio入口；八组授权意图，不拉起App或daemon
│   ├── cmd/pax-mcp-launcher/            # 用户稳定入口与App显式安装/已有登记刷新
│   ├── cmd/pax-ai-usage-rebuild/        # 重建ai_usage.db（make ai-usage-rebuild）：备份、seed切换记录、同一流水线重扫、对账、替换
│   │   ├── main.go                     # flags、stores/runtime/IPC装配
│   │   └── user.go                     # home目录、socket查询、App owner identity/watch
│   ├── internal/core/                   # root RPC、受保护资源、watchdog、残留代理清理、只读归属
│   ├── internal/processidentity/        # core/用户paxd复用PID、UID、内核启动时间
│   ├── internal/coreclient/             # paxd 调 root core 的内部 RPC、内核身份与会话（只依赖 config 标准值）
│   ├── internal/controller/             # 产品状态机、操作队列、保护检查、错误解释
│   ├── internal/mcpserver/              # MCP schema/RPC映射、脱敏和有界查询续页
│   ├── internal/config/                 # 生成、校验、版本和回滚；routing_claude.go 提供内置 Claude 名单
│   ├── internal/importers/              # 链接/二维码/订阅/outbound → NodeProfile
│   ├── internal/singbox/                # 配置转换/StartedService/selector、指定线路熔断、无TUN测量
│   ├── internal/environment/            # 当前环境固定探针、真实浏览器静态采样页与短时loopback会话
│   ├── internal/measurement/            # 独立HTTP测量/脱敏/预算
│   ├── internal/network/                # 只读物理路径/路由归属与测量，无CAS或强关
│   ├── internal/cfoptimizer/            # CF候选与边缘测量
│   ├── internal/subfetch/               # 订阅 / 图标 HTTPS 取数与地址校验
│   ├── internal/siteicon/               # 在线对象图标的安全解码与私有缓存
│   ├── internal/aiusage/                # 本机CLI解析、按请求时间归属、分项计价、session_limits_*上限投影、ai_usage.db（v8，<5提示重建；5–7追加分析表、cursor首次补读）、单一写入流水线与Rebuild
│   ├── internal/aiprofile/              # AI 客户端 profile / CLI绑定 / profile_events.db切换时间线（Timeline）与重建用History
│   ├── internal/routing/                # rule-set 和规则匹配
│   ├── internal/telemetry/              # 产品事实转换
│   ├── internal/errorlog/               # 本机存储错误轮转sink
│   ├── internal/stats/                  # 5分钟桶聚合纯函数
│   ├── internal/historystore/           # SQLite I/O
│   ├── internal/storage/
│   ├── internal/ipc/                    # user.go单一用户路径规则、ListenUser；旧root登记保留
│   └── gen/proto/                       # 生成的 Go gRPC 类型
├── macos/
│   ├── PaxConnect.xcodeproj
│   ├── PaxConnect/                      # App target
│   │   ├── App/                         # AppDelegate、WorkbenchWindowController、MenuBarController、菜单
│   │   ├── Daemon/                      # PaxdLauncher/CoreLauncher/PowerEventForwarder、bundle校验
│   │   └── Resources/                   # Assets.xcassets/AppIcon（云团图标）、LaunchDaemon plist
│   └── Packages/
│       ├── PaxProto/                    # 纯生成物包，不设测试target；由PaxClient依赖编译
│       ├── PaxClient/                   # DaemonConnection、ConnectionSession、产品事件信封与 ClientError
│       ├── PaxCore/                     # ConnectionStore、DaemonSetupStore、NetworkDiagnosticHistory与系统服务协议
│       ├── PaxUI/                       # Theme、Components（PaxTabs独立文件）、PaxCharts、PaxMenuBarIcon 云团状态图标
│       └── Features/                    # 各页（Dashboard/Routes/RouteWorkbench（NetworkDiagnostic*环境摘要与主题卡片、Report实际证据报告）/RoutingWorkbench/LogsWorkbench/SettingsWorkbench/AIUsage/About）、Workbench共享壳、MenuBar（P06）
├── configs/                             # 默认配置和测试 fixture
├── rulesets/                            # 默认规则和测试 fixture
├── scripts/                             # package-release.sh / release-materials.py下载候选、create-release-dmg.py / dmg-background.swift安装界面、backup-release-data.py私有备份；构建；migrate-root-data.sh/.py、test-migrate-root-data.py
├── tests/                               # 真实宿主验收脚本；paxd-user/{MIGRATION,RUN,RESULTS}.md
└── third_party/licenses/
```

呈现语义定义在api/presentation.proto；config/product_text与controller/system_event_text／system_event_mapping承接事件语义，historystore/columns_text负责可选col_texts子表及冻结读，清理沿现有owner。environment/presentation、browser_texts.json与scripts/language-browser-catalog.py负责短会话网页词条，内置字典由App资源单源生成。跨端模型与旧诊断可选Codable字段在PaxClient，ProductTextPresentation和LogDetailPresentation在PaxCore；日志语言更新位于Features/LogsWorkbench/LogsInterfaceLanguage。

语言包沿PaxCore的LanguagePack／LanguageCatalog／StrictLanguageJSON／LanguagePackRepository／InterfaceLanguageStore；资源在Sources/PaxCore/Resources/LanguagePacks/{en,zh-Hans}按功能拆分。Features/SettingsWorkbench/InterfaceLanguageSettingsController提供本地语言与包操作，Workbench/InterfaceCopyBindings只更新显式产品文字；App/Info.plist声明语言与导入类型。定向测试LanguagePackTests，运行证据tests/interface-localization；范围与未完成项见architecture §19。

指定线路熔断沿`singbox/core_fixed_{health,routes,owner}.go`与`core_route_gate.go`，准入上游接口在`patches/sing-box/routegate/`；`telemetry/fixed_route_events.go`复用系统事件投影。定向回归在同包，真实网络、socket与实窗证据在`tests/fixed-route-breaker/`。

崩溃防逃逸1153已取消。`tests/crash-guard/seatbelt_probe.py`及`evidence/`保留本机机制验证；未实施草案归档至`docs/archive/2026-11-20-crash-guard-1153/`并登记归档索引，取消边界见`changes/pax-connect-network-control-plane/fixed-route-crash-guard.md`。

来源投影沿现有目录：config/traffic_identity.go 定义 AI CLI 路径证据和目标键；controller/traffic_identity_projection.go 只读纠正旧桶，traffic_exits.go 聚合真实出口；对应测试验证历史保守性与跨窗口守恒。

近期响应实现沿现有目录：controller/traffic_recent_responses.go负责冻结60条、列表尾20条投影；样本轴与选点走受影响图表的真实页面验收。

概览摘要柱绘制沿现有PaxUI包，`PaxStackedChart+Summary.swift`复用堆叠几何与共享读数；流量和模型趋势共用，四列排行仍由`PaxModelRankCard`值组件承接。

概览独立刷新沿现有包：`PaxCore/RefreshConsumer.swift` 管同页异步消费者在途合并，`Features/Workbench/WorkbenchDashboardObservation.swift` 管按依赖呈现与日历通知；五组读入口在 `WorkbenchDashboardHistory.swift`。`tests/dashboard-independent-refresh/` 保存票1103受控 App / socket 验收。

首次引导沿现有目录：Features/Onboarding承载共享提示与跨页接线，PaxCore/OnboardingStore只存引导偏好；aiprofile/initialize.go负责首次API补充，cmd/pax-connectd/initial_install.go在建库前冻结首次身份。

后台恢复：App/BackendRecovery.swift统一恢复任务；PaxClient/DaemonConnectionIdentity.swift集中Hello与归属校验；controller/backend_recovery.go在产品owner初始化时核对旧core，证据在tests/backend-recovery。

会话分析文件：aiusage/analysis{,_events,_test}.go、context_usage{,_test}.go、session_titles{,_database,_native,_test}.go、session_catalog{,_files,_scan,_query,_test,_claude_test,_preview_test}.go、session_preview.go、session_agents{,_parse,_test}.go、controller/ai_{analysis,session_catalog}.go与ai_session_preview_test.go；PaxClient/Model/AISession{Analysis,ContextUsage,Catalog}、PaxCore/AISession{Store,Metrics,CatalogStore}、Features/AIUsage/AISessionAnalysisView、AISessionContextTable、AISessionRequestChart、AISessionPickerController及PaxUI/PaxAnalysisChart{,Hover,Linked}；旧Charts/Comparison扩展已移除。合同见P07d，证据tests/ai-session-analysis；同代Watch/响应竞争回归沿AISessionTests，真实刷新验收见tests/ai-session-refresh-race。

会话迷你汇总沿现有目录：aiusage/session_limits_days.go负责本地日桶/同期，session_limits_summary_test.go覆盖口径，price_backfill.go与price_backfill_test.go负责启动补齐已有模型的旧缺价及桶/对比验收；Features/AIUsage/AISessionLimitsActivity.swift负责费用/主会话双列纯柱及Token/单位费用详情，AISessionLimitsDetail.swift负责hover，AISessionLimitsView/ListCard负责列表内搜索与详情返回，AISessionLimitsLoading.swift负责分区空壳与高度保留，AISessionPickerCell.swift保留旧目录行调用。验收见tests/ai-session-mini-cards。

AIUsage 已移除质量测试专用 AIQualityController.swift、AIQualityRun.swift、AIUsageSVGFixture.swift；Profile 编辑与用量详情沿现有组件。

实际连接归属：controller/history_route_identity.go沿routeData现有有界标识表保存运行版名称，history_ingest在journal入口冻结。定向回归history_route_identity_test.go覆盖编辑/移除与双库加工；tests/traffic-route-identity/repair以临时Go overlay复用正式聚合合并并封装离线SQLite维护，不加入产品入口或自动迁移。

列式日志库：historystore/columns*.go负责事实与有序子表，column_import/upgrade*.go负责v3→v4转换、gap记录与分批空间回收，column_projection*.go负责派生行I/O，column_snapshot*.go负责短读视图/有界旧版本，column_log_query/traffic_query*.go负责SQL筛选与分页；controller/history_column*.go调用生产投影并在现有owner调度升级，history_traffic_columns*.go接流量日志取材。config/log_gap.go统一断档纯规则，Logs.CapacityBytes在升级失败时排除冻结旧表。history_read_tokens.go与history_read_validity.go管理令牌与返回校验；history_connection_flow.go保留归属规则，history_activity_record.go保留展示投影，column_time_ranges.go处理查询时间边界。logs_v3_fixture_test.go仅生成升级夹具，history_projection_{oracle,filters}_test.go保留纯标准值投影对照。tests/log-store-columns/为208，log-store-switch/为209–210证据，不改原型保护区。

MCP客户端接入：internal/mcpclientconfig的clients/document/files负责配置检测、TOML/JSON/YAML字段合并与私有备份/外部变化拒写；cmd/pax-mcp-launcher/clients.go装配只读检查、接入和MCP入口探针。App的MCPInstaller持Process，Features的MCPAccessReport/Actions与MCPAccessSettingsController只做状态和显式意图；不新增协议或存储。合同见changes/pax-mcp/client-access.md。

MCP安装：internal/mcpinstall复用storage的私有原子记录，校验签名bundle/配套清单并定位socket；cmd/pax-mcp-launcher不持业务数据。App target的MCPInstaller把安装动作注入Features设置页，启动仅刷新已有登记。processidentity提供内核owner可执行路径；两MCP产物与依赖LICENSE/NOTICE由bundle-daemon.sh、mcp-notices.py打包，release-materials.py核对。

MCP设置：协议细则见architecture/settings-operations.md；mcpserver/settings.go与settings_schema.go负责六个正式设置工具、权限和独立owner版本；controller/settings_operations.go将原设置/日志owner接入公共请求结果，不加存储或任务框架。privacy.proto保存偏好消息，operation_context.proto解开共享结果依赖；SettingsOperationResult归operation.proto，PaxClient/DaemonSettingsOperation映射。tests/pax-mcp/settings_acceptance.py用隔离HOME、真实daemon/stdio/socket与SQLite回读；settings_fixture_integration_test.go仅生成隔离合成数据，不编入App。

MCP Profile：mcpserver/profiles.go与profiles_schema.go负责七个工具、权限、不含凭据的投影；controller/ai_profile_operations.go拥有跨RPC的目录/短写任务并接公共结果缓存。aiprofile仍唯一读写CLI/凭据与恢复journal；models.go在有界结果模式下于目录提交前检查完整workspace字节数，超限显式失败、不丢已有目录。tests/pax-mcp/profiles_acceptance.py与profile_fixture.py使用隔离HOME、真实stdio/socket及CLI文件回读。PaxClient/ProfileOperationMappingTests.swift覆盖目录失败与保存成功分离、计划与恢复结果。

MCP AI查询：mcpserver/ai_usage.go映射AIUsageService，ai_usage_schema.go与ai_usage_metadata.go定义schema、来源/费用/时区元数据；原reader事务分页水位归aiusage/query.go。tests/pax-mcp/ai_usage_acceptance.py保留隔离CLI采集、真实stdio/socket与正式App只读对账。

MCP网络工具：mcpserver/network.go负责受控RPC映射、依赖与typed结果脱敏；network_schema.go负责权限与schema；tests/pax-mcp/network_acceptance.py及first_permission_acceptance.py保留真实stdio/socket/浏览器与首次权限证据，network_evidence.py仅压缩有界回执。

MCP网络操作：controller/operation_network.go复用现有testOwner/pathDiagnostics异步诊断；operation_measurements.go保留批次逐项终态，不从当前目录反推。PaxClient/DaemonNetworkOperation.swift映射typed结果与可选进度；network_operations_test.go及NetworkOperationMappingTests.swift覆盖断线、取消、并发准入与字段存在性。

## 18. 测试与验证

适用范围、用例与验收入口见[验证合同](validation.md)。

CF的异步验证/冻结保存沿controller/cf_validation_operations.go，MCP四工具沿mcpserver/cf.go与cf_schema.go；不增包或存储。共享CF结果消息归operation.proto，Swift DaemonCFValidation与现有CFWorkspace在PaxClient映射。真实stdio/socket与落盘证据见tests/pax-mcp/1130-results.md。
