# 架构：sing-box 引入与 gRPC 边界（§7）

## 7. sing-box 引入与 gRPC 边界

### 7.1 集成目标

固定版本 sing-box Go module 由 root `pax-core` 内嵌，使用 `daemon.StartedService` 管理生命周期。paxd 生成配置，经 PaxCore 的 ApplyProfile 校验白名单后启动／reload；回直连调用 CloseService，core 进程保留。节点切换调用 SelectOutbound，再按老 outbound tag 在进程内关闭旧连接（spike 3 的 W3），不再采用代次／DNS 屏障切换。

core 只补充 App-PID watchdog、启动时残留 Pax 系统代理清理、配置白名单、root fd 归属识别、默认接口变化 CloseAll 和 Pause/Wake；TUN、系统代理、路由、DNS、出站由 sing-box 负责。代理关闭补丁仅替换上游DarwinSystemProxy.Disable：逐协议读取当前端点，仅关闭仍指向本实例的设置，保留第三方后来写入的端点，不恢复ON前快照。补丁在patches/sing-box，scripts/prepare-box-module.py按版本/源文件哈希准备忽略目录.tools/proxy-guard/sing-box，所有正式构建经scripts/go.sh；无全局代理恢复管理器。读取与关闭不是跨应用原子事务，同时写入的竞争仍需真机验证。构建需要 `with_clash_api`，不开放 HTTP controller 端口。App 的产品边界仍是 paxd 的 `api/pax.proto`；PaxCore 服务不是 App 公共协议，sing-box 类型不越过 Go 适配边界。

旧root/App-session CLI、Check/Probe/post-start、generation/DNS屏障、自建reconnect、系统代理CAS/journal/.m、appsession回执及第三方强关已删除。用户daemon只经core控制正式网络，singbox.Runtime仅保留无TUN测量/订阅实例。保留固定sing-tun路由保护（EEXIST拒绝替换、自有路由、按fd关闭）及Darwin代理关闭端点保护；未有上游等价证据，spike 4可选。§7.2–§7.4是历史嵌入说明，以本节及进程模型决策为准。

### 7.2 配置文件、内存实例和 sing-box gRPC

这三个概念必须分开：

```text
active.json / pending.json
  = Pax Connect 的持久化、校验、诊断和回滚材料

option.Options
  = 从配置内容解析出的 Go 配置对象

box.Box
  = 真正运行中的 sing-box 内存实例

StartedService 能力 / gRPC
  = sing-box 可选的运行时管理和观测接口；嵌入模式默认直接调用 Go 对象
```

CLI 模式可以从 JSON 文件读取配置，再创建 `box.Box`；嵌入模式直接由 `SingBoxAdapter` 把配置内容解析为 `option.Options` 并调用 `box.New`。两种模式都不是"gRPC 修改配置文件后 sing-box 自动监听文件"。嵌入模式默认直接调用 `StartedService` 对应的 Go 能力，不启动额外 gRPC server。

sing-box 的 `StartedService` proto 描述状态、日志、连接、测速和 outbound 等运行时接口；嵌入模式默认直接使用对应的 Go 对象，不启动额外 gRPC server。`StartOrReloadService(configContent)` 是 Go 内部方法，负责用新的配置内容创建或替换内存实例，不能把它误认为一个自动读写配置文件的 RPC。

### 7.3 嵌入模式的运行数据桥接

`box.New`、`Start` 和 `Close` 是公开的嵌入生命周期 API；日志、连接、outbound 和测速事件则主要由 sing-box 的 `StartedService`/运行时管理器维护。`SingBoxAdapter` 必须在 Go 进程内建立 telemetry bridge：优先复用 sing-box 已有的 `StartedService` 能力或公开运行时接口，必要时在 Pax Connect 维护小范围 fork/适配补丁，向 `TelemetryConsumer` 暴露稳定的内部事件模型。

这不是新增 Helper 进程，也不是让 App 连接 sing-box gRPC。它只是把 sing-box 的内部事实转换成 Pax Connect 的内部事件；上游没有公开的指标不能在业务层伪造，必须标记为待扩展或未归类。

### 7.4 三种"gRPC"必须分开

| 名称 | 作用 | 在 Pax Connect 中的定位 |
|---|---|---|
| Pax Connect gRPC | App 与 Go daemon 的产品控制协议 | 唯一的 App IPC；使用 `api/pax.proto` |
| sing-box `daemon.StartedService` 能力 / gRPC | sing-box 的运行时管理与观测接口 | 嵌入模式默认直接复用 Go 对象，不启动额外 gRPC server；独立调试或未来进程隔离时才使用 gRPC，不负责 Pax Connect 配置文件生命周期 |
| 节点传输 gRPC | 某个节点本身使用的传输方式 | 仅在节点配置声明时启用；不是 Pax Connect 控制协议 |

因此，用户导入一个带 gRPC 传输的节点时，Pax Connect 只把节点参数写入 sing-box 配置。用户不需要理解节点传输 gRPC，也不需要直接连接 sing-box 管理 gRPC。

### 7.5 Go module 版本策略

NetworkDiagnosticProgress是本轮临时计数/阶段标准值，measurement发布、controller拥有，proto与Swift同名映射；与历史报告分开，不落盘。合同见[实时进度](../changes/pax-connect-network-control-plane/network-diagnostics-progress.md)。

网络诊断标准值为measurement.NetworkDiagnosticReport / api.NetworkDiagnosticReport / Swift.NetworkDiagnosticReport，含逐源资料、独立提供者请求/备用证据、可选供应方评分、归因事实（供应方原始风险、保存策略、域名候选规则）和请求样本；仅singbox适配持有上游出站。当前环境另用EnvironmentDiagnosticReport / EnvironmentSample / EnvironmentCheck，daemon environment包持Mac实际网络与真实浏览器loopback短时会话；系统语言/时区保留App本机快照，不冒充浏览器；合同见[网络诊断](../changes/pax-connect-network-control-plane/network-diagnostics.md)。

后台恢复的跨端握手追加owner_app_pid，Go/Swift同提交同步；行为以[协议§4](protocol.md#4-ipc-与协议)及[恢复合同](../changes/pax-connect-network-control-plane/backend-recovery.md)为准。

AI Profile标准值由Go→proto→PaxClient映射，见P07。

首次引导新增Hello首次身份、AI绑定检测和初始化计划，Go/proto/Swift同步；合同见[首次引导](../changes/pax-connect-network-control-plane/first-run-onboarding.md)，不改变sing-box运行模型。

- 发布构建固定 sing-box tag 或 commit，并在 `go.mod` / `go.sum` 锁定依赖；当前核实基线为 commit `8a42af329c6f8cc2c43a142ea4eadecd5412bfce`、Go `1.24.7`。
- 本地联调可以临时使用：

  ```go
  replace github.com/sagernet/sing-box => <本机 sing-box 源码目录>
  ```

  该绝对路径不能进入发布合同、CI 或用户机器配置。
- 如果需要补丁，维护 Pax Connect 自己的 fork，记录上游 commit、补丁原因和 GPL 合规材料；不要复制 sing-box 源码到 `daemon/`。
- 构建阶段必须验证 macOS arm64、TUN 所需 entitlement 和网络权限、sing-box GPL 及其依赖许可证清单。
- `RouteGroup`、`NodeProfile`、`RuleSetMeta`、`RoutingDecision`、`ProtectionStatus`、`TrafficSnapshot` 和 `ProductLog` 是跨模块产品模型。
- 流量来源、冻结分页及出口/规则包分组以[分类合同](../changes/pax-connect-network-control-plane/traffic-source-categories.md)和[分组合同](../changes/pax-connect-network-control-plane/traffic-list-summary.md)为准；原始连接不新增PID/argv/委托链字段。
- 近期响应模型 `TrafficResponsePoint` 由既有 `FlowFact` 经 `FlowLatency` 投影，携带连接ID、开始时间、耗时；`TrafficRow` / `TrafficDetail` 独立附加近期样本与读取状态，不替换全期汇总字段。wire编号见 `api/traffic.proto`，冻结与保留合同见[统一延迟](../changes/pax-connect-network-control-plane/traffic-system-latency.md#e-查询保留与更新)。

会话分析跨端值`AISessionKey/Row/Analysis`对应Go `SessionKey/AnalysisRow/Analysis`与`AIUsageAnalysis*`协议消息。Analysis.Requests / proto requests / Swift requests为逐请求事实，RequestID / request_id / requestID稳定识别单次请求。Go SessionTitle / proto session_title / Swift sessionTitle为可选显示标题，不属于身份；独立目录SessionCatalogEntry/Query/Page对应AISessionCatalogEntry/Query/Page协议与Swift AISessionCatalogEntry/Page，携带名称来源、状态、最近活动和独立版本；Title/title为展示文字，TitleSource/title_source=claude.last-prompt或claude.user-message时为有界最近消息预览，状态仍untitled，不新增模型字段；目录查询From/To对应from_ms/to_ms，PaxClient接收Date边界，按原生活动或保留请求（含子代理）筛选，页携带目录/用量双版本及未关联子会话标记，不改变用量账本。PaxClient映射、PaxCore.AISessionStore管理读取，Features仅投影；新增Go `ContextUsage/ContextBand`、proto `AIUsageContextUsage/Band`、Swift `AISessionContextUsage/Band`，后台按日期/会话/模型/推理强度/单次输入档聚合输入双费用与去重轮次，主会话包含后代；ContextUsage同步Subagents/subagents、SubagentBands/subagent_bands、SubagentsIncomplete/subagents_incomplete，代理数按原生活动与请求去重，子代理输入双费用与Token独立小计。未知价格仅在分析显示缺测。见[P07d](../changes/pax-connect-network-control-plane/ui/specs/P07d-ai-sessions.md)。

会话上限投影`SessionLimits`（Go）/`AISessionLimits`（Swift）及对应LimitMoney、ModelLimit、LimitRow、LimitSummary、LimitTrend、LimitDay、LimitComparison由aiusage计算、PaxClient映射，proto见api/ai_usage.proto。正式页面消费daemon汇总，不从已加载日志重算；Features只生成展示百分比、已聚合均费、标签与图形。金额为输入侧USD，平均值保留亚微美元精度；分段已定价数与请求数独立，汇总/日桶/同期的InputTokens/input_tokens/inputTokens是含缓存的总输入Token，可选wire字段缺失为未知，已知0保留；日桶区分不可用与0，同期比较可缺失；合同见[上限视图](../changes/pax-connect-network-control-plane/ui/specs/P07d-ai-session-limits.md)。

AI本地用量跨端值：`AIUsageRequest`、`AIUsageTotals`、`AIUsageBucket` 定义在 aiusage / api/ai_usage.proto / PaxClient/Model/AIUsage；Features只投影。`AIUsageLogPage.state`映射已有日志响应state，用于失效代数核对，无wire字段变化。请求带官方分项单价/推理强度与来源，每条请求都有价格（无未定价/估算状态），DSH只读。性能可缺测，时间/Token与微美元费用在daemon冻结；合同见[AI采集](../changes/pax-connect-network-control-plane/ai-usage-collection.md) §3–7。

TrafficRoutingMatch是流量行可选的历史最近选择证据（at/rule/source/application_id/application/identity），Go/Swift与api/traffic.proto同步；config.FlowFact.RoutingMatchedAt在实际最终匹配冻结，通过既有内部JSON传递。累计不按包拆分，查询与历史降级见[最近命中](../changes/pax-connect-network-control-plane/latest-routing-match.md)。

手动代理配置沿用私有Document：socks/version=5、http、http+tls，认证不入NodeProfile；新增结果RouteSaveResult（routeID/created）经proto/Go/Swift对应映射。合同见[手动代理](../changes/pax-connect-network-control-plane/manual-proxy-routes.md)。

### 7.6 Geo 与 DNS 保护边界

- `RuleSetManager` 只管理规则集的来源、缓存和版本，不生产连接、测速或日志数据。
- `SingBoxAdapter` 将规则集、DNS 服务器、DNS 规则、TUN 接管和 route 规则转换为 sing-box 配置。
- `ProtectionChecker` 读取 sing-box 运行状态并执行少量保护校验，输出 `PROTECTED`、`DEGRADED` 或具体异常；它不替代 `TelemetryConsumer` 的实时事件消费。
- 加密 DNS 失败时，产品默认不静默降级到明文 DNS；TUN 失败时可以沿用系统代理降级（M2），但必须标记为 `DEGRADED`；MVP 中 TUN 失败保持关闭并提示原因。

IPv4-only网络：正式与测量共用的构建器使用ipv4_only解析策略，AAAA返回空答案；节点/双跳解析按冻结模式，IPv6字面量节点或CF入口明确不适用。TUN保留IPv6接管与reject边界，热规则更新保留地址族与AAAA约束。实际策略见[IPv4运行](../changes/pax-connect-network-control-plane/ipv4-only-network-plan.md)。

### 7.7 上游补丁与构建边界

- 上游补丁集中在 `patches/sing-box`（观测、观测质量、路由热更、DNS 热更 / 读者租约、Darwin 代理关闭归属）与 `patches/sing-tun`（路由保护）；每个补丁带源文件哈希清单，由 `scripts/prepare-box-module.py` / `prepare-tun-module.py` 校验上游版本与哈希后把依赖副本只准备到忽略目录 `.tools/`，不编辑全局模块缓存、不入库。升级内核必须复核补丁生命周期。
- 所有正式构建与测试经 `scripts/go.sh`；真实 TUN 构建带 `pax_tun_guard` tag，普通 `go build` 的二进制不具备 TUN 启动能力。
- 上游类型只允许出现在 `internal/singbox` 与补丁源码；观测数据以 Pax 标准值（`config` 的 Observation / FlowFact / HistoryEvent）穿过 core 边界，core 内部 RPC 只传有界 JSON 封套，App 只见 PaxClient 映射值。

### 7.8 测量实例（用户 paxd，无 TUN）

- `singbox.Runtime` 只持一个无入站的共享测量 Box 与共享 Network / Router / 日志环境；每个受理的测量任务克隆 service registry，创建自己的 OutboundManager、DNSRouter、DNSTransportManager，直接取得自己的 proxy 出站，不切任何 selector。普通 / CF / 两跳 / 订阅借道都走这一入口，正常上网线路与其它任务不受影响。
- 连接池、DNS 缓存与出站只存活于本任务；HTTP Transport 保持默认 keep-alive 语义，调用结束 `CloseIdleConnections`，绝不跨任务或线路长期共享连接。Close 先拒新任务、取消并排空，再关闭共享 Box。
- 产品并发 / 预算 / 配置 / 持久化归 controller 与 `internal/measurement`（只收注入的 HTTP Client 与标准值，不 import config / proto / singbox）；任务时限、出口查询池、下载预算与各项菜单语义唯一见 [route-measurements](../changes/pax-connect-network-control-plane/route-measurements.md)，本文不复述。

### 7.9 网络保护（DNS / WebRTC）

加密 DNS 分流为默认，严格 DNS 与公网 STUN 强制走代理为可选；设置经 controller 唯一队列 CAS 保存，下一次启用代理冻结，不自动断网。配置转换在 `config/dns_routing` 与 `singbox/dns_policy`、`dns_fallback`，规则表更新经 core 的 UpdateRouting 同步、保留实例与 TUN。唯一合同见 [network-privacy](../changes/pax-connect-network-control-plane/network-privacy.md)；配置不等于“无泄露”，真机验收见 tests/network-privacy。

流量来源投影与Geo匹配证据限制见[分类合同](../changes/pax-connect-network-control-plane/traffic-source-categories.md)；原FlowFact和路由策略不改。

规则编译到option.Rule必须解码到全新slice；禁止复用bootstrap slice造成未出现字段残留。历史579公网private标签的重放与验证见来源分类合同和GEO-ATTRIBUTION.md。

Geo下载、bundle离线基线和程序额外规则的唯一边界见[来源链](../changes/pax-connect-network-control-plane/geo-source-chain.md)；DNS用户规则优先级修复见network-privacy.md。

Geo基础包新增Google代理规则，先于CN直连；旧双源缓存缺Google时引用固定bundle基线，root只准入三种固定rule_set标签及哈希。下载/优先合同见geo-source-chain.md。

内置广告包沿既有 RoutingPackage 字符串 kind=adblock 投影，Go/Swift 同步；私有匹配 JSON 继续只由 daemon 编译，root 不开放新资源路径。状态与更新合同见[内置广告包](../changes/pax-connect-network-control-plane/adblock.md)。
