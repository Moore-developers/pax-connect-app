# 架构：设计系统、动画与图表（§14、§15）

## 14. 设计系统

原生 UI 规范唯一见 `docs/changes/pax-connect-network-control-plane/ui/design-system.md`（连接工作台，用户 2026-10-02 确认）。AppKit 实现位于 PaxUI，组件目录与 Dashboard 共同复用；不维护平行皮肤。

- `PaxTheme`：原生系统语义色与系统强调色支持深浅色；间距、圆角、字号与动效集中定义，业务 View 使用 token。原型暖色 token 不直接移植；完整规范见上述 design-system.md。
- 字体：系统字体 + `monospacedDigitSystemFont` 显示数值，避免数字跳动。
- 图标：SF Symbols（`NSImage(systemSymbolName:)`）；品牌资源进 Asset Catalog。原型里的 Phosphor 图标不带过来。
- 可访问性：自绘控件实现 `NSAccessibility` 角色、标签和数值；图表提供文本摘要（如"最近 30 分钟平均延迟 148ms，最高 310ms"）。
- 颜色表达状态不表达国家：健康 / 警告 / 错误三类语义色，强调色只给品牌和主操作。

### 14.1 Liquid Glass

分层原则（Apple 的规范）：玻璃只用于浮在内容之上的导航和控制层；数据卡片、表格、图表属于内容层，保持实底。全页都做成玻璃，会让可读性和性能一起变差。

| 层 | 元素 | 实现 |
|---|---|---|
| 系统自带 | 窗口工具栏、侧栏、sheet、popover、菜单、`NSSwitch` / `NSSegmentedControl` / 按钮 | 用 Xcode 26 SDK 编译即自动获得，不写代码 |
| 工具栏定制 | 智能代理开关项 | `NSToolbarItem.style = .prominent`；降级时用 `backgroundTintColor` 着琥珀色 |
| 侧栏延伸 | Dashboard 内容延伸到侧栏玻璃下方 | `NSBackgroundExtensionView` 包裹页面背景 |
| 自定义浮层（按 U1 规范选用） | 操作条、Toast、异常横条 | `NSGlassEffectView`（`style` 取 `.regular` 或 `.clear`，设 `cornerRadius`、`tintColor`）；相邻的几块放进同一个 `NSGlassEffectContainerView`，设 `spacing` 让它们靠近时融合 |
| 内容 | 卡片、表格、图表 | 实底 `CardView`，不用玻璃 |

- 封装：`PaxUI.GlassSurface` 统一包一层，业务代码不直接创建 `NSGlassEffectView`，方便以后调参或降级。
- 注意：玻璃要**包住**内容（设为 `contentView`），不能作为背景放在内容下面；玻璃也不能叠在玻璃上。
- 系统开启"降低透明度"或"增强对比度"时，系统玻璃会自动变不透明；自定义浮层同样要检查 `accessibilityDisplayShouldReduceTransparency`。

### 14.2 动画

全部用 Core Animation 和 `NSAnimationContext`，统一收在 `PaxUI.Motion`：定义 3 档时长（快 0.15s / 标准 0.25s / 慢 0.4s）、两种曲线（标准缓动、弹簧），业务代码只引用档位，不写具体数字。

| 场景 | 动画 | 实现 |
|---|---|---|
| 开关代理 | 状态胶囊颜色渐变；当前线路卡片出现"连接中"扫光，成功后收束成常亮状态点 | `CABasicAnimation` 渐变颜色 + `CAGradientLayer` 位移扫光 |
| 玻璃操作条 | 展开/收起时几块玻璃融合或分离 | 在 `NSGlassEffectContainerView` 里改变子视图 frame，系统自动做融合形变 |
| 切换线路 | 线路名和数值交叉淡入淡出；延迟曲线从旧数据过渡到新数据 | `CATransition(.fade)`；曲线路径做 `path` 动画 |
| 数值变化 | 流量、延迟数字滚动过渡 | 自绘 `RollingNumberView`，每位数字用 `CATransition(.push)` |
| 图表进场 | 折线从左往右描出；柱子从底部长高 | `strokeEnd` 从 0 到 1；`bounds.size.height` 弹簧动画 |
| 页面切换 | 侧栏切页时内容轻微位移并淡入 | `NSTabViewController.transitionOptions = [.crossfade]` 加自定义位移 |
| 列表更新 | 新日志从顶部插入；筛选时行淡入淡出 | diffable data source 的 `animatingDifferences`，只在用户操作时开启 |
| 异常出现 | 异常横条从卡片底部滑出，轻微弹一下 | `CASpringAnimation` |
| Toast | 从底部升起，停留后淡出 | `NSAnimationContext` + `GlassSurface` |

规则：

- 动画只表达状态变化，1 秒一次的实时刷新（流量、延迟新点）不做过渡，避免界面一直在动。
- 开启"减少动态效果"（`accessibilityDisplayShouldReduceMotion`）后，位移、缩放、弹簧全部换成 0.15s 淡入淡出，扫光改为静态颜色。
- 窗口被遮挡或最小化时停掉所有持续动画（扫光、状态点呼吸）。

## 15. 图表（PaxCharts）

不用 Apple Charts，图表自绘。仅四种静态几何，无通用库：

| 图表 | 用途 | 实现 |
|---|---|---|
| 样本面积图 | 当前线路最近30次、流量列表20条/详情60条有效延迟 | `PaxTimeSeriesChart` 样本模式：等距、自适应、单调平滑实线与浅色填充；稀疏规则见统一延迟合同 |
| 柱图 / 堆叠柱 | 今日流量近 7 日；AI 用量分层 | 一组 `CALayer` 或单个路径 |
| 分段比例条 | 代理/直连/未归类 | 三段 `CALayer` |
| 迷你趋势 | 线路列表行内 | 同折线，去掉坐标 |

- 延迟图由daemon筛有效后取最近样本，UI不做时间桶聚合或峰谷抽样；其他时间图保持各自查询口径。
- 实时追加点时 `CATransaction.setDisableActions(true)`；切换时间范围才做过渡动画。
- 悬停提示用 `NSTrackingArea` 与共享浮层；延迟样本显示完整日期时间到秒及耗时。

会话分析两卡共用卡片上方会话选择。上方复用PaxDataTable、PaxProportionView.compact与PaxTabs，展示三档及小计；下方PaxAnalysisChart上下联动曲线/费用柱、纯文字指标与共享hover。合同P07d。
