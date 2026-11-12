import AppKit
import Observation
import PaxCore
import PaxUI

/// 组件目录本身也使用 PaxUI，不另写展示皮肤；无后台、无网络的真实使用场景。
@MainActor
public final class ComponentGalleryViewController: NSViewController {
  private let tooltip: PaxChartTooltipCoordinator
  public init(tooltip: PaxChartTooltipCoordinator = PaxChartTooltipCoordinator()) {
    self.tooltip = tooltip
    super.init(nibName: nil, bundle: nil)
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override func viewWillDisappear() {
    super.viewWillDisappear()
    tooltip.close()
  }
  private let content = NSStackView()
  private let feedback = PaxText.label("点击组件查看交互反馈", secondary: true)
  private var buttons: [NSButton] = []
  private let scroll = NSScrollView()
  private var initiallyShown = false
  public override func loadView() {
    let root = WorkbenchSurface()
    scroll.hasVerticalScroller = true
    scroll.drawsBackground = false
    let document = WorkbenchDocument()
    scroll.documentView = document
    document.translatesAutoresizingMaskIntoConstraints = false
    content.orientation = .vertical
    content.alignment = .leading
    content.spacing = PaxTheme.Spacing.cardGap
    content.translatesAutoresizingMaskIntoConstraints = false
    document.addSubview(content)
    scroll.translatesAutoresizingMaskIntoConstraints = false
    root.addSubview(scroll)
    NSLayoutConstraint.activate([
      scroll.leadingAnchor.constraint(equalTo: root.leadingAnchor),
      scroll.trailingAnchor.constraint(equalTo: root.trailingAnchor),
      scroll.topAnchor.constraint(equalTo: root.safeAreaLayoutGuide.topAnchor),
      scroll.bottomAnchor.constraint(equalTo: root.bottomAnchor),
      document.widthAnchor.constraint(equalTo: scroll.contentView.widthAnchor),
      content.leadingAnchor.constraint(
        equalTo: document.leadingAnchor, constant: PaxTheme.Spacing.page),
      content.trailingAnchor.constraint(
        equalTo: document.trailingAnchor, constant: -PaxTheme.Spacing.page),
      content.topAnchor.constraint(equalTo: document.topAnchor, constant: PaxTheme.Spacing.pageTop),
      content.bottomAnchor.constraint(
        equalTo: document.bottomAnchor, constant: -PaxTheme.Spacing.page),
    ])
    view = root
    add(PaxText.label("组件目录", font: PaxTheme.Typography.title()))
    add(PaxText.label("AppKit · macOS 26 基础 / 27 增强 · 值与回调驱动", secondary: true))
    add(feedback)
    let featureTags = NSStackView(views: [
      PaxFeatureTag("CF", symbol: "cloud", tone: .blue), PaxFeatureTag("链式代理", tone: .purple),
      PaxFeatureTag("当前线路", symbol: "checkmark", tone: .blue, style: .current),
    ])
    addCard(
      "线路特性与路径",
      views: [
        featureTags,
        PaxConnectionPath(nodes: [
          .init("本机", address: "192.0.2.10", symbol: "desktopcomputer"),
          .init("CF", address: "172.64.79.122", symbol: "cloud", tone: .blue),
          .init("出口 IP", address: "未获取", symbol: "server.rack"),
        ]),
      ])
    let directory = PaxRouteTable()
    directory.apply([
      .init(
        id: "one", name: "洛杉矶 · 首选", connection: "VMess · WS · TLS", cf: "CF", current: true,
        address: "198.51.100.24", latency: "198 ms", speed: "4.1 MB/s"),
      .init(id: "two", name: "香港 → 东京", connection: "VLESS · TCP · TLS", chained: true),
      .init(id: "three", name: "台北 · 新增", connection: "VLESS · WS · TLS"),
    ])
    directory.heightAnchor.constraint(
      equalToConstant: PaxTheme.Routes.rowHeight * 3 + PaxTheme.Routes.headerHeight
    ).isActive = true
    add(directory)
    let bars = PaxMiniBars(
      values: [3.4, 3.8, 3.6, 4.2, 3.9], color: PaxTheme.DashboardColor.green,
      summary: "下载速度历史，单位 MB/s")
    bars.heightAnchor.constraint(equalToConstant: PaxTheme.Routes.miniHeight).isActive = true
    addCard("历史测速柱条", views: [bars])
    let metric = PaxRouteMetric(
      title: "线路延迟", symbol: "timer", value: "198", unit: "ms", caption: "今日 14:30 · 测试",
      samples: [224, 218, 216, 211, 207, 214, 202, 198], color: PaxTheme.DashboardColor.amber)
    let flow = PaxRouteTrafficChart(
      samples: [
        .init("08:00", values: [12, 80]), .init("08:30", values: [24, 160]),
        .init("09:00", values: [18, 120]),
      ], formatValue: TrafficFormatting.megabytes)
    flow.heightAnchor.constraint(equalToConstant: PaxTheme.Routes.chartHeight).isActive = true
    flow.tooltipCoordinator = tooltip
    addCard(
      "线路指标与堆叠流量",
      views: [PaxRouteSectionHeading("测试结果", symbol: "waveform.path.ecg"), metric, flow])
    let candidates = PaxCandidateTable(titles: ["IP 地址", "入口延迟", "测试时间", "状态"], choices: true)
    candidates.apply(
      rows: [
        .init(
          id: "one", values: ["172.64.79.122", "38 ms", "今日 14:30", "可连接"],
          statusColor: PaxTheme.Color.healthy)
      ], selected: "one")
    candidates.heightAnchor.constraint(
      equalToConstant: PaxTheme.Routes.candidateRowHeight + PaxTheme.Routes.controlHeight
    ).isActive = true
    addCard("候选选择表格", views: [candidates])
    let demo = WorkbenchDemoStore()
    let routeCard = PaxCurrentRouteCard(tooltip: tooltip)
    routeCard.apply(demo.dashboardRouteModel)
    let routeList = PaxRouteListCard(tooltip: tooltip)
    routeList.apply(items: demo.qualityItems, selectedID: demo.selectedRouteID)
    routeList.onSelect = { [weak routeCard, weak routeList] id in
      demo.perform(.selectRoute(id))
      routeCard?.apply(demo.dashboardRouteModel)
      routeList?.apply(items: demo.qualityItems, selectedID: id)
    }
    add(routeList)
    routeList.heightAnchor.constraint(equalToConstant: PaxTheme.Dashboard.routeListHeight)
      .isActive = true
    let traffic = PaxTodayTrafficCard(tooltip: tooltip)
    traffic.apply(demo.dashboardTrafficModel)
    let now = demo.trafficCapturedAt
    let buckets = demo.dashboardUsageBuckets
    let trend = PaxModelTrendCard(tooltip: tooltip)
    trend.apply(
      DashboardUsageProjection.trend(buckets: buckets, state: nil, updatedAt: now, now: now))
    let rank = PaxModelRankCard()
    rank.apply(DashboardUsageProjection.rank(buckets: buckets, state: nil, now: now))
    let attention = PaxInsightCard(title: "需要关注 · 今日", identifier: "gallery-attention")
    attention.apply(PaxInsightModel(items: demo.dashboardAttention, emptyTitle: "一切正常 · 今日暂无问题"))
    let changes = PaxInsightCard(title: "最近变化", identifier: "gallery-changes")
    changes.apply(PaxInsightModel(items: demo.dashboardChanges, emptyTitle: "今日暂无 Pax 操作记录"))
    let action = PaxActionCard()
    action.apply(DashboardInsightProjection.actionModel(.channelFailed))
    let insightHeight =
      PaxTheme.Dashboard.insightHeader
      + PaxTheme.Dashboard.insightRow * CGFloat(PaxTheme.Dashboard.insightRows)
    for (card, height) in [
      (routeCard as NSView, PaxTheme.Dashboard.primaryRowHeight),
      (traffic as NSView, PaxTheme.Dashboard.primaryRowHeight),
      (trend as NSView, PaxTheme.Dashboard.modelRowHeight),
      (rank as NSView, PaxTheme.Dashboard.modelRowHeight),
      (attention as NSView, insightHeight), (changes as NSView, insightHeight),
      (action as NSView, PaxTheme.Dashboard.actionCardHeight),
    ] {
      add(card)
      card.heightAnchor.constraint(equalToConstant: height).isActive = true
    }
    let mode = PaxTabs(["代理", "直连"], style: .mode)
    mode.onSelect = { [weak self] index in
      self?.feedback.stringValue = "模式：" + ["代理", "直连"][index] + "（组件回调）"
    }
    addCard("代理模式滑动开关", views: [row([mode])])
    let analysisList = RoutingTrafficList(
      store: RoutingWorkbenchStore(session: demo, initialPeriod: .day),
      icons: WorkbenchObjectIcons(),
      tooltip: tooltip, style: .dashboard)
    let analysis = WorkbenchDashboardViewController.makeAnalysisCard()
    analysisList.onOpen = { [weak self] id in
      self?.feedback.stringValue = "查看连接详情：" + id + "（组件回调）"
    }
    analysisList.render(in: analysis)
    add(analysis)
    let analysisHeight = analysis.heightAnchor.constraint(equalToConstant: analysis.preferredHeight)
    analysisHeight.isActive = true
    func refreshAnalysis() {
      analysisList.render(in: analysis)
      analysisHeight.constant = analysis.preferredHeight
    }
    analysis.onDimension = { index in
      if let value = RoutingWorkbenchStore.Dimension(rawValue: index) {
        analysisList.store.perform(.dimension(value))
        refreshAnalysis()
      }
    }
    analysis.onFilter = { index in
      if let value = RoutingWorkbenchStore.Direction(rawValue: index) {
        analysisList.store.perform(.direction(value))
        refreshAnalysis()
      }
    }
    let navigation = PaxNavigationButton(title: "概览", symbol: "square.grid.2x2")
    navigation.isSelected = true
    addCard("导航选中态", views: [navigation])
    let main = actionButton("主操作", style: .prominent)
    let secondary = actionButton("次要操作", style: .secondary)
    let ghost = actionButton("轻量操作", style: .ghost)
    let disabled = actionButton("暂不可用", style: .secondary)
    disabled.isEnabled = false
    addCard(
      "按钮与交互",
      views: [
        row([main, secondary, ghost, disabled]),
        PaxText.label(
          "系统 hover / pressed / focus / disabled；键盘 Tab 可达", font: PaxTheme.Typography.caption(),
          secondary: true),
      ])
    let switcher = PaxToggle(label: "开机自动连接", target: self, action: #selector(toggle(_:)))
    let segments = PaxSegmentedControl(labels: ["应用", "域名", "IP"])
    segments.onSelect = { [weak self] index in
      self?.feedback.stringValue = "分段选择：\(["应用", "域名", "IP"][index])"
    }
    let search = NSSearchField()
    search.placeholderString = "原生搜索字段"
    addCard("原生控件", views: [switcher, segments, search])
    let badges = row([
      PaxBadge(title: "● 已保护", tone: .healthy), PaxBadge(title: "△ 降级", tone: .warning),
      PaxBadge(title: "× 失败", tone: .error), PaxBadge(title: "○ 数据陈旧", tone: .neutral),
    ])
    addCard(
      "状态与数值",
      views: [
        badges,
        row([
          PaxMetricView(label: "今日流量", value: "2.40", unit: "GB", context: "截至 14:32"),
          PaxMetricView(
            label: "手动测试延迟", value: "148", unit: "ms", context: "今天 14:28 · 单次测试", compact: true),
          PaxMetricView(label: "尚未提供", value: "—", context: "不把缺失数据转换为零", compact: true),
        ]),
      ])
    addCard(
      "原因与反馈",
      views: [
        PaxNotice(title: "后台服务断开", detail: "保留最后快照，当前网络状态未知。"),
        PaxNotice(title: "保护不完整", detail: "有流量不能证明 IPv6 已接管。", tone: .warning),
        PaxNotice(title: "操作没有生效", detail: "保留原线路，重试不会清空无关统计。", tone: .error),
      ])
    let sampleChart = PaxStackedChart(
      samples: [
        .init("09:00", values: [0.12, 0.04, 0.01]), .init("10:00", values: [0.18, 0.03, 0.01]),
        .init("11:00", values: [0.10, 0.04, 0.02]), .init("12:00", values: [0.24, 0.05, 0.01]),
      ], seriesLabels: ["代理", "直连", "未归类"], unit: "GB / 时")
    sampleChart.tooltipCoordinator = tooltip
    sampleChart.heightAnchor.constraint(equalToConstant: PaxTheme.Chart.height).isActive = true
    let proportion = PaxProportionView(segments: [
      .init("代理", value: 75), .init("直连", value: 21), .init("未归类", value: 4),
    ])
    proportion.heightAnchor.constraint(equalToConstant: PaxTheme.Chart.proportionHeight).isActive =
      true
    let line = PaxSparkline(
      values: [146, 150, 148, 160, 149, 151], summary: "六个延迟样本 146 到 160 ms")
    line.heightAnchor.constraint(equalToConstant: PaxTheme.Chart.sparklineHeight).isActive = true
    addCard(
      "图表",
      views: [
        sampleChart, proportion, PaxText.label("延迟样本 · 146–160 ms", secondary: true), line,
        PaxText.label(
          "点击柱图后使用左右键查看样本；hover 与键盘读取相同数据", font: PaxTheme.Typography.caption(), secondary: true),
      ])
    let funnel = PaxStackedChart(
      samples: (0..<8).map {
        .init(String(format: "%02d:00", $0), values: [Double($0 % 3 + 1), Double($0 + 2) * 3])
      }, seriesLabels: ["上传", "下载"], unit: "MB")
    funnel.style = .funnel
    funnel.colors = [PaxTheme.DashboardColor.upload.last ?? .orange, PaxTheme.DashboardColor.green]
    let days = PaxStackedChart(
      samples: (0..<7).map {
        var day = PaxChartSample("10/\(24 + $0)", values: [Double($0 + 2)])
        day.amountLabel = "$\($0 + 2).00"
        return day
      }, seriesLabels: ["费用"], unit: "USD")
    days.style = .labeledColumns
    let clients = ["Claude Code", "Codex", "DSH", "其他"]
    let shareBar = PaxClientShareBar(
      shares: zip(clients, [6.0, 3, 1, 1]).map { PaxClientShare($0, value: $1) })
    for chart in [funnel, days] {
      chart.tooltipCoordinator = tooltip
      chart.heightAnchor.constraint(equalToConstant: PaxTheme.Chart.height).isActive = true
    }
    addCard(
      "概览 v2 共享图表",
      views: [funnel, days, shareBar, PaxClientLegend(clients: Array(clients.prefix(3)))])
    let table = PaxDataTable(
      columns: [
        .init("对象", width: 240), .init("去向", width: 160), .init("流量", width: 120, numeric: true),
      ],
      rows: [
        .init(id: "safari", values: ["Safari", "代理", "1.32 GB"], subtitle: "网页浏览"),
        .init(id: "unknown", values: ["未知应用", "未归类", "92 MB"], subtitle: "归属信息不足"),
      ])
    table.onActivate = { [weak self] id in self?.feedback.stringValue = "表格行激活：\(id)" }
    table.heightAnchor.constraint(equalToConstant: PaxTheme.Layout.galleryTable).isActive = true
    let timeline = PaxTimeline(entries: [
      .init(id: "check", time: "14:32", title: "保护检查通过", detail: "检查结果", tone: .healthy),
      .init(id: "test", time: "14:28", title: "手动测试完成", detail: "单次测量，不代表实时质量"),
    ])
    timeline.onSelect = { [weak self] id in self?.feedback.stringValue = "事件选中：\(id)" }
    addCard("表格与事件", views: [table, timeline])
    let empty = PaxEmptyState(symbol: "network", title: "还没有线路", caption: "先添加线路，空状态不显示虚构延迟")
    empty.heightAnchor.constraint(equalToConstant: PaxTheme.Layout.emptyState).isActive = true
    let loading = NSProgressIndicator()
    loading.style = .spinning
    loading.startAnimation(nil)
    addCard(
      "空与加载", views: [empty, row([loading, PaxText.label("局部加载不会遮住整个连接操作", secondary: true)])])
    let glass = GlassSurface(interactive: true)
    let button = actionButton("交互式玻璃控制", style: .ghost)
    glass.contentView = button
    glass.heightAnchor.constraint(equalToConstant: PaxTheme.Layout.glassControl).isActive = true
    let close = actionButton("原生 sheet…", style: .secondary)
    close.action = #selector(openSheet)
    addCard(
      "系统材质与浮层",
      views: [
        glass, close,
        PaxText.label(
          "27：交互反馈；26：静态玻璃。降低透明度/增强对比度由系统材质适配。内容卡片保持实底。", secondary: true, wrapping: true),
      ])
  }
  public override func viewDidAppear() {
    super.viewDidAppear()
    guard !initiallyShown else { return }
    initiallyShown = true
    view.window?.makeFirstResponder(buttons.first)
    scroll.contentView.scroll(to: .zero)
    scroll.reflectScrolledClipView(scroll.contentView)
  }
  private func add(_ view: NSView) {
    content.addArrangedSubview(view)
    view.widthAnchor.constraint(equalTo: content.widthAnchor).isActive = true
  }
  private func addCard(_ title: String, views: [NSView]) {
    let stack = NSStackView(views: [PaxSectionHeader(title)] + views)
    stack.orientation = .vertical
    stack.alignment = .leading
    stack.spacing = PaxTheme.Spacing.regular
    for item in stack.arrangedSubviews {
      item.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
    }
    let card = PaxCard()
    card.contentView = stack
    add(card)
  }
  private func row(_ views: [NSView]) -> NSStackView {
    let stack = NSStackView(views: views)
    stack.orientation = .horizontal
    stack.spacing = PaxTheme.Spacing.regular
    stack.alignment = .centerY
    return stack
  }
  private func actionButton(_ title: String, style: PaxButton.Style) -> PaxButton {
    let button = PaxButton(style, title: title, target: self, action: #selector(action(_:)))
    buttons.append(button)
    return button
  }
  @objc private func action(_ sender: NSButton) {
    feedback.stringValue = "已点击：\(sender.title)"
  }
  @objc private func toggle(_ sender: NSSwitch) {
    feedback.stringValue = sender.state == .on ? "本地开关已打开" : "本地开关已关闭"
  }
  @objc private func openSheet() {
    let sheet = WorkbenchSheet(title: "变更确认", explanation: "先给上下文，再展示当前值、新值和影响范围；取消不写入。")
    sheet.addInformation("当前 → 新值", "关闭 → 开启")
    sheet.addView(PaxToggle(label: "仅应用到本次会话"))
    sheet.addAction("应用") { [weak self, weak sheet] in
      self?.feedback.stringValue = "变更已确认"
      sheet?.dismissSheet()
    }
    presentAsSheet(sheet)
  }
}
