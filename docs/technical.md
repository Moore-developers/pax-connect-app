# Pax Connect 技术设计文档

版本：`0.2`

## 1. 技术目标

Pax Connect 是一个个人 macOS 桌面应用，目标是让用户通过一个开关获得默认安全、稳定、自动分流的网络环境。

技术实现必须满足：

- AppKit 负责界面和用户交互。
- Go daemon 负责网络核心编排和长期运行。
- sing-box 负责实际代理、DNS、路由和流量转发。
- App 与 Go 之间只通过 Pax Connect 自己定义的 gRPC 协议通信。
- 关窗口保留代理；退出 App 时恢复网络并停止 daemon。当前 sudo/临时 launchd 合同见 architecture §17，覆盖本文历史常驻服务描述。
- 配置错误、网络切换和核心崩溃不能永久破坏系统网络。

## 2. 总体架构

（2026-10-16 安装／归属校注：当前 App 通过 sudo 安装临时 root 会话；[进程模型决策](changes/pax-connect-network-control-plane/decisions/core-process-model.md)的目标为用户级 paxd + sudo-once pax-core，数据迁至 `~/Library/Application Support/Pax Connect/`、私有文件 0600；目标为 planned。） 以下保留原有记录。

```text
┌──────────────────────────────────────────┐
│ Pax Connect.app（当前用户）               │
│                                          │
│  AppKit App（纯 AppKit）                  │
│  - Dashboard                             │
│  - 线路 / 分流 / 日志 / 设置             │
│  - 菜单栏入口                            │
│  - 仅转发睡眠/唤醒事件给 daemon          │
│  - Pax Connect gRPC Client               │
└──────────────────┬───────────────────────┘
                   │ Unix Domain Socket
                   │ gRPC + Protobuf（Pax 产品协议）
┌──────────────────▼───────────────────────┐
│ pax-connectd（root, launchd 托管）        │
│  - gRPC Server                            │
│  - LifecycleManager                      │
│  - ConfigStore                            │
│  - SingBoxAdapter                         │
│  - NetworkMonitor（自监听网络变化）        │
│  - RouteManager                           │
│  - RuleSetManager                         │
│  - ConfigGenerator / Validator            │
│  - HealthChecker                          │
│  - TelemetryConsumer / Stats              │
└──────────────────┬───────────────────────┘
                   │ Embedded Go API (recommended)
┌──────────────────▼───────────────────────┐
│ sing-box                                  │
│  - DNS                                    │
│  - TUN inbound                            │
│  - System proxy related service           │
│  - Route rules                            │
│  - Outbounds and protocol implementations │
└──────────────────────────────────────────┘
```

## 3. 进程和组件边界

### AppKit App

负责：

- 展示连接状态、线路、分流和日志。
- 发送 `SetProxyEnabled`、`SelectRoute`、`UpdateSettings` 等产品命令。
- 仅转发 macOS 睡眠和唤醒事件（`NotifyPowerEvent`）给 daemon；网络变化由 daemon 自己监听。
- 对错误进行用户可理解的翻译。

不负责：

- 直接启动 sing-box。
- 直接执行 `sudo` 或 shell 命令。
- 直接修改默认路由和 DNS。
- 解析 sing-box 内部对象。

### Go Controller Daemon（`pax-connectd`）

（2026-10-16 安装／归属校注：当前 App 通过 sudo 安装临时 root 会话；[进程模型决策](changes/pax-connect-network-control-plane/decisions/core-process-model.md)的目标为用户级 paxd + sudo-once pax-core，数据迁至 `~/Library/Application Support/Pax Connect/`、私有文件 0600；目标为 planned。） 以下保留原有记录。

以 root 运行，由 launchd 托管（`SMAppService.daemon` 注册为 LaunchDaemon）。负责：

- 维护 Pax Connect 的运行状态机。
- 生成、校验、保存和回滚配置。
- 启动、停止和重载 sing-box。
- 统一管理 TUN、系统代理、DNS 和路由状态。
- 自己监听网络变化（接口、默认路由、DNS 配置），执行节点健康检查和自动切换。
- 向 App 推送状态、日志、连接和流量事件。

### sing-box

负责：

- 代理协议实现和加密连接。
- DNS 解析和 DNS 路由。
- TUN 流量接收。
- 路由规则匹配。
- outbound 连接和流量转发。

sing-box 不负责 Pax Connect 的产品状态、配置版本回滚、用户提示和异常解释。

## 4. IPC 与 gRPC

使用 Unix Domain Socket 上的 Pax Connect 自定义产品 gRPC，不监听公网或局域网 TCP 端口；它不是 sing-box 的 gRPC API 或 proto。

传输方式、Socket 路径与权限、`LOCAL_PEERCRED` 鉴权、开发期 `--socket` 调试、`PaxService` 接口清单和 `WatchEvent` 定义的唯一权威是 `docs/architecture.md` §4，此处不再重复维护。

- 命令只返回 `OperationAck{operation_id}`，最终结果通过 `Watch` 的 `OperationResult` 回来；UI 永远以 daemon 推送为准。
- 大列表（日志页、分流页）走 `Query*` 分页，不从 stream 里全量拿。

## 5. 运行状态机

```text
OFF
  │ Start
  ▼
STARTING
  │ 核心启动成功
  ▼
RUNNING
  │ 网络变化 / 节点失效
  ▼
RECOVERING
  │ 恢复成功
  └──────────────► RUNNING

RUNNING ── ApplyConfig ──► RELOADING
RELOADING ── 成功 ───────► RUNNING
RELOADING ── 失败 ───────► ROLLING_BACK
ROLLING_BACK ─────────────► RUNNING / ERROR

任意状态 ── Stop ──► STOPPING ──► OFF
```

状态必须持久化最近一次有效结果，避免 UI 重启后只显示一个没有依据的开关状态。

核心状态和产品状态分开：

- `RUNNING`：sing-box 进程/实例正在运行。
- `PROTECTED`：Pax Connect 经过 DNS、出口和路由检查，确认网络保护有效。
- `DEGRADED`：核心运行，但部分安全或可用性检查失败。

## 6. sing-box 集成策略

集成方式（Embedded Backend）、配置文件/内存实例/gRPC 三层关系、Go module 版本锁定和"三种 gRPC"边界的唯一权威是 `docs/architecture.md` §7，此处不再重复维护。要点：

- Go daemon 以固定版本的 `github.com/sagernet/sing-box` Go module 直接嵌入，`box.New` / `Start` / `Close` 由 `internal/singbox/SingBoxAdapter` 统一调用；`option.Options`、`box.Box` 等上游类型不越过 adapter 边界，不把 sing-box 源码复制进业务目录。
- 嵌入模式默认直接调用 Go 对象，不启动额外 gRPC server；sing-box 的 `StartOrReloadService(configContent)` 是 Go 内部方法，不能替代 Pax Connect 的 `ApplyConfig`（后者还要负责产品 schema、健康检查、版本记录和失败回滚）。
- Clash API 和 V2Ray API 不属于必需依赖；Pax Connect 的配置、生命周期和状态模型由 Go daemon 统一负责。

## 7. 配置管理与 Reload

### 配置目录

（2026-10-16 安装／归属校注：当前 App 通过 sudo 安装临时 root 会话；[进程模型决策](changes/pax-connect-network-control-plane/decisions/core-process-model.md)的目标为用户级 paxd + sudo-once pax-core，数据迁至 `~/Library/Application Support/Pax Connect/`、私有文件 0600；目标为 planned。） 以下保留原有记录。

```text
/Library/Application Support/Pax Connect/      # root 所有，0700
├── configs/
│   ├── active.json
│   ├── pending.json
│   └── history/
├── rulesets/
├── state.json
├── logs/
└── run/
```

### ApplyConfig 流程

```text
接收新配置
  → 校验 JSON 和产品 schema
  → sing-box CheckConfig
  → 检查端口、DNS、TUN 和路由冲突
  → 保存 pending.json
  → 记录本实例接口/路由基线（不用于恢复全局路由）
  → 停止旧实例
  → 启动新实例
  → 等待核心 Started
  → 检查 TUN / DNS / 代理出口
  → 成功：active.json 指向新版本
  → 失败：停止新实例并恢复上一版本
```

### 变更分级

| 变更 | 处理方式 |
|---|---|
| 选择已有节点 | 运行时更新或轻量操作 |
| 切换智能规则模式 | 运行时更新 |
| 修改 DNS | 完整 reload |
| 修改 TUN 参数 | 完整 reload |
| 修改 inbound、route、outbound | 完整 reload |
| 修改日志级别 | 运行时更新 |
| 更新订阅节点列表 | 生成新配置并 reload |

sing-box 的简单 `ReloadService` 不能代替 Pax Connect 的 `ApplyConfig`，因为产品需要接收新配置、健康检查、版本记录和失败回滚。

## 8. DNS、TUN 与系统代理

### 默认策略

- 产品只有“开启/关闭”两个用户状态。
- 开启时默认使用智能分流。
- TUN 或系统代理是内部接入方式，不作为首页模式选择。
- DNS 由 sing-box 统一处理，并根据路由规则决定解析路径。
- MVP关闭时只清理本实例TUN与路由；系统代理/DNS快照恢复属于M2，不恢复全局路由表。

### 系统代理

适合：

- 浏览器和遵循 macOS 系统代理的应用。
- 不需要接管所有系统流量的场景。

限制：

- 部分应用不遵守系统代理。
- UDP 和后台服务可能绕过。
- 按应用统计不一定完整。

### TUN

适合：

- 需要接管更多 TCP/UDP 流量。
- 需要统一 DNS 和路由分流。

限制：

- 需要更高系统权限。
- 可能与其他 VPN、虚拟网卡或防火墙冲突。
- 需要处理睡眠唤醒、切网和异常退出后的恢复。

个人版本暂不采用 Packet Tunnel Provider；TUN 由 root LaunchDaemon 中的 daemon 统一管理，不引入独立 sing-box Helper。权限模型与 root 攻击面收窄措施见 §12，后续如确有权限隔离需要，再单独评估通用网络权限组件。（2026-10-16 安装／归属校注：当前 App 通过 sudo 安装临时 root 会话；[进程模型决策](changes/pax-connect-network-control-plane/decisions/core-process-model.md)的目标为用户级 paxd + sudo-once pax-core，数据迁至 `~/Library/Application Support/Pax Connect/`、私有文件 0600；目标为 planned。）

## 9. 网络监控与健康检查

### NetworkMonitor

负责监听或检测：

- Wi-Fi、Ethernet、Hotspot 变化。
- 默认路由变化。
- DNS 配置变化。
- 睡眠和唤醒。
- 网络接口和 TUN 状态。

daemon 自己监听网络变化；App 只把睡眠/唤醒提示转发给 daemon（`NotifyPowerEvent`），最终状态判断和恢复动作由 daemon 负责，不依赖 App 在线。

### HealthChecker

检查分层进行：

1. **进程/实例**：sing-box 是否运行。
2. **入口**：系统代理或 TUN 是否可用。
3. **DNS**：查询是否成功、是否走预期路径。
4. **代理出口**：测试节点连接和出口访问。
5. **业务可用性**：对常用服务执行低频、可配置探测。

健康检查失败不能只返回一个布尔值，至少要记录阶段、目标、耗时、错误类型和是否自动修复。

## 10. 自动选路与分流

### 路线评分

节点评分可以综合：

```text
score =
  latency       × 0.30
  + successRate × 0.35
  + packetLoss  × 0.20
  + stability   × 0.15
```

具体权重应通过真实使用数据调整，不应把单次测速结果当成唯一依据。

### 分流信号

- 域名服务分类。
- 本地 rule-set。
- GeoIP/ASN。
- DNS/CNAME 和 IP 归属。
- 应用或进程信息（TUN 模式下可选）。
- 节点出口地区和历史成功率。

国家识别只是内部证据。面向用户应优先显示“AI 服务、工作、流媒体、本地服务”等服务意图。

## 11. 日志与事件模型

2026-10-18 当前日志/流量接入范围以[实施规格](changes/pax-connect-network-control-plane/spec.md#2026-10-18-日志与流量实施规格to-spec-deep)及其Q1–Q6为准；保留期已被[history-storage](changes/pax-connect-network-control-plane/history-storage.md)取代。设计待确认，尚无新的运行证据。

日志分为产品事件和原始核心日志：

- **产品日志**：用户可读，例如“节点连接成功”“Wi-Fi 变化，正在恢复”。
- **核心日志**：sing-box 原始输出，用于详细诊断。

产品日志字段：

```text
timestamp
level
category
title
message
affectedTarget
resolved
action
metadata
```

日志页提供（M2）：

- 全部
- 错误
- 警告
- 系统

Dashboard 只显示最近几条重要日志。异常不单独建立一级“问题”页面；概览显示未解决数量，并跳转到日志筛选。

## 12. 数据安全

- Unix Socket 不监听公网。
- gRPC 方法采用白名单，不提供任意命令执行。
- 配置文件原子写入，权限限制为用户可读。
- 订阅凭据、代理密码和密钥不写入普通日志。
- 日志中的域名、IP 和应用信息提供清理或删除能力（保留 1/12/24 小时）。
- 所有外部规则集下载使用 HTTPS，并校验版本或签名。
- 不默认上传用户的域名、连接或流量统计。

daemon 以 root LaunchDaemon 运行（`SMAppService.daemon` 注册，首次在系统设置批准一次），但仍然不引入独立 sing-box Helper。收窄攻击面：gRPC 方法白名单、订阅只允许 HTTPS、导入内容严格按 `NodeProfile` 解析（不接受路径和命令）、日志不写密钥。TUN、路由、DNS 和系统代理操作由 daemon 统一管理；如果后续权限隔离要求改变，再单独评估通用权限组件。（2026-10-16 安装／归属校注：当前 App 通过 sudo 安装临时 root 会话；[进程模型决策](changes/pax-connect-network-control-plane/decisions/core-process-model.md)的目标为用户级 paxd + sudo-once pax-core，数据迁至 `~/Library/Application Support/Pax Connect/`、私有文件 0600；目标为 planned。）

## 13. macOS 打包与启动

（2026-10-16 安装／归属校注：当前 App 通过 sudo 安装临时 root 会话；[进程模型决策](changes/pax-connect-network-control-plane/decisions/core-process-model.md)的目标为用户级 paxd + sudo-once pax-core，数据迁至 `~/Library/Application Support/Pax Connect/`、私有文件 0600；目标为 planned。） 以下保留原有记录。

推荐 App 内嵌 Go daemon：

```text
Pax Connect.app/
└── Contents/
    ├── MacOS/PaxConnect
    ├── MacOS/pax-connectd                # Go daemon，不是 sing-box Helper
    ├── Library/LaunchDaemons/
    │   └── com.paxconnect.daemon.plist   # BundleProgram 指向 Contents/MacOS/pax-connectd
    ├── Resources/
    └── Info.plist
```

- 安装：`DaemonInstaller` 调 `SMAppService.daemon(plistName:)` 注册为 root LaunchDaemon；状态为 `.requiresApproval` 时展示引导页并调 `SMAppService.openSystemSettingsLoginItems()`。
- App 不用 `Process` 启动 daemon；daemon 由 launchd 托管，UI 启动时连接已有 daemon，不重复启动核心。
- App 退出、崩溃、重启都不影响代理；daemon 崩溃后由 launchd `KeepAlive` 拉起，启动时先对账网络快照再恢复运行。
- 卸载：设置页"移除后台服务"先停代理、恢复网络，再 `unregister`。

构建顺序：

```text
生成 Proto 代码
  → 编译 Go daemon（arm64）
  → 编译 AppKit App
  → 将 daemon 复制进 App Bundle 的 Contents/MacOS/
  → 由内向外完成代码签名（daemon hardened runtime → App）
  → notarization
```

最低系统 macOS 26，只构建 `arm64`，不做 Universal / `lipo`（macOS 27 起不再支持 Intel）。

## 14. AppKit 前端架构方案

App 是纯 AppKit，界面全部代码构建（无 XIB/Storyboard），图表由 PaxCharts 自绘，负责产品界面和交互，不负责代理核心、系统网络权限或 sing-box 类型解析。

页面骨架、Dashboard 网格、状态与渲染规则、Liquid Glass 与动效、PaxCharts 图表和菜单栏入口的完整合同以 `docs/architecture.md` §10–§15 为准，本节不再重复维护，只保留模块划分与状态流骨架。

### 14.1 模块划分与状态流

```text
PaxConnectApp
  └─ Features（Dashboard / Routes / Routing / Logs / Settings / Import）
       ├─ PaxUI（PaxTheme、CardView、PaxCharts）
       └─ PaxCore（领域模型、Stores、Intent）
            └─ PaxClient（DaemonConnection actor、EventAggregator actor）
                 └─ PaxProto（生成代码）
```

```text
PaxClient.DaemonConnection ──► ConnectionSession（actor，250ms 批次）
                                   │ 一次 MainActor 提交
                                   ▼
             ConnectionStore / RouteStore / TrafficStore / LogStore / SettingsStore
             （@MainActor @Observable，按领域拆分）
```

- Store 是 `@MainActor @Observable` 的领域 Store，通过 intent 发命令并记录 `pendingOperationID`；重复点击在 Store 层合并。
- 命令最终结果以 `Watch` 的 `OperationResult` 为准；断线保留最后快照，不用本地数据覆盖。

渲染规则（自绘视图依赖追踪、diffable 列表、稳定 ID、环形缓冲与后台暂停）、玻璃分层与动效档位、PaxCharts 图表类型与降采样、菜单栏入口行为，均见 `docs/architecture.md` §12–§15。

## 15. 可靠性与性能约束

当前架构的主要风险不在 AppKit 布局，而在高频事件、断线恢复、配置并发和网络恢复的一致性。以下约束属于正式架构，而不是实现阶段的可选优化。

### 15.1 事件流与快照恢复

只有一条 `Watch` 流，`WatchEvent` 的 oneof 承载 `snapshot` / `operation` / `history_delta` / `current_path_check` / `ai_usage` 五类 payload，事件带全局单调递增的 `seq`；命令结果通过流上的 `OperationResult(operation_id)` 回来。推送只带版本号与小摘要，明细由 App 拉取（规则见 protocol.md §4.2）。客户端侧只处理这一条流，不再维护多条订阅的生命周期。

```text
seq                  # 全局单调递增，客户端据此判断丢包
eventType            # snapshot / operation / history_delta / current_path_check / ai_usage（与 WatchEvent oneof 一致）
occurredAt
snapshotVersion      # 与 WatchEvent 一起，标记该事件基于哪份快照
```

客户端重连流程：

```text
检测断线
  → 指数退避重连
  → Hello 握手（版本不兼容则提示升级）
  → GetSnapshot 获取完整快照
  → 记录 snapshotVersion 与已消费的最大 seq
  → 恢复 Watch（带上 from_seq）
  → daemon 补发或直接推新快照；客户端丢弃版本早于快照、seq 已消费的事件
```

`Watch` 不能自己成为 UI 的权威状态。Go daemon 在关键变更后发布一致快照，增量事件只作补充；App 侧以快照为准，避免出现线路已切换但日志、流量仍显示旧状态的撕裂。

### 15.2 领域 Store 更新策略

以 `docs/architecture.md` §13 为准（ConnectionSession 250ms 批次、按领域拆分的 `@MainActor @Observable` Store、intent + `pendingOperationID`、稳定 ID、断线保留快照），此处不再重复维护。

### 15.3 图表和日志性能

图表降采样（60–180 点，保留 min/avg/max）与动画规则以 `docs/architecture.md` §15 为准；原始日志分级、限流与保留期（1/12/24 小时）以 `docs/architecture.md` §9 为准。

### 15.4 配置操作串行化

所有 `ApplyConfig`、订阅更新和需要 reload 的线路操作进入单一 `ConfigOperationQueue`：

- 同时只允许一个配置操作执行。
- 尚未执行的旧线路选择可以被最新选择覆盖。
- 每次操作生成 `operationID`，状态事件必须带回该 ID。
- 操作有超时、取消和失败回滚；超时后不能继续提交旧配置。
- 轻量线路切换优先使用运行时更新，DNS、TUN、route、inbound 变化才执行完整 reload。

### 15.5 网络恢复和权限边界

- App 只上报睡眠和唤醒事件（`NotifyPowerEvent`）；网络变化监听与恢复决策全部由 Go `NetworkMonitor` 负责，daemon 运行身份、数据目录与攻击面收窄见 §12 与 `docs/architecture.md` §11。
- daemon 必须具备单实例锁、socket 残留清理、崩溃后的网络恢复和 UI/daemon 版本兼容检查。
- MVP TUN停止仅关闭本实例并确认自身接口/路由清理，不恢复全局路由快照；系统代理与DNS快照恢复属M2，恢复动作保持幂等。

### 15.6 可观测性和测试

必须为关键链路记录可关联的 `operationID`、`snapshotVersion` 和耗时：

```text
用户操作 → gRPC → daemon 状态机 → sing-box → 健康检查 → UI 快照
```

最低测试范围（gRPC 断线重连、配置串行化、reload 回滚、网络恢复、AppKit 布局）以 `docs/architecture.md` §18 为准。

## 16. 推荐项目结构

目录结构以 `docs/architecture.md` §16 为准（daemon 内部含 controller、config、importers、singbox、network、routing、telemetry、stats、storage、ipc 和 gen/proto），此处不再重复维护。依赖关系保持单向：

```text
PaxConnectApp → Features → PaxUI / PaxCore → PaxClient → PaxProto
                                        │
                                        └─ gRPC（Unix Socket）→ pax-connectd → SingBoxAdapter → sing-box
```

App 各模块不引用 sing-box 类型，业务代码不复制 sing-box 源码，上游 API 变化集中在 `SingBoxAdapter` 处理；Pax Connect gRPC 与 sing-box gRPC 是两个不同的协议边界，不能因为名称相同而复用接口或共享业务模型（细则见 `docs/architecture.md` §5、§7）。

## 17. 实施阶段

当前按 `docs/architecture.md` §19 推进 UI：设计规范 → 组件 → Dashboard → 其余 UI；全部 UI 完成后再制定真实接入计划。旧 MVP 队列冻结，已有后端、协议、模型与验收记录保留。正式连接仍遵守本文网络状态、安全与恢复合同；演示入口使用隔离的本地状态与动作，独立于后台服务。产品视角见 `docs/product.md` §13。

## 18. 真实接入验收标准（当前冻结）

- App（主窗口或菜单栏）可以仅通过一个开关启动和停止 Pax Connect。
- daemon 重启或 UI 重启后，状态可以恢复并准确反映实际网络状态。
- 配置错误不会替换掉当前可用配置。
- reload 失败可以自动回滚。
- Wi-Fi、热点切换和睡眠唤醒后能自动恢复或明确报错。
- 关闭 Pax Connect 后，系统代理、DNS 和路由恢复到启动前状态。
- Dashboard 中的线路、分流和日志数据来自真实运行状态，而不是配置文件推测。
- 任何安全异常都能在概览中显示未解决数量，并跳转到对应日志详情。

本节保留真实接入的验收合同；旧 MVP 的限定范围见 `docs/archive/2026-10-02-mvp-freeze.md`。当前 UI 阶段的验收以 `docs/architecture.md` §19.3 为准；演示操作与模拟状态不能证明真实网络能力。

2026-10-19：用户已授权并实施55–65日志/流量接入。三页同源查询、日志本机保留及清除已接通；具体通过项与未验证的TUN/应用/协议/性能条件唯一见tests/logs-traffic/RESULTS.md，覆盖此前本轮仍待确认的进度文字，不自动完成旧票。
