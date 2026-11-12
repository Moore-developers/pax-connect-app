# 架构：Go daemon 模块、数据流与外部依赖（§5、§6、§8、§9）

## 5. Go daemon 内部模块与边界



| 模块 | 职责 | 对外接口 | 主要依赖 |
|---|---|---|---|
| AppKit Features | 展示状态和发出用户意图 | 各领域 Store、导航 | AppKit、PaxUI |
| GRPC Client | 调用命令、订阅快照和事件 | Pax Connect gRPC | 生成的 Swift client |
| Controller | 产品状态机、操作队列、策略、保护状态和错误解释 | Pax Connect gRPC Server | storage、network、routing、singbox、telemetry |
| Importers | 统一解析节点来源 | `NodeProfile` | 协议解析器、二维码/订阅输入 |
| RuleSetManager（`routing` 内部组件） | Geo rule-set 下载、缓存、版本校验、更新和回滚 | `RuleSetMeta` | HTTPS、本地缓存、SQLite |
| Routing | 规则匹配、优先级和命中解释 | `RoutingDecision` | RuleSetManager、域名/IP、应用信号 |
| TelemetryConsumer | 消费 sing-box 自动产生的状态、测速、日志、连接和 outbound 数据 | 内部事件流 | sing-box runtime/adapter |
| Stats | 消费连接事件并按线路、应用、域名/IP、规则、上传/下载聚合 | `TrafficSnapshot` | TelemetryConsumer |
| Network / ProtectionChecker | TUN、系统代理、DNS、快照恢复，以及 TUN/DNS/IPv6 保护检查 | `ProtectionStatus` | macOS 网络能力、TUN 权限、sing-box runtime |
| SingBoxAdapter | 隔离配置/生命周期/观测；[v1.14.3迁移](../changes/pax-connect-network-control-plane/kernel-1.14.3-upgrade.md) | 内部 adapter | 固定内嵌版本 |

- 依赖方向：AppKit → Pax Connect gRPC → Controller/业务模块 → SingBoxAdapter → sing-box Go API/runtime。
- 禁止 App 直接引用 sing-box 类型、执行 shell、写系统网络配置或解析完整 sing-box JSON。
- 禁止将 sing-box 的 gRPC API、proto 或内部对象作为 Pax Connect 的公共 API；两者即使都使用 gRPC，也必须保持协议、生命周期和错误模型隔离。

### 5.1 状态 owner 与持久化

- **产品状态**：用户 paxd 内只有 `controller` 的串行 owner（`ConfigureOperations` 队列）写目录、偏好、来源与代理意图；任务进度（`testOwner`）只是短寿命投影，不写入已保存的测量事实；observer 回传不可变值且在池锁外执行，不得锁内等待产品回调。
- **私有文件**：产品数据文件统一经 `storage.PrivateFile`（受保护路径 / owner / 权限、有界字节、同步 + 原子替换）；纯产品 schema 在 `config` 校验，损坏文件不被空文件覆盖，写入故障与“提交后不确定”分别向 App 公布。目前的文件及唯一写入方：
  - `route-measurements.json`（`config/measurement_store.go`）、`route-history.json` 与 `route-usage.json`（`controller/route_data.go`）：线路测量最近结果、线路历史样本、线路用量；
  - `measurement-preferences.json`、`network-privacy.json`、`routing-library.json`、`cf-evidence.json`：测量偏好、网络保护设置、规则包库、CF 证据，各由 controller 对应 owner 单写；
  - `physical_egress_cache`（`config/physical_egress_cache.go`）：本机出口缓存，仅物理观察线程写；
  - 日志 / 流量历史在分库 SQLite（`internal/historystore`，每库单写协程，proxy 操作只非阻塞提交事件），模型与保留期见 [history-storage](../changes/pax-connect-network-control-plane/history-storage.md)。
- **调度与历史链路**：循环统一用 `internal/cadence`（声明间隔、唤醒、运行条件、是否有变化；业务间隔集中在 `intervals.go`）。采集每 5 s 读 core，空读取不写 journal、不改版本号：它只延长内存里的采集区间，区间在满 60 s、状态切换、停顿超过 8 s 或随下一批数据落成一行 `poll{from_ms,at_ms}`，作为流量覆盖证明（崩溃最多丢未落区间）。入库有新行即唤醒日志整理（60 s 仅兜底，新日志约 6 s 内可查）；日志 / 流量整理仅在元数据变化时通知发布；发布每秒最多一次。流量过期清理与容量淘汰与 `traffic_revision` 同事务推进 `traffic_deletion_generation`。自动测延迟 30 s 一点，新点与裁剪推进 `route_history_revision`。代理循环的 1 s 刷新只在测速 / 下载 / 出口检测或唤醒后跟进进行时才有定时器，不再有 checkpoint 定时。版本号规则见 [protocol §4.2](protocol.md)。
- **AI会话分析**：aiusage以schema9追加独立原生目录、schema10追加子会话标记、schema11追加原生代理/活动日期/请求归属并只重读目录元数据，保留轮次/请求关联表与Analyze读取，首次旧cursor补读元数据仍沿账本去重；原生事件确认轮次/压缩，采集增量索引Codex/Claude/DeepSeek原生身份与名称，分析查询只读目录投影，按日期分档的上下文用量在同一读事务聚合，含全范围/单会话合计与模型×推理强度行，主会话包含子代理且全局请求只计一次，轮次跨模型去重。正式上限投影由session_limits_*在同一读事务现算，不迁移存储；DeepSeek历史基准与行内比较按同模型/峰谷分组，复用计价时段规则；合同见[上限视图 §7](../changes/pax-connect-network-control-plane/ui/specs/P07d-ai-session-limits.md#7-数据接入)。旧Analyze固定分档保留兼容。Claude代理类型名只取旁侧元数据agentType并缓存于现有目录文件状态，变名只推进目录版本；不存描述/正文。

- **AI本地用量**：`internal/aiusage` 独立拥有只读CLI日志扫描、解析和 `ai_usage.db` 单写；aiprofile 的应用/来源观测时间线（每次扫描前检查配置并取快照，按请求时间归属）通过cmd注入；schema12新增小时桶来源维度，旧账本不重标，见[来源合同](../changes/pax-connect-network-control-plane/ai-service-sources.md)；`cmd/pax-ai-usage-rebuild` 是重新处理入口；Open仅补保留期内已收录模型的无单价行并同步小时桶，保留历史归属/倍率，见AI采集§5；daemon遇<5旧结构只报`ai_usage_rebuild_required`；5/6仅原事务追加兼容表和索引至7，不重读历史。controller仅映射三RPC，App不读源日志。合同见[AI采集](../changes/pax-connect-network-control-plane/ai-usage-collection.md)。
- **地址族事实**：network读取当前物理服务的SystemConfiguration配置、地址和作用域路由；IPv6关闭/仅本地链路/不可用/可用/未知分别呈现。物理网络身份含有效地址族状态，Pax自建TUN不参与判定。私有RouteConfig冻结IPv4Only，不写节点持久化。
- **环境检测**：controller编排独立45秒environment会话，固定loopback单次浏览器回传及Mac实际网络探测；不绑定所选线路、不落盘。
- **网络诊断**：controller在队列外编排45秒显式查询，复用测试owner名额与寿命，measurement解析归因资料，singbox持冻结测量出站；无持久化或系统修改。见[合同](../changes/pax-connect-network-control-plane/network-diagnostics.md)。
- **只读采集**：`internal/network` 只做只读物理路径 / 路由归属采集，不写系统网络；controller 在队列外有界执行诊断并复核会话边界。
- **子包边界**：`coreclient`、`measurement`、`cfoptimizer`、`siteicon`、`subfetch` 等包的依赖与职责见 [code-standards/module-boundaries.md](../code-standards/module-boundaries.md)，不在此重复。

历史查询的共享不可变读模型、提交版本、按需响应/详情及删除撤销规则见[等价历史读取](../changes/pax-connect-network-control-plane/history-read-model.md)。业务字段与统计/筛选/排序不变，内存模型可从SQLite重建，不新增第二套持久化权威。

首次使用合同见[引导](../changes/pax-connect-network-control-plane/first-run-onboarding.md)。启动建库前冻结首次身份；aiprofile区分missing/account/invalid/ready，首次API创建复用Plan/Apply与恢复日志。新文件排他创建，不覆盖途中外部新文件。

## 6. 运行流程与数据流

- 启动：App 连接 Pax Connect daemon → 获取完整快照 → Controller 生成/读取有效配置 → SingBoxAdapter 解析配置并创建内存中的 `box.Box` → 在进程内消费状态、测速、日志、连接和 outbound 数据。
- 开启智能代理：Controller 校验配置并调用 RuleSetManager 加载有效规则，再调用 SingBoxAdapter 的 `Start`；sing-box 自动启动 TUN、DNS、路由、线路组和测速；ProtectionChecker 检查实际接管结果，Controller 发布 `PROTECTED` 或 `DEGRADED` 产品状态。
- 分流：Pax Connect 生成优先级明确的 sing-box route 和 DNS 配置；sing-box 负责匹配和执行。用户规则顺序、默认策略与生效边界唯一见 [分流优先级](../changes/pax-connect-network-control-plane/decisions/routing-precedence.md)，规则包维护见 [Geo 决策](../changes/pax-connect-network-control-plane/decisions/geo-dns-protection.md)。2026-10-06 确认的是产品设计，真实能力仍按 §19 冻结。
- 统计：sing-box 产生连接和上下行事件；`TelemetryConsumer` 异步消费，`Stats` 按线路、应用、域名/IP、规则和上传/下载聚合后写入 SQLite；无法归属的流量标记未归类但仍计入总量和线路流量。
- 日志：sing-box 产生原始日志；`TelemetryConsumer` 订阅后分类、限流和写入日志库，保留 1/12/24 小时（见history-storage）；Dashboard 只读取产品化后的日志。
- 关闭：Pax Connect 调用 SingBoxAdapter 的 `Close`，核对本实例TUN/路由清理并发布结果；系统代理快照恢复属M2。
- 状态保存：线路、分组、订阅和 active/pending/history 配置由 Pax Connect 保存；这些文件不等于运行实例；日志（1/12/24 小时）与流量聚合（30 天）存于分库 SQLite，见 history-storage。

### 6.1 数据生产与消费边界

```text
sing-box 产生：测速、连接、上下行、命中规则、应用信息、原始日志、核心状态
Pax Connect 控制：生成 DNS/TUN/route/rule-set 配置，检查保护状态和规则集版本
Pax Connect 消费：接收 sing-box runtime 事件、异步聚合、写入 SQLite、生成产品快照和错误解释
App 消费：只读取 Pax Connect 产品 gRPC 的 `Watch` 事件流和 `Query*` 分页，不读取原始 sing-box 数据
```

线路探测统一经 singbox adapter 的真实出站执行，不在 App 或其他包重复实现代理协议，也不伪造 sing-box 原始日志。2026-10-13 已确认的 CF 独立模块负责物理出口到 CF 边缘的候选发现、TCP 精测与显式下载筛选；这是入口证据，不是代理线路测速。controller 再通过 singbox 验证具体线路的适用性。模块和本期无自动巡检的范围见当前线路接入规划。

## 8. 接口与外部依赖

| 名称 | 类型 | 用途 | 协议或配置入口 | 兼容要求 |
|---|---|---|---|---|
| Pax Connect gRPC | 内部 API | App/daemon 产品控制面和保护状态快照；单条 `Watch` 事件流 + `Query*` 分页 | Unix Socket + Pax Connect Protobuf；`PaxService` 的 `GetSnapshot` 包含保护、DNS、IPv6 和 active rule-set 状态 | 不暴露 sing-box proto |
| sing-box `StartedService` 能力 / gRPC | sing-box 可选管理能力 | 状态、日志、连接、outbound 和测速 | 嵌入模式直接调用 Go 对象；需要时才启用 gRPC | 不暴露给 App |
| 节点传输 gRPC | 节点数据传输 | 某条 VLESS/VMess 线路的 gRPC stream | 由节点 outbound 配置生成 | 不作为控制协议 |
| 节点链接 | 外部输入 | VLESS/VMess/Trojan/SS/SOCKS/HTTP 导入 | 分享链接 | 首版支持清单 |
| 订阅 URL | 外部输入 | 线路集合和分组 | HTTPS | 刷新失败保留旧版本 |
| 二维码 | 外部输入 | 节点链接导入 | 图像解码 | 内容必须落入支持清单 |
| sing-box outbound JSON | 外部输入 | 单线路导入 | JSON | 不导入完整配置 |
| Geo / rule-set | 外部数据 | 默认分流 | HTTPS 下载、本地缓存和版本校验 | 更新失败保留上一版 |
| 加密 DNS | 外部服务 | DNS 解析和保护 | DoH/DoT/DoQ，由 sing-box 配置 | 不默认降级明文 DNS |

## 9. 关键约束

- 安全：Unix Socket 限制权限并校验对端 uid；订阅和 rule-set 使用 HTTPS；密钥不写普通日志；加密 DNS 失败不静默降级明文；系统网络恢复必须幂等。
- 性能：sing-box 负责实时产生底层事件；Go 侧使用有界队列异步消费并批量写入 SQLite；首页图表 60~180 点；原始日志限流并保留 1/12/24 小时；配置操作串行化。
- 数据保留：日志与连接明细保留 1/12/24 小时（默认 24），流量聚合保留 30 天，见 history-storage；清理任务不得阻塞 runtime 事件消费。
- 可靠性：快照版本、事件 `seq`、断线重连、last-known-good 配置和 rule-set 回滚；保护检查失败不得误报为已保护。
- 兼容限制：首版不承诺 v2rayN 全协议无边界兼容，只支持明确协议和导入格式清单。
- 相关文档：`docs/product.md`、`docs/technical.md`、`docs/changes/pax-connect-network-control-plane/spec.md`。
