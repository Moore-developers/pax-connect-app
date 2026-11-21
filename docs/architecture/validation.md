# 架构：测试与验证

本文件是[layout-tests §18](layout-tests.md#18-测试与验证)的验证合同分册。

## 18. 测试与验证

列式日志的用户路径、数据集与性能门槛见[测试计划](../changes/pax-connect-network-control-plane/history-log-test-plan.md)。

| 层 | 内容 | 工具 |
|---|---|---|
| Go | 导入、规则优先级、配置队列、回滚、统计聚合、socket 鉴权 | `go test` |
| 协议 | proto 生成一致性、版本握手、`Watch` 断线续传 | Go 集成测试 + Swift 集成测试（连真 daemon） |
| PaxCore / PaxClient | Store 状态机、事件合并、断线保留快照、重复命令合并 | Swift Testing，假 `DaemonConnection` |
| UI | 本次行为涉及的关键页面与状态 | 真实窗口截图；离屏 `NSView` 渲染仅作布局自检，不引第三方快照库 |
| Features / App 交互 | 本次修改的开关、选线路、导入、筛选、窗口与生命周期路径 | 运行产物上的真实操作与关键截图，不设自动测试 |
| 真实宿主 | TUN、DNS hijack、IPv6、系统代理降级、睡眠唤醒、daemon 被杀后恢复 | `tests/` 脚本 + 手工检查单 |

PaxUI、Features 与 App 的自动测试组及 App 专用测试 manifest 已移除；Swift 自动测试保留 PaxClient / PaxCore，PaxProto 由依赖方编译与契约检查覆盖；Go 与静态检查保留。共享改动的真实验收覆盖受影响调用方；历史文档中的旧测试名、路径和命令只作当时证据，不是当前运行入口。

默认全量的专项排除项由 [Makefile](../../Makefile) 定义。下列定向入口仍可运行这些用例。

- 日志突发、空闲 TLS 重读和路由命中实窗夹具预检使用 `make test-go PKG=./internal/controller RUN='TestHistoryBurstRemainsQueryableAndBounded|TestIdleControllerDoesNotReparseCatalogTLSEverySecond|TestRoutingMatchFixtureRecordedScenarios'`。
- 重算错误提示、定位行顺序、图表时间和老用户引导使用 `make test-swift PKG=macos/Packages/PaxCore FILTER='AIRepriceTests/messages|LocationExposureTests/assessed|TrafficChartFormattingTests/connectionTime|OnboardingStoreTests/returningUsersAreNotForcedThroughWelcome'`。

工具专项由 `make check` 按改动选中。也可直接运行对应命令。

- 旧 root 数据迁移使用 `python3 -B scripts/test-migrate-root-data.py`。触发范围包括迁移脚本与迁移名单内的产品存储边界。
- 构建前退出使用 `python3 -B scripts/test-quiesce-build-app.py`。触发范围包括退出工具、构建、重启和发布备份入口。
- 代理验收探针使用 `python3 -B tests/proxy-session/test_probe.py`。触发范围包括探针、用例和验收脚本。

Web 原型（`ui/prototype`）保留作信息结构参考；原生视觉规范见 ui/design-system.md，不移植旧暖色 token。静态布局可用 AppKit 离屏渲染辅助比对；视觉与交互验收使用真实窗口，玻璃材质必须有真实窗口证据。

列表单行摘要合同：`docs/changes/pax-connect-network-control-plane/traffic-list-summary.md`。

流量详情出口标签：`macos/Packages/Features/Sources/Features/Workbench/WorkbenchTrafficExitTags.swift`；复用PaxTag代理/直连样式。

三视角来源与Geo证据合同：`docs/changes/pax-connect-network-control-plane/traffic-source-categories.md`。

规则包贡献：`daemon/internal/controller/traffic_rule_groups.go`，拆组合同见traffic-list-summary.md。

独立详情快照：controller/history_traffic_detail_read.go；SQLite清理后分页回归见traffic_detail_snapshot_test.go，实窗证据见tests/traffic-detail-snapshot。

明细交互状态：`WorkbenchConnectionListState.swift`；昨日/24h计数合同见traffic-detail-controls.md。

规则归因回归：`geo_rule_attribution_test.go`验证最终profile字段/匹配标签；`geo_rule_observation_test.go`贯穿隔离真实连接观测。

DNS 优先级由 `config/dns_routing_priority_test.go` 和 `singbox/dns_priority_observation_test.go` 覆盖。指定线路、App 归属和热更新见[票 1151 验收](../../tests/network-privacy/1151-results.md)。Geo 来源见 geo-source-chain.md。

Google资产rulesets/geosite-google.srs；真实重叠路由/DNS验证google_geo_test.go，旧缓存兼容及三源原子更新routing_geo_test.go；规则页走真实页面验收。

页面自动更新：RoutingLibraryModels.swift承载规则包编辑值类型，LogsFilterBar.swift承载日志筛选条。PaxCore的AIUsageRefreshStateTests与PaxClient的AIUsageLogStateMappingTests保留状态隔离及wire状态映射覆盖；页面失效、分页、阅读与原位更新按实际改动走真实验收。隔离实窗证据见tests/incremental-page-updates，controller的history_ui/ai_ui_fixture integration测试提供双历史库与AI样本，不接用户数据或TUN。

RouteWorkbenchActions.swift承载线路菜单、导入与批量测量动作，ViewController保留页面呈现；AI日志版本一致性回归见controller/ai_usage_generation_test.go。

IPv4运行：network/ipv6.go与ipv6_configuration_darwin.go读取/判定物理服务配置，config/address_family.go冻结私有运行策略；network/route_result_test、ipv6_test、controller/ipv4_diagnostics_test、singbox/ipv4_only_test覆盖适用地址族。真实证据见tests/ipv4-only-network。

最近命中实现沿既有config观测/格式化、controller历史聚合与stats分钟统计、PaxClient模型映射、PaxCore/Features列表详情；票114专属验收记录在tests/latest-routing-match。controller/traffic_latest_match.go负责冻结候选、同刻稳定比较与旧JSON未知边界；traffic_latest_metadata_test验证SQLite重启/JSON往返，traffic_latest_match_test验证单行RPC与冻结分页；PaxClient TrafficRoutingMatchMappingTests验证可选presence映射；PaxCore TrafficLatestMatchFormattingTests验证格式；Features列、说明与日志状态走真实页面验收。

阻断详情（票115）：telemetry事实合并保留明确blocked空出口；列式投影关联LastEventID保留原事件证据，查询按固定快照和截止时间使用，controller只纠正明确旧unknown拒绝副本，不回写历史。测试和验收见tests/blocked-traffic。

历史读取令牌合同见history-read-model.md §4；当前文件分工见本节列式日志库，旧票116证据见tests/history-read-model。

Codex身份：`internal/aiusage/parse_codex.go`解析白名单元数据，`codex_evidence.go`索引证据，`write_codex.go`与既有writer同事务去重/晚模型修桶，`rebuild_codex.go`保留退役marker，`codex_owner{,_test}.go`处理子文件身份/旧游标补读与同事务归属校正；合同见[Codex身份](../changes/pax-connect-network-control-plane/codex-usage-identity.md)，隔离验收`tests/codex-usage-identity/`。

手动代理：`api/operation.proto`独立命令结果合同；controller/route_add.go负责新增编排；PaxCore/ProxyRouteDraft.swift负责表单值与单条解析，RoutesStoreProxy.swift提供保存结果；Features/RouteWorkbench的RouteProxyForm/Controller、RouteWorkbenchProxy与RouteDetailConfiguration复用新增/编辑。PaxUI/PaxProxyRouteTheme集中尺寸；tests/manual-proxy-routes保存真实验收。

设置：Features/SettingsWorkbench四控制器，合同P05。

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

CF顶部显式测试回归沿controller/cf_displayed_tests_test.go覆盖旧/空/失败扫描后的延迟、地区、下载与落盘；真实验收见tests/cf-explicit-tests/。

测试截图按tests/SCREENSHOTS.md保留最新功能批次；旧图片从清理前Git提交取回，文字验收结论保留。
