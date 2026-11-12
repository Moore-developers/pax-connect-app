# 架构：目录结构与测试（§16、§18）

## 16. 目录结构

发布入口：AGENTS.md指向docs/release/WORKFLOW.md；BUILD.md定义本地打包。scripts/public-allowlist.txt包含导出所需的release安装/版本材料，私有执行记录留在tests/release/。

术语入口为根GLOSSARY.md；网络观测补充定义按职责拆在docs/glossary/network-observation.md。



```text
pax-connect/
├── api/
│   ├── pax.proto                        # App ↔ 后台产品 gRPC
│   ├── pax_core.proto                   # paxd ↔ root core 内部 gRPC，仅Go
│   └── ai_profiles / ai_usage / network_diagnostics / operation / privacy / route_exit / route_usage / traffic .proto   # 按族拆出的产品 gRPC 消息
├── patches/{sing-box,sing-tun}/          # 固定版本上游补丁（观测、路由/DNS热更、代理关闭归属、路由保护）及无root回归
├── daemon/
│   ├── go.mod                           # 锁定 sing-box Go module
│   ├── go.sum
│   ├── cmd/pax-core/                    # root StartedService + PaxCore
│   ├── cmd/pax-core-probe/              # 手动root验收客户端，direct-only
│   ├── cmd/pax-connectd/                # --user 默认装配；旧root/App-session兼容入口
│   ├── cmd/pax-ai-usage-rebuild/        # 重建ai_usage.db（make ai-usage-rebuild）：备份、seed切换记录、同一流水线重扫、对账、替换
│   │   ├── main.go                     # flags、stores/runtime/IPC装配
│   │   └── user.go                     # home目录、socket查询、App owner identity/watch
│   ├── internal/core/                   # root RPC、受保护资源、watchdog、残留代理清理、只读归属
│   ├── internal/processidentity/        # core/用户paxd复用PID、UID、内核启动时间
│   ├── internal/coreclient/             # paxd 调 root core 的内部 RPC、内核身份与会话（只依赖 config 标准值）
│   ├── internal/controller/             # 产品状态机、操作队列、保护检查、错误解释
│   ├── internal/config/                 # 生成、校验、版本和回滚
│   ├── internal/importers/              # 链接/二维码/订阅/outbound → NodeProfile
│   ├── internal/singbox/                # 配置转换/StartedService/selector、无TUN测量
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


来源投影沿现有目录：config/traffic_identity.go 定义 AI CLI 路径证据和目标键；controller/traffic_identity_projection.go 只读纠正旧桶，traffic_exits.go 聚合真实出口；对应测试验证历史保守性与跨窗口守恒。

近期响应实现沿现有目录：controller/traffic_recent_responses.go负责冻结60条、列表尾20条投影；样本轴与选点走受影响图表的真实页面验收。

概览摘要柱绘制沿现有PaxUI包，`PaxStackedChart+Summary.swift`复用堆叠几何与共享读数；流量和模型趋势共用，四列排行仍由`PaxModelRankCard`值组件承接。

概览独立刷新沿现有包：`PaxCore/RefreshConsumer.swift` 管同页异步消费者在途合并，`Features/Workbench/WorkbenchDashboardObservation.swift` 管按依赖呈现与日历通知；五组读入口在 `WorkbenchDashboardHistory.swift`。`tests/dashboard-independent-refresh/` 保存票1103受控 App / socket 验收。

首次引导沿现有目录：Features/Onboarding承载共享提示与跨页接线，PaxCore/OnboardingStore只存引导偏好；aiprofile/initialize.go负责首次API补充，cmd/pax-connectd/initial_install.go在建库前冻结首次身份。

后台恢复：App/BackendRecovery.swift统一恢复任务；PaxClient/DaemonConnectionIdentity.swift集中Hello与归属校验；controller/backend_recovery.go在产品owner初始化时核对旧core，证据在tests/backend-recovery。

会话分析文件：aiusage/analysis{,_events,_test}.go、context_usage{,_test}.go、session_titles{,_database,_native,_test}.go、session_catalog{,_files,_scan,_query,_test,_claude_test,_preview_test}.go、session_preview.go、session_agents{,_parse,_test}.go、controller/ai_{analysis,session_catalog}.go与ai_session_preview_test.go；PaxClient/Model/AISession{Analysis,ContextUsage,Catalog}、PaxCore/AISession{Store,Metrics,CatalogStore}、Features/AIUsage/AISessionAnalysisView、AISessionContextTable、AISessionRequestChart、AISessionPickerController及PaxUI/PaxAnalysisChart{,Hover,Linked}；旧Charts/Comparison扩展已移除。合同见P07d，证据tests/ai-session-analysis；同代Watch/响应竞争回归沿AISessionTests，真实刷新验收见tests/ai-session-refresh-race。

会话迷你汇总沿现有目录：aiusage/session_limits_days.go负责本地日桶/同期，session_limits_summary_test.go覆盖口径，price_backfill.go与price_backfill_test.go负责启动补齐已有模型的旧缺价及桶/对比验收；Features/AIUsage/AISessionLimitsActivity.swift负责费用/主会话双列纯柱及Token/单位费用详情，AISessionLimitsDetail.swift负责hover，AISessionLimitsView/ListCard负责列表内搜索与详情返回，AISessionLimitsLoading.swift负责分区空壳与高度保留，AISessionPickerCell.swift保留旧目录行调用。验收见tests/ai-session-mini-cards。

AIUsage 已移除质量测试专用 AIQualityController.swift、AIQualityRun.swift、AIUsageSVGFixture.swift；Profile 编辑与用量详情沿现有组件。

## 18. 测试与验证

| 层 | 内容 | 工具 |
|---|---|---|
| Go | 导入、规则优先级、配置队列、回滚、统计聚合、socket 鉴权 | `go test` |
| 协议 | proto 生成一致性、版本握手、`Watch` 断线续传 | Go 集成测试 + Swift 集成测试（连真 daemon） |
| PaxCore / PaxClient | Store 状态机、事件合并、断线保留快照、重复命令合并 | Swift Testing，假 `DaemonConnection` |
| UI | 本次行为涉及的关键页面与状态 | 真实窗口截图；离屏 `NSView` 渲染仅作布局自检，不引第三方快照库 |
| Features / App 交互 | 本次修改的开关、选线路、导入、筛选、窗口与生命周期路径 | 运行产物上的真实操作与关键截图，不设自动测试 |
| 真实宿主 | TUN、DNS hijack、IPv6、系统代理降级、睡眠唤醒、daemon 被杀后恢复 | `tests/` 脚本 + 手工检查单 |

PaxUI、Features 与 App 的自动测试组及 App 专用测试 manifest 已移除；Swift 自动测试保留 PaxClient / PaxCore，PaxProto 由依赖方编译与契约检查覆盖；Go 与静态检查保留。共享改动的真实验收覆盖受影响调用方；历史文档中的旧测试名、路径和命令只作当时证据，不是当前运行入口。

Web 原型（`ui/prototype`）保留作信息结构参考；原生视觉规范见 ui/design-system.md，不移植旧暖色 token。静态布局可用 AppKit 离屏渲染辅助比对；视觉与交互验收使用真实窗口，玻璃材质必须有真实窗口证据。

列表单行摘要合同：`docs/changes/pax-connect-network-control-plane/traffic-list-summary.md`。

流量详情出口标签：`macos/Packages/Features/Sources/Features/Workbench/WorkbenchTrafficExitTags.swift`；复用PaxTag代理/直连样式。

三视角来源与Geo证据合同：`docs/changes/pax-connect-network-control-plane/traffic-source-categories.md`。

规则包贡献：`daemon/internal/controller/traffic_rule_groups.go`，拆组合同见traffic-list-summary.md。

独立详情快照：controller/history_traffic_detail_read.go；SQLite清理后分页回归见traffic_detail_snapshot_test.go，实窗证据见tests/traffic-detail-snapshot。

明细交互状态：`WorkbenchConnectionListState.swift`；昨日/24h计数合同见traffic-detail-controls.md。

规则归因回归：`geo_rule_attribution_test.go`验证最终profile字段/匹配标签；`geo_rule_observation_test.go`贯穿隔离真实连接观测。

DNS优先级：config/dns_routing_priority_test.go及singbox/dns_priority_observation_test.go；Geo来源合同geo-source-chain.md。

Google资产rulesets/geosite-google.srs；真实重叠路由/DNS验证google_geo_test.go，旧缓存兼容及三源原子更新routing_geo_test.go；规则页走真实页面验收。

页面自动更新：RoutingLibraryModels.swift承载规则包编辑值类型，LogsFilterBar.swift承载日志筛选条。PaxCore的AIUsageRefreshStateTests与PaxClient的AIUsageLogStateMappingTests保留状态隔离及wire状态映射覆盖；页面失效、分页、阅读与原位更新按实际改动走真实验收。隔离实窗证据见tests/incremental-page-updates，controller的history_ui/ai_ui_fixture integration测试提供双历史库与AI样本，不接用户数据或TUN。

RouteWorkbenchActions.swift承载线路菜单、导入与批量测量动作，ViewController保留页面呈现；AI日志版本一致性回归见controller/ai_usage_generation_test.go。

IPv4运行：network/ipv6.go与ipv6_configuration_darwin.go读取/判定物理服务配置，config/address_family.go冻结私有运行策略；network/route_result_test、ipv6_test、controller/ipv4_diagnostics_test、singbox/ipv4_only_test覆盖适用地址族。真实证据见tests/ipv4-only-network。

最近命中实现沿既有config观测/格式化、controller历史聚合与stats分钟统计、PaxClient模型映射、PaxCore/Features列表详情；票114专属验收记录在tests/latest-routing-match。controller/traffic_latest_match.go负责冻结候选、同刻稳定比较与旧JSON未知边界；traffic_latest_metadata_test验证SQLite重启/JSON往返，traffic_latest_match_test验证单行RPC与冻结分页；PaxClient TrafficRoutingMatchMappingTests验证可选presence映射；PaxCore TrafficLatestMatchFormattingTests验证格式；Features列、说明与日志状态走真实页面验收。

阻断详情（票115）：telemetry事实合并保留明确blocked空出口；historystore的ConnectionSnapshot关联LastEventID仅读取同快照原事件证据，controller只纠正明确旧unknown拒绝副本，不回写历史。测试和验收见tests/blocked-traffic。

等价历史读取（票116）：`historystore/logs_changes.go`提供提交后增量与短读取屏障；`controller/history_log_model.go`发布共享不可变版本，`history_activity_model.go`缓存按连接/独立事件归组的生产摘要，`history_model_budget.go`计量当前及令牌引用版本。`history_log_detail_read.go`保留事件/原文按需读取；`history_log_model_test.go`验证字节版本、删除迟到、超旧预算分页、发布恢复、deadline与DNS分页。合同见history-read-model.md，真实差分与性能证据见tests/history-read-model。

Codex身份：`internal/aiusage/parse_codex.go`解析白名单元数据，`codex_evidence.go`索引证据，`write_codex.go`与既有writer同事务去重/晚模型修桶，`rebuild_codex.go`保留退役marker，`codex_owner{,_test}.go`处理子文件身份/旧游标补读与同事务归属校正；合同见[Codex身份](../changes/pax-connect-network-control-plane/codex-usage-identity.md)，隔离验收`tests/codex-usage-identity/`。

手动代理：`api/operation.proto`独立命令结果合同；controller/route_add.go负责新增编排；PaxCore/ProxyRouteDraft.swift负责表单值与单条解析，RoutesStoreProxy.swift提供保存结果；Features/RouteWorkbench的RouteProxyForm/Controller、RouteWorkbenchProxy与RouteDetailConfiguration复用新增/编辑。PaxUI/PaxProxyRouteTheme集中尺寸；tests/manual-proxy-routes保存真实验收。

关于页沿Features/About组织，复用系统设置分组卡显示版本与许可摘要；专用尺寸在PaxUI/PaxAboutTheme。

内置广告包：rulesets/adblock-lite.srs 与 manifest/许可 NOTICE；config/routing_adblock.go 定义身份，controller/routing_adblock.go 初始化与域名源边界；Features/RoutingWorkbench 的 RoutingPackagePresentation.swift、RoutingPackageUpdates.swift 分别维护包文案与更新设置。定向测试在 controller/singbox 的 routing_adblock_test.go，真实验收 tests/adblock。

官方账号来源：aiprofile/sources.go与default_api.go维护认证来源事件和默认API局部切换；aiusage/source_migration.go拥有schema12来源维度迁移；PaxClient/Model/AIServiceSource.swift映射来源。归属合同从ai-usage-collection §4进入ai-usage-attribution.md。验收见tests/ai-service-sources。

模型用量三表复用PaxDataTable及PaxPresentation中的PaxText.tableLabel；PaxUI/PaxFilterRow.swift承载筛选行与范围输入排版，业务过滤与校验留在Features/AIUsage。

网络诊断证据：environment/evidence.go持固定目标检查与输入白名单，browser.js为真实浏览器有界采样，retry.go只重试确切IP资料；PaxCore/EnvironmentDiagnosticEvidence.swift投影受控原因与分项读值，NetworkDiagnosticWorkflowTests覆盖全模式/局部更新/旧JSON及导出。

诊断判断投影：PaxCore/NetworkDiagnosticAssessment.swift负责服务/定位/风险摘要，EnvironmentDiagnosticAssessment.swift负责环境对照/出口判断；Features复用单行详情展开。验收见NetworkDiagnosticAssessmentTests与tests/network-diagnostics/assessment-1109.md。

独立诊断资料：measurement/intelligence{,_http,_parse}.go复用ExitLookup调度并保留逐源标准值，environment/intelligence.go合并同会话同IP请求；PaxCore/NetworkDiagnosticEvidence.swift投影来源/备用/风险计数。协议仍在api/network_diagnostics.proto；验收见票157。

诊断实时进度：measurement/diagnostic_progress.go发布实际阶段/计数，controller/network_diagnostic_progress.go拥有最近一轮；PaxCore/NetworkDiagnosticActivity.swift区分当前进度与历史，Features/RouteWorkbench/NetworkDiagnosticProgressView.swift及NetworkDiagnosticProgressRow.swift负责固定页头、三列状态与显示寿命；PaxUI/PaxActivityIndicator.swift供状态区和PaxRouteButton.swift共用转圈，后者从PaxRouteControls.swift按按钮职责独立。App/DiagnosticBrowserOpener.swift拥有默认浏览器后台打开及短时焦点保持。验收见NetworkDiagnosticProgressTests和票160。

会话上限：aiusage/session_limits_*负责只读预设/查询/投影，controller/ai_limits.go映射协议；PaxClient/Model/AISessionLimits与Features/AISessionLimitsMapping隔离协议和展示；AISessionLiveLimitsView复用目录与Store；AISessionLimitsReadout为此页比例条与问号共用原生悬浮面板。tests/ai-session-limits包含socket/SQLite对账和PaxSparkline真实数据栅格化重放；PaxProportionView内迷你图仅按像素保留峰谷，不改原始数据。

模型用量概览时间桶由Features/AIUsage/AIUsageDataProjection投影；PaxUI/PaxStackedChart+Categorical承载可选最少时间格、右轴平滑数列与对齐hover，PaxCharts保留共享状态/事件；Features/AIUsage/AIUsageTrendReadout投影结构化读数，PaxUI/PaxChartStructuredReadout在共享popover复用H1卡。合同见P07c「趋势时间与单位费用」，不改存储与RPC。
