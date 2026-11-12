# 架构：App 模块、UI 骨架与状态渲染（§10、§12、§13）

## 10. App 模块划分

用本地 Swift Package 拆模块，依赖只能向下：

```text
PaxConnectApp（Xcode App target）
  └─ Features（Dashboard / Routes / Routing / Logs / Settings / Import）
       ├─ PaxUI（设计 token、基础控件、PaxCharts）
       └─ PaxCore（领域模型、Stores、Intent）
            └─ PaxClient（gRPC 连接、重连、事件聚合）
                 └─ PaxProto（生成代码）
```

| 模块 | 内容 | 规则 |
|---|---|---|
| `PaxProto` | swift-protobuf + protoc-gen-grpc-swift-2 生成物 | 只有 `PaxClient` 可 import |
| `PaxClient` | `DaemonConnection`（actor）、`ConnectionSession`（actor）、proto → 领域模型映射 | 不碰主线程；不暴露 proto 类型 |
| `PaxCore` | `Route`、`RoutingDecision`、`TrafficSnapshot`、`ProductLog`、`ProtectionStatus`；各领域 Store | 不 import AppKit，可在纯单元测试里跑 |
| `PaxUI` | `PaxTheme`、`CardView`、`StatusBadge`、`PaxSwitch`、`SegmentedTabs`、`MetricView`、`PaxCharts` | 不认识业务 Store，只接收值 |
| `Features` | 各页 ViewController 和页面专属 View | 通过 Store 读状态、发 Intent；不直接调 `PaxClient` |
| App target | `AppDelegate`、`WorkbenchWindowController`、daemon 注册/授权引导、菜单 | 组装依赖 |

## 12. UI 骨架



```text
NSApplication
└─ AppDelegate
   ├─ DaemonInstaller（SMAppService 注册、授权状态、引导页）
   ├─ MenuBarController（NSStatusItem + NSMenu，见P06）
   └─ WorkbenchWindowController（NSWindow, fullSizeContentView, 最小 1000×680）
      ├─ NSToolbar
      │   ├─ 页面标题 / 保护状态胶囊
      │   ├─ NSSearchToolbarItem（全局搜索，作用于当前页）
      │   └─ 智能代理开关
      └─ WorkbenchShellViewController（NSSplitViewController）
          ├─ sidebar  : WorkbenchNavigation（产品侧栏）
          ├─ content  : NSTabViewController（tabStyle = .unspecified，由侧栏驱动）
          │   ├─ WorkbenchDashboardViewController
          │   ├─ RouteWorkbenchViewController
          │   ├─ RoutingViewController
          │   ├─ LogsViewController
          │   ├─ SettingsViewController
          │   └─ AboutViewController（版本信息与开源许可直接展示，见P08）
          └─ inspector: 可选详情容器，按各页合同使用；日志为双击事件打开浮层（P04）
```

**Dashboard（连接工作台）**：`NSScrollView` + flipped document view，固定两档手写布局；净内容宽 ≥700pt 时左 44% RTT线路质量卡（下含可选线路历史列表）、右 56% 流量，跨列明细，下方事件和条件性异常；不足时堆叠，收紧连接卡。唯一开关在 toolbar，线路选择在质量卡列表，详情可从线路页进入，不再常驻线路网格。窗口默认 1280×860、最小 1000×680。页面合同唯一见 `ui/specs/P01-dashboard.md`；token 与组件状态唯一见 `ui/design-system.md`。

UI 阶段默认 App 装配 `WorkbenchWindowController → WorkbenchShellViewController → Dashboard / ComponentGallery`；`WorkbenchDemoStore` 只持有内存演示状态，不创建 ConnectionSession / DaemonInstaller。PAX_MODE=demo 强制演示；显式 PAX_MODE=live 或非空 PAX_SOCKET 进入同一新工作台的真实装配；动作按 Hello 通告的能力启用，未通告的保持禁用。旧MainWindow/MainSplit/Sidebar已删除。演示目录以产品快照驱动核心Store，OFF选路走命令替身；未交付的运行中演示交互仍隔离在fixture。

**菜单栏入口**：统一原生菜单、只读测试耗时及六项页面导航，唯一合同见 [P06](../changes/pax-connect-network-control-plane/ui/specs/P06-menubar.md)（MB-01–10，用户已授权implement；验收见tests/menu-bar）。取代此前左右键分离、popover和退出仅UI的设计；实际退出仍遵守§17。

**组件映射**：

| 设计元素 | AppKit 实现 |
|---|---|
| 卡片 | `CardView`：layer-backed `NSView`，`updateLayer()` 设圆角、描边、背景、阴影 |
| 表格（域名/IP、日志、分流列表） | view-based `NSTableView` + `NSTableViewDiffableDataSource`，稳定 ID |
| 应用/域名/IP 流量排行 | 复用 `PaxDataTable` 与流量单元格；Dashboard 按 P01 保留摘要行数，分流按 P03 为全量六列表格，不生成虚构占位行 |
| Tabs（全部/代理/直连/未归类） | 自绘 `SegmentedTabs`，或皮肤化 `NSSegmentedControl` |
| 智能代理开关 | `NSSwitch`（原生优先）；设计需要时自绘 `PaxSwitch` |
| 线路选择 | `NSPopover` 内嵌列表；线路页用 inspector |
| 导入线路 | 窗口 sheet（`presentAsSheet`），链接/二维码/订阅/outbound 四个分段 |
| 二维码导入 | 拖入图片或从剪贴板读取，`CIDetector(ofType: CIDetectorTypeQRCode)` 解码 |
| 异常提示 | 当前线路卡片底部的玻璃横条（`GlassSurface`）+ 跳转日志筛选 |
| Toast | 窗口底部玻璃浮层（`GlassSurface`），动画见 §14.2 |

## 13. 状态与渲染规则

```text
PaxClient.DaemonConnection ──► ConnectionSession（actor，250ms 批次）
                                   │ 一次 MainActor 提交
                                   ▼
             ConnectionStore / RouteStore / TrafficStore / LogStore / SettingsStore
             （@MainActor @Observable，按领域拆分）
                                   │
                ┌──────────────────┴──────────────────┐
                ▼                                     ▼
   自绘视图：在 updateLayer()/layout()/draw()     列表视图：Observations { store.rows }
   里直接读 Store → AppKit 自动追踪并失效          → apply diffable snapshot
```

- **自绘视图**（卡片数值、图表、状态胶囊）：在 `updateLayer()`、`layout()`、`draw(_:)` 中读取 `@Observable` 属性，macOS 26 自动追踪依赖，属性变化只失效读过它的视图，不写 `needsDisplay = true`。
- **列表视图**：用 Swift 6.2 的 `Observations { }` 异步序列监听行数据，稳定ID不变时仅更新变化单元格，结构变化才更新diffable snapshot；普通刷新不先清空、不重建容器、不重播入场。阅读、分页与删除失效的规则见[页面自动更新](../changes/pax-connect-network-control-plane/incremental-page-updates.md)§2–3。
- **命令**：ViewController 调 Store 的 intent（如 `connection.setProxyEnabled(true)`）；Store 记录 `pendingOperationID` 驱动"启动中"，收到 `OperationResult` 后清除；重复点击在 Store 层合并，不产生并发操作。
- **断线**：保留最后快照并显示"与后台服务断开"，不用本地数据覆盖。
- **生命周期**：窗口不可见（`occlusionState` 不含 `.visible`）时PaxRefresh停止页面查询、保留脏标记；恢复时补读，不补跑隐藏期间的更新。Watch仍消费状态和失效通知。
- **概览独立读取**：`PaxRefresh` 同一 page 可注册命名异步消费者，复用页面可见集与策略间隔，各自拥有 gate 和可取消在途任务，通知合并一次后继；原单消费者 `bind` 行为保持。概览五组及按依赖观察 / 投影合同见 [P01 §5](../changes/pax-connect-network-control-plane/ui/specs/P01-dashboard.md#5-刷新与状态)。日期 / 时区与唤醒通过系统通知复核，无新增轮询。
- 日志 Store 用固定容量环形缓冲（Dashboard 20 条，日志页走分页查询）。

### 13.1 共享件与消费边界

- **代理入口**：Dashboard 与线路页都经 Features 的 `ProxyTransitionPresenter` 进入同一个 `ConnectionStore`；授权说明、证书风险、已证实的 TUN 冲突保留确认，普通进度 / 失败用 `ProxyStatusAccessory` / `PaxInlineStatus` 在开关旁显示，不再用大浮层。停止优先，错误归属不被后台测速或订阅覆盖。
- **反馈**：`PaxCore.ConnectionFeedback` 是 `ConnectionStore` 的只读产品问题投影，按授权 / 连接 / 选路 / 来源 / 测量归属动作；“关闭”只影响当前会话呈现，持久错误仍在来源 / 详情里。App 不靠隐藏提示掩盖失败。
- **查询型 Store**：日志 / 流量由 `TelemetryStore` 沿唯一 `ConnectionSession` 共享查询并合并 / 取消；页面进入、切回与筛选变更读最新快照；可见期间由PaxRefresh按域信号和最小间隔补读，隐藏停止。缓存/令牌失效与仍有效的显示值分开管理，必要补读不被阅读暂停拦截。对象图标由 shell 注入的 owner（Features/Workbench）编排可见对象请求，PaxUI 只收值、图片与回调，不直接请求网络。
- **分层**：App 沿 PaxClient → PaxCore → Features 只发产品意图、读产品值；模型映射只在 PaxClient；时间 / 流量 / 延迟格式化集中在 PaxCore；`PaxDataTable` 的可选增量接缝（reloading ID、cellUpdater、表头排序、多选）只服务实际消费者，默认路径不变。
