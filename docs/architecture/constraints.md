# 架构：技术选型与约束（§0–§1）

## 0. 相对旧基线改了什么

| 项 | 旧基线 | 新方案 | 原因 |
|---|---|---|---|
| UI 框架 | 前版方案 | 纯 AppKit + Core Animation 自绘图表（技术选型） | 用户决定 |
| 最低系统 | 未定 | macOS 26，首版只出 arm64 | 近 5 年的 Apple silicon Mac 都能装 26；Liquid Glass 与 AppKit Observation 自动追踪从 26 起才有；macOS 27 已不支持 Intel |
| daemon 权限 | 普通用户权限 | 用户级 paxd + sudo-once root pax-core（计划，见 §2–§3） | 旧基线自相矛盾：macOS 上创建 utun 和写路由必须 root，普通用户 daemon 做不了 TUN |
| 视觉 | Web 原型的自定义深色皮肤 | Liquid Glass 用于导航和控制层，内容层保持实底 | macOS 26 原生设计语言；见 §14.1 |
| daemon 启动 | App 用 `Process` 拉起 | App 拉起 paxd；首次 ON sudo 拉起 core（计划） | 目录无需 root；core 会话内复用，随 App 退出 |
| 网络变化监听 | UI 上报，daemon 决策 | daemon 自己监听；App 只转发睡眠/唤醒提示 | root daemon 可直接读路由/接口变化，少一条依赖 UI 在线的链路 |
| 事件流 | 3 条独立 stream + 快照 | 1 条 `Watch` 多路复用 stream，全局递增 `seq` | 单一顺序、单一重连路径，从结构上消除三流撕裂 |
| 窄屏 | 390px 移动端布局 | 窗口最小尺寸 + 宽/紧凑两档 | Mac 窗口不存在手机宽度，移动端状态是 Web 原型带来的 |

不变的部分：Go daemon 内嵌 sing-box、`SingBoxAdapter` 隔离上游类型、产品 gRPC 与 sing-box gRPC 分离、配置 active/pending/last-known-good、Geo rule-set、保护状态模型、流量归属口径。这些在本文件 §5–§9 展开。

## 1. 约束

**"只用 AppKit"的边界**：

- 允许：AppKit、Foundation、Observation（Swift 标准库）、Core Animation、Core Graphics、Core Text、ServiceManagement、Network、OSLog。
- 技术选型为纯 AppKit，界面全部代码构建（无 XIB/Storyboard），可 diff、可审查、Agent 可改；图表由自绘 PaxCharts 提供，不引入系统图表库；不使用 `NSHostingView` / `NSHostingController`、Cocoa Bindings、Combine 等其它 UI 通道。

**语言与工具**：Swift 6（strict concurrency）、Xcode 26+、Go 1.24+。

**最低系统：macOS 26 Tahoe，只出 arm64**

| 候选 | 覆盖近 5 年新机（2021.9 以后发布） | 能用 Liquid Glass | 代价 |
|---|---|---|---|
| macOS 15 | 覆盖，外加少量旧 Intel | 否，要维护两套视觉 | 所有玻璃、Observation 相关代码都要加 `#available` 分支和降级实现；Intel 需要 Universal 构建和真机测试 |
| **macOS 26** | 覆盖。近 5 年发布的机型全部是 Apple 芯片，M1 及以后都能升 26 | 是 | 放弃未升级系统的用户 |
| macOS 27 | 覆盖，同上 | 是 | 27 刚于 2026.9 发布，装机量还低；新增 API 对本产品没有刚需 |

- 硬件范围：M1（2020）及以后的 Mac、MacBook Neo。选 26 的真实理由是需要 Liquid Glass 和 macOS 26 起的 AppKit Observation 自动追踪，macOS 15 要为此维护两套实现。首版只构建 arm64、不做 `lipo`：近 5 年发布的机型都是 Apple 芯片，macOS 27 起已不支持 Intel，Intel 用户群只会继续缩小。
- 如果以后必须支持 15：玻璃统一封装在 `PaxUI.GlassSurface` 里，15 上降级为 `NSVisualEffectView`；Info.plist 设 `NSObservationTrackingEnabled = YES`（该 Info.plist 键来自 Apple 文档说明，需实测确认）；`Observations` 改用 `withObservationTracking` 手动重订阅。架构不变，只是多一层降级。
