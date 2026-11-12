import AppKit
import Observation
import PaxCore
import PaxUI

@MainActor
final class RouteDetailController: RouteModalController, NSTextFieldDelegate {
  let store: RouteWorkbenchStore
  let id: String
  let tooltip: PaxChartTooltipCoordinator
  var draft: RouteWorkbenchStore.Draft?
  private var original: RouteWorkbenchStore.Draft?
  private var tab = "overview"
  private let tabs = PaxTabBar(style: .detail)
  private let tagsRow = NSStackView()
  private let draftStatus = PaxRouteText.label("未保存", font: PaxTheme.Typography.caption())
  var inputs: [String: NSTextField] = [:]
  private var editor: NSTextView?
  var proxyForm: RouteProxyForm?
  private var saved = ""
  private var dataTask: Task<Void, Never>?
  private var editTask: Task<Void, Never>?
  private var saving = false
  private var historyOpen = false
  private var measurementHistoryOpen = false
  var usageRange = 0
  private let pathBlock = NSStackView()
  private let certificateBlock = NSStackView()
  private let testsBlock = NSStackView()
  private let historyBlock = NSStackView()
  private let usageBlock = NSStackView()
  var usageUpdater: (() -> Void)?
  var usageRefreshTask: Task<Void, Never>?
  private var bodyTarget: NSStackView?
  private var overviewState: OverviewState?
  private var tlsEditor: RouteTLSSettingsViewController?
  var frontChoices: [NodeProfile] = []
  init(
    store: RouteWorkbenchStore, id: String, editing: Bool = false,
    tooltip: PaxChartTooltipCoordinator, preparedDraft: RouteWorkbenchStore.Draft? = nil
  ) {
    self.store = store
    self.id = id
    self.tooltip = tooltip
    if let chain = store.node(id)?.chain {
      frontChoices = store.eligibleFronts(final: chain.finalID)
    }
    draft = preparedDraft ?? store.draft(id)
    original = preparedDraft == nil ? draft : store.draft(id)
    tab = editing ? "config" : "overview"
    super.init(title: store.node(id)?.name ?? "线路详情", size: PaxTheme.Routes.dialog, style: .detail)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    super.loadView()
    pane.identity.addArrangedSubview(tagsRow)
    draftStatus.textColor = PaxTheme.Color.warning
    pane.identityActions.insertArrangedSubview(draftStatus, at: 0)
    pane.identityActions.arrangedSubviews.last?.setAccessibilityLabel("关闭线路详情")
    pane.header.addArrangedSubview(tabs)
    tabs.widthAnchor.constraint(equalTo: pane.header.widthAnchor).isActive = true
    tabs.onSelect = { [weak self] key in self?.selectTab(key) }
    render()
    observe()
    if store.isLive {
      dataTask = Task { [weak self] in
        guard let self else { return }
        await self.store.refreshPhysical()
        await self.store.loadMeasurementHistory(self.id)
        await self.store.setUsagePeriod(
          self.id, period: ["today", "week", "month"][self.usageRange])
      }
    }
    if store.isLive && tab != "overview" && draft?.chainContext.isEmpty != false { loadDraft() }
  }
  private func tags() -> NSView {
    let row = NSStackView()
    row.spacing = PaxTheme.Spacing.xs
    if let n = store.node(id) {
      row.addArrangedSubview(
        PaxFeatureTag(ProxyRoutePresentation.protocolLabel(n), style: .protocolLabel))
    }
    let p = store.info(id)
    if p.cfIP != nil {
      row.addArrangedSubview(PaxFeatureTag("CF", tone: .blue))
    }
    if p.chain != nil { row.addArrangedSubview(PaxFeatureTag("链式代理", tone: .purple)) }
    if store.isCurrent(id) {
      row.addArrangedSubview(PaxFeatureTag("当前线路", symbol: "checkmark", tone: .blue))
    }
    return row
  }
  private func observe() {
    withObservationTracking {
      _ = store.info(id)
      _ = store.currentRouteID
      _ = store.isRunning
      _ = overviewSnapshot()
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in
        guard let self else { return }
        if self.tab == "overview" { self.refreshOverview() }
        self.observe()
      }
    }
  }
  private func selectTab(_ key: String) {
    guard store.canEdit || key == "overview" else {
      showError(RouteWorkbenchStore.EditError("编辑功能尚未接入后台"))
      return
    }
    guard !saving else { return }
    if store.isLive && draft == nil && key != "overview" {
      tab = key
      loadDraft()
      return
    }
    do {
      try capture()
      if tab == "raw", store.info(id).chain == nil, var value = draft {
        try store.acceptJSON(&value)
        draft = value
      }
    } catch {
      showError(error)
      return
    }
    tab = key
    saved = ""
    render()
  }
  private func loadDraft() {
    editTask?.cancel()
    editTask = Task { [weak self] in
      guard let self else { return }
      do {
        let value = try await self.store.loadDraft(self.id)
        try Task.checkCancellation()
        self.draft = value
        self.original = value
        self.render()
      } catch { if !Task.isCancelled { self.showError(error) } }
    }
  }
  private func capture() throws {
    guard var value = draft else { return }
    if tab == "config", let proxyForm {
      let proxy = proxyForm.value()
      value.json = try proxy.document()
      value.name = proxy.displayName
      value.server = proxy.server
      value.port = proxy.port
      value.sni = proxy.serverName
    } else if tab == "config" {
      value.name = inputs["name"]?.stringValue ?? value.name
      value.server = inputs["server"]?.stringValue ?? value.server
      value.port = inputs["port"]?.stringValue ?? value.port
      value.host = inputs["host"]?.stringValue ?? value.host
      value.sni = inputs["sni"]?.stringValue ?? value.sni
      if value.server != value.preferredIP { value.preferredIP = nil }
      if store.info(id).chain == nil { try store.synchronizeJSON(&value) }
    } else if tab == "raw", let editor {
      value.json = editor.string
    }
    draft = value
  }
  private func render() {
    pane.performContentUpdates { renderContent() }
  }
  private func renderContent() {
    tooltip.close()
    draftStatus.isHidden = store.sameDraft(draft, original)
    pane.applyTitleColor(store.isCurrent(id) ? PaxTheme.Routes.blue : PaxTheme.Routes.text)
    pane.applyTitle(store.node(id)?.name ?? "线路详情")
    for child in tagsRow.arrangedSubviews {
      tagsRow.removeArrangedSubview(child)
      child.removeFromSuperview()
    }
    tagsRow.addArrangedSubview(tags())
    pane.clear()
    overviewState = nil
    usageUpdater = nil
    inputs = [:]
    editor = nil
    proxyForm = nil
    pane.status.stringValue = ""
    pane.status.isHidden = true
    var detailTabs: [PaxTabBar.Item] = [
      .init(id: "overview", title: "概览", symbol: "square.grid.2x2"),
      .init(id: "config", title: "配置"),
    ]
    if store.info(id).chain == nil { detailTabs.append(.init(id: "raw", title: "详细参数")) }
    tabs.apply(detailTabs, selected: tab)
    if !saved.isEmpty {
      let label = PaxRouteText.label(saved, font: PaxTheme.Typography.caption())
      label.textColor = PaxTheme.Color.healthy
      pane.add(label)
    }
    switch tab {
    case "config": configuration()
    case "raw": rawConfiguration()
    default: overview()
    }
    pane.setActions(
      tab == "overview"
        ? [] : [button("取消", #selector(close)), button("保存", #selector(save), primary: true)])
  }
  func section(_ title: String, symbol: String, views: [NSView], detail: String = "") {
    let stack = NSStackView(
      views: [PaxRouteSectionHeading(title, symbol: symbol, detail: detail)] + views)
    stack.orientation = .vertical
    stack.alignment = .leading
    stack.spacing = PaxTheme.Spacing.medium
    for v in stack.arrangedSubviews {
      v.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
    }
    let card = PaxCard(style: .routesSection)
    card.contentView = stack
    card.heightAnchor.constraint(
      equalTo: stack.heightAnchor, constant: PaxTheme.Routes.sectionTop * 2
    )
    .isActive = true
    addBody(card)
  }
  private struct OverviewState: Equatable {
    var path: [PaxConnectionPath.Node]
    var current: Bool
    var running: Bool
    var title: String
    var metrics: RouteWorkbenchStore.Presentation
    var availability: [Bool]
    var captions: [String]
    var history: [RouteWorkbenchStore.HistoryRow]
    var historyCaption: String
    var more: Bool
    var moreUsage: Bool
    var upload: Double?
    var download: Double?
    var connections: Int?
    var windowConnections: UInt64?
    var transfer: PaxTimeSeries?
    var usageCaption: String
    var certificateKind: String
  }
  private func overviewSnapshot() -> OverviewState {
    let p = store.info(id)
    var metrics = p
    metrics.uploadMB = nil
    metrics.downloadMB = nil
    metrics.activeConnections = nil
    metrics.windowConnections = nil
    metrics.transferSamples = nil
    return OverviewState(
      path: store.path(id), current: store.isCurrent(id), running: store.isRunning,
      title: store.node(id)?.name ?? "线路详情", metrics: metrics,
      availability: [
        store.canTest(id), store.canDownload(id), store.canDiagnose(id), store.canManage,
        store.isDownloading(id), store.sameDraft(draft, original), store.canTestCF(id),
      ],
      captions: [store.downloadCaption(id), store.measurementCaption(id), store.physicalCaption],
      history: store.historyRows(id), historyCaption: store.measurementHistoryCaption(id),
      more: store.hasMoreHistory(id), moreUsage: store.hasMoreUsage(id),
      upload: p.uploadMB, download: p.downloadMB, connections: p.activeConnections,
      windowConnections: p.windowConnections, transfer: p.transferSamples,
      usageCaption: store.dataCaption(id),
      certificateKind: store.node(id).map {
        ($0.chain == nil && $0.security == "tls") ? "tls" : store.certificateExplanation(id)
      } ?? "missing")
  }
  private func addBody(_ view: NSView) {
    guard let target = bodyTarget else {
      pane.add(view)
      return
    }
    target.addArrangedSubview(view)
    view.widthAnchor.constraint(equalTo: target.widthAnchor).isActive = true
  }
  private func updateBlock(_ block: NSStackView, render: () -> Void) {
    for view in block.arrangedSubviews {
      block.removeArrangedSubview(view)
      view.removeFromSuperview()
    }
    bodyTarget = block
    render()
    bodyTarget = nil
    block.isHidden = block.arrangedSubviews.isEmpty
  }
  private func overview() {
    for block in [pathBlock, certificateBlock, testsBlock, historyBlock, usageBlock] {
      block.orientation = .vertical
      block.alignment = .leading
      block.spacing = PaxTheme.Routes.sectionGap
      pane.add(block)
    }
    refreshOverview()
  }
  private func refreshOverview() {
    let next = overviewSnapshot()
    let old = overviewState
    guard next != old else { return }
    let origin = pane.bodyScroll.contentView.bounds.origin
    pane.performContentUpdates {
      if old == nil || old?.path != next.path || old?.current != next.current
        || old?.running != next.running || old?.metrics.exitLocation != next.metrics.exitLocation
        || old?.metrics.lastExitLocation != next.metrics.lastExitLocation
        || old?.metrics.exitMessage != next.metrics.exitMessage
      {
        updateBlock(pathBlock, render: connectionPath)
      }
      if old == nil || old?.certificateKind != next.certificateKind {
        if old != nil {
          tlsEditor?.removeFromParent()
          tlsEditor = nil
        }
        updateBlock(certificateBlock, render: certificateSettings)
      }
      if old == nil || old?.metrics != next.metrics || old?.availability != next.availability
        || old?.captions != next.captions
      {
        updateBlock(testsBlock, render: measurementResults)
      }
      if old == nil || old?.history != next.history || old?.more != next.more
        || old?.historyCaption != next.historyCaption
      {
        updateBlock(historyBlock, render: measurementHistory)
      }
      if old == nil || old?.upload != next.upload || old?.download != next.download
        || old?.connections != next.connections || old?.windowConnections != next.windowConnections
        || old?.transfer != next.transfer
        || old?.usageCaption != next.usageCaption || old?.moreUsage != next.moreUsage
        || old?.current != next.current
        || old?.running != next.running
      {
        if let usageUpdater {
          usageUpdater()
        } else {
          updateBlock(usageBlock, render: usageRecords)
        }
      }
      pane.invalidateContentSize()
    }
    if old == nil || old?.current != next.current || old?.metrics.cfIP != next.metrics.cfIP
      || old?.metrics.chain != next.metrics.chain
    {
      for view in tagsRow.arrangedSubviews {
        tagsRow.removeArrangedSubview(view)
        view.removeFromSuperview()
      }
      tagsRow.addArrangedSubview(tags())
    }
    pane.applyTitle(next.title)
    pane.applyTitleColor(next.current ? PaxTheme.Routes.blue : PaxTheme.Routes.text)
    overviewState = next
    pane.layoutSubtreeIfNeeded()
    pane.bodyScroll.contentView.scroll(to: origin)
  }
  private func connectionPath() {
    addBody(
      PaxRouteSectionHeading(
        "连接路径", symbol: "point.3.connected.trianglepath.dotted",
        detail: store.isCurrent(id) && store.isRunning ? "当前连接" : "配置路径"))
    addBody(PaxConnectionPath(nodes: store.path(id)))
    if let node = store.node(id), let notice = ProxyRoutePresentation.transportNotice(node) {
      addBody(PaxRouteText.label(notice, secondary: true, wrapping: true))
    }
    let p = store.info(id)
    if let value = p.exitLocation {
      section("出口信息", symbol: "globe", views: [RouteExitInfoView(value: value, historical: false)])
    } else if p.exitMessage != nil || p.lastExitLocation != nil {
      let status = PaxRouteText.label(
        p.exitMessage ?? "本次出口未获取", font: PaxTheme.Typography.caption(), wrapping: true)
      status.textColor = PaxTheme.Color.warning
      var content: [NSView] = [status]
      if let previous = p.lastExitLocation {
        content.append(RouteExitInfoView(value: previous, historical: true))
      }
      section("出口信息", symbol: "globe", views: content, detail: "本次未获取")
    }
  }

  private func certificateSettings() {
    if store.isLive, let node = store.node(id), node.chain == nil,
      node.security == "tls", let connection = store.connectionStore
    {
      if tlsEditor == nil {
        let editor = RouteTLSSettingsViewController(store: connection, route: node, embedded: true)
        editor.onContentResize = { [weak self] in self?.pane.invalidateContentSize() }
        addChild(editor)
        tlsEditor = editor
      }
      if let editor = tlsEditor {
        editor.loadViewIfNeeded()
        section("证书设置", symbol: "checkmark.shield", views: [editor.view])
      }
    } else {
      let text = store.certificateExplanation(id)
      if !text.isEmpty {
        addBody(
          PaxRouteText.label(
            text, font: PaxTheme.Typography.caption(), secondary: true, wrapping: true))
      }
    }
  }
  private func measurementResults() {
    let p = store.info(id)
    let row = PaxRouteMetricRow([
      RouteMetricPresentation.make(
        "首次连接耗时", "bolt", p.firstConnection, kind: .latency, color: p.firstConnection.latencyColor),
      RouteMetricPresentation.make(
        p.latency.historical ? "历史延迟" : "稳定延迟", "timer", p.latency, kind: .latency,
        color: p.latency.latencyColor),
      RouteMetricPresentation.make(
        "下载速度", "arrow.down", p.speed, kind: .speed, color: PaxTheme.DashboardColor.green),
    ])
    row.spacing = PaxTheme.Spacing.regular
    if p.cfIP != nil && p.chain == nil {
      row.addMetric(
        RouteMetricPresentation.make(
          "CF 入口延迟", "cloud", p.cfLatency, kind: .cf, color: p.cfLatency.latencyColor))
    }
    let tests = NSStackView(views: [
      button("查询 IP 地区", #selector(queryExit)),
      button("测试延迟", #selector(testLatency)), button("测试下载速度", #selector(testSpeed)),
    ])
    if store.isLive { tests.addArrangedSubview(button("诊断线路", #selector(diagnoseRoute))) }
    if let chain = p.chain {
      if store.supportsCF(chain.front) {
        tests.addArrangedSubview(button("验证前置 CF", #selector(frontCF)))
      }
      if store.supportsCF(chain.final) {
        tests.addArrangedSubview(button("验证最终 CF", #selector(finalCF)))
      }
    }
    tests.spacing = PaxTheme.Spacing.tight
    if p.cfIP != nil && p.chain == nil {
      tests.addArrangedSubview(button("测试 CF 入口", #selector(testCF)))
    }
    for view in tests.arrangedSubviews {
      guard let button = view as? NSButton else { continue }
      let available =
        button.action == #selector(queryExit)
        ? (store.canQueryExit(id) || store.isQueryingExit(id))
        : button.action == #selector(testLatency)
          ? store.canTest(id)
          : button.action == #selector(testSpeed)
            ? (store.canDownload(id) || store.isDownloading(id))
            : button.action == #selector(diagnoseRoute)
              ? store.canDiagnose(id)
              : button.action == #selector(testCF) && store.isLive
                ? store.canTestCF(id) : store.canManage
      if button.action == #selector(testLatency), store.isTestingLatency(id) {
        button.title = "停止测试"
      }
      if button.action == #selector(queryExit), store.isQueryingExit(id) { button.title = "停止查询" }
      if button.action == #selector(testCF), store.isTestingCF(id) { button.title = "测试中…" }
      if button.action == #selector(testSpeed), store.isDownloading(id) {
        button.title = store.isDownloadQueued(id) ? "取消排队" : "停止测速"
      }
      if button.action == #selector(testSpeed), !store.isDownloading(id) {
        button.toolTip = "每条最多8 MiB、30秒，可随时停止"
      }
      button.isEnabled =
        (button.action == #selector(testLatency) && store.isTestingLatency(id))
        || (button.action == #selector(testSpeed) && store.isDownloading(id))
        || (available && store.sameDraft(draft, original))
      if !available { button.toolTip = "此功能尚未接入后台或正在执行" }
    }
    var items: [NSView] = [row, tests]
    items.append(
      PaxRouteText.label(
        store.downloadCaption(id), font: PaxTheme.Typography.caption(), secondary: true,
        wrapping: true))
    items.append(
      PaxRouteText.label(
        store.measurementCaption(id), font: PaxTheme.Typography.caption(), secondary: true,
        wrapping: true))
    items.append(
      PaxRouteText.label(
        store.physicalCaption, font: PaxTheme.Typography.caption(), secondary: true))
    if store.isMeasuring(id) || p.latency.running || p.speed.running {
      let loading = PaxLoadingStateView()
      loading.apply(
        .loading,
        message: p.speed.running
          ? (store.isDownloadQueued(id) ? "测速排队中，可取消" : "正在下载测速，可停止")
          : store.measurementProgressCaption(id))
      items.append(loading)
    }
    if p.latency.samples.count >= 3 {
      items.append(
        PaxRouteButton(
          historyOpen ? "收起延迟历史" : "延迟历史 · 最近 \(p.latency.samples.count) 次",
          symbol: "chart.xyaxis.line", style: .quiet, target: self, action: #selector(toggleHistory)
        ))
      if historyOpen {
        items.append(
          trend(
            p.latency, color: PaxTheme.DashboardColor.amber, height: PaxTheme.Routes.chartHeight))
      }
    }
    section("测试结果", symbol: "waveform.path.ecg", views: items, detail: "最近一次 · 按项目记录")
  }
  private func measurementHistory() {
    guard store.isLive else { return }
    let records = store.historyRows(id)
    let more = store.hasMoreHistory(id)
    if records.isEmpty, let error = store.measurementHistoryError(id) {
      let message = PaxRouteText.label(
        error, font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
      message.textColor = PaxTheme.Color.error
      section(
        "测量记录", symbol: "clock.arrow.circlepath",
        views: [message, button("重新读取记录", #selector(reloadHistory))], detail: "读取失败")
      return
    }
    guard !records.isEmpty || more else { return }
    let toggle = PaxRouteButton(
      measurementHistoryOpen ? "收起测量记录" : "展开测量记录",
      symbol: measurementHistoryOpen ? "chevron.up" : "chevron.down", style: .quiet, target: self,
      action: #selector(toggleMeasurementHistory))
    let heading = NSStackView(views: [
      PaxRouteText.label(
        store.measurementHistoryCaption(id), font: PaxTheme.Typography.caption(), secondary: true),
      NSView(), toggle,
    ])
    heading.distribution = .fill
    var items: [NSView] = [heading]
    if measurementHistoryOpen {
      let table = PaxDataTable(
        columns: [
          .init("测试时间", width: PaxTheme.Workbench.directionColumn, growth: 0.5, sortable: false),
          .init("项目", width: PaxTheme.Routes.pathIcon * 2, growth: 0.25, sortable: false),
          .init(
            "结果", width: PaxTheme.Workbench.directionColumn, numeric: true, growth: 2,
            alignment: .left, sortable: false),
          .init("状态", width: PaxTheme.Routes.pathIcon * 2, growth: 0.25, sortable: false),
        ], selection: .none)
      table.setTableIdentifier("route-measurement-history")
      table.cellProvider = { rowID, column in
        guard let row = records.first(where: { $0.id == rowID }) else { return nil }
        if column == 3 {
          return PaxBadge(
            title: row.status, tone: row.failed ? .error : row.stale ? .neutral : .healthy)
        }
        if column == 2 {
          let text = PaxRouteText.label(
            row.result, font: PaxTheme.Typography.tableValue,
            secondary: row.stale && !row.failed)
          if row.failed { text.textColor = PaxTheme.Color.error }
          text.toolTip = row.result
          return text
        }
        return nil
      }
      table.apply(
        rows: records.map { .init(id: $0.id, values: [$0.time, $0.metric, $0.result, $0.status]) })
      let height = min(
        PaxTheme.Workbench.fullTable,
        PaxTheme.Table.header + CGFloat(max(1, records.count)) * PaxTheme.Table.row)
      table.heightAnchor.constraint(greaterThanOrEqualToConstant: height).isActive = true
      items.append(table)
      if more {
        items.append(
          PaxRouteButton(
            "加载更多记录", symbol: "clock", style: .quiet, target: self, action: #selector(moreHistory)))
      }
    }
    section("测量记录", symbol: "clock.arrow.circlepath", views: items, detail: "\(records.count) 条")
  }
  func controlTextDidChange(_ obj: Notification) { saved = "" }
  private func rawConfiguration() {
    if let chain = store.info(id).chain {
      section(
        "节点引用", symbol: "point.3.connected.trianglepath.dotted",
        views: [
          PaxRouteText.label(
            "前置：\(chain.front)\n最终：\(chain.final)\n\n协议参数在各自线路的详细参数中编辑。", secondary: true,
            wrapping: true)
        ])
      return
    }
    let format = button("检查参数", #selector(formatJSON))
    let scroll = NSScrollView()
    scroll.hasVerticalScroller = true
    scroll.borderType = .bezelBorder
    let editor = NSTextView()
    editor.isRichText = false
    editor.font = .monospacedSystemFont(ofSize: PaxTheme.Routes.bodyFont, weight: .regular)
    editor.string = draft?.json ?? "{}"
    editor.isAutomaticQuoteSubstitutionEnabled = false
    editor.isAutomaticDashSubstitutionEnabled = false
    editor.isVerticallyResizable = true
    editor.isHorizontallyResizable = false
    editor.textContainer?.widthTracksTextView = true
    scroll.documentView = editor
    editor.frame = NSRect(
      origin: .zero,
      size: NSSize(
        width: preferredContentSize.width - PaxTheme.Spacing.page * 2,
        height: PaxTheme.Workbench.details))
    scroll.heightAnchor.constraint(equalToConstant: PaxTheme.Workbench.details).isActive = true
    self.editor = editor
    section(
      "节点参数", symbol: "curlybraces",
      views: [
        format, scroll,
        PaxRouteText.label(
          "仅编辑当前节点，保存时检查基础字段。", font: PaxTheme.Typography.caption(), secondary: true),
      ])
  }
  @objc func chooseCF() {
    do { try capture() } catch {
      showError(error)
      return
    }
    guard let draft else { return }
    presentRouteModal(
      RouteCFController.make(store: store, draft: draft) { [weak self] candidate in
        guard let self else { return }
        self.draft?.server = candidate.ip
        self.draft?.cf = true
        self.draft?.preferredIP =
          candidate.measurement.value != nil && candidate.measurement.failure == nil
          ? candidate.ip : nil
        do {
          if var d = self.draft {
            try self.store.synchronizeJSON(&d)
            self.draft = d
          }
          self.render()
        } catch { self.showError(error) }
      })
  }
  @objc func cfEnabled(_ sender: NSButton) { draft?.cf = sender.state == .on }
  override func viewWillAppear() {
    super.viewWillAppear()
    startUsageRefresh()
  }
  override func viewDidDisappear() {
    super.viewDidDisappear()
    store.refresh?.pageDidDisappear(.routeDetail)
    dataTask?.cancel()
    dataTask = nil
    usageRefreshTask?.cancel()
    usageRefreshTask = nil
  }
  @objc func frontChanged(_ sender: NSPopUpButton) {
    guard store.info(id).chain != nil else { return }
    let routes = frontChoices
    if routes.indices.contains(sender.indexOfSelectedItem) {
      do { try capture() } catch {
        showError(error)
        return
      }
      draft?.frontID = routes[sender.indexOfSelectedItem].id
      draft?.frozenFront = routes[sender.indexOfSelectedItem]
      render()
    }
  }
  @objc private func save() {
    do {
      try capture()
      guard var value = draft else { return }
      if tab == "raw" && store.info(id).chain == nil { try store.acceptJSON(&value) }
      if store.isLive {
        guard !saving else { return }
        saving = true
        for input in inputs.values { input.isEnabled = false }
        editor?.isEditable = false
        proxyForm?.setEnabled(false, editing: true)
        draft = value
        pane.status.stringValue = "正在保存…"
        pane.status.isHidden = false
        editTask = Task { [weak self] in
          guard let self else { return }
          defer {
            self.saving = false
            for input in self.inputs.values { input.isEnabled = true }
            self.editor?.isEditable = true
            self.proxyForm?.setEnabled(true, editing: true)
          }
          do {
            try await self.store.saveDocument(value)
            try Task.checkCancellation()
            self.draft = nil
            self.original = nil
            self.tab = "overview"
            self.saved = self.store.isCurrent(self.id) ? "已保存，重新连接后生效" : "已保存"
            self.render()
          } catch { if !Task.isCancelled { self.showError(error) } }
        }
        return
      }
      try store.save(value)
      draft = store.draft(id)
      original = draft
      tab = "overview"
      saved = store.info(id).override ? "已保存 · 本地覆盖" : "已保存"
      render()
    } catch { showError(error) }
  }
  @objc private func formatJSON() {
    do {
      try capture()
      guard var value = draft else { return }
      try store.acceptJSON(&value)
      draft = value
      editor?.string = value.json
      pane.status.stringValue = "JSON 基础检查通过"
      pane.status.isHidden = false
    } catch { showError(error) }
  }
  @objc func recordFragmentChanged(_ sender: NSButton) {
    do {
      try capture()
      draft?.recordFragment = sender.state == .on
      if var value = draft {
        try store.synchronizeJSON(&value)
        draft = value
      }
      saved = ""
      draftStatus.isHidden = store.sameDraft(draft, original)
    } catch { showError(error) }
  }
  @objc private func queryExit() {
    if store.isQueryingExit(id) {
      store.stopExitQueries(id)
    } else {
      store.perform(.test(id, .exit))
    }
  }
  @objc private func testLatency() {
    if store.isTestingLatency(id) {
      store.stopTests(id)
    } else {
      store.perform(.test(id, .latency))
    }
  }
  @objc private func diagnoseRoute() { store.diagnose(id) }
  @objc private func testSpeed() {
    if store.isDownloading(id) {
      store.stopDownload(id)
      return
    }
    store.perform(.test(id, .speed))
  }
  @objc private func frontCF() { chainCF(front: true) }
  @objc private func finalCF() { chainCF(front: false) }
  private func chainCF(front: Bool) {
    guard let chain = store.node(id)?.chain, let route = store.node(id) else { return }
    let member = front ? chain.frontID : chain.finalID
    Task { [weak self] in
      guard let self else { return }
      do {
        var draft = try await self.store.loadDraft(member)
        draft.chainContext = self.id
        draft.chainRevision = route.revision
        let frozen = draft
        let picker = RouteCFController.make(store: self.store, draft: draft) {
          [weak self] candidate in
          guard let self else { return }
          var applied = frozen
          applied.server = candidate.ip
          applied.cf = true
          applied.preferredIP = candidate.ip
          do { try self.store.synchronizeJSON(&applied) } catch {
            self.showError(error)
            return
          }
          self.presentAsSheet(
            RouteDetailController(
              store: self.store, id: member, editing: true,
              tooltip: self.tooltip, preparedDraft: applied))
        }
        self.presentAsSheet(picker)
      } catch { self.showError(error) }
    }
  }
  @objc private func testCF() { store.perform(.test(id, .cf)) }
  @objc private func toggleMeasurementHistory() {
    measurementHistoryOpen.toggle()
    pane.performContentUpdates {
      updateBlock(historyBlock, render: measurementHistory)
      pane.invalidateContentSize()
    }
  }
  @objc private func toggleHistory() {
    historyOpen.toggle()
    updateBlock(testsBlock, render: measurementResults)
    pane.invalidateContentSize()
  }
  private func finishDiscard() {
    editTask?.cancel()
    draft = nil
    original = nil
    super.close()
  }
  override func close() {
    guard !saving else {
      pane.status.stringValue = "正在等待后台保存结果，请稍候"
      pane.status.isHidden = false
      return
    }
    do { try capture() } catch { showError(error) }
    guard !store.sameDraft(draft, original), let window = view.window else {
      editTask?.cancel()
      draft = nil
      original = nil
      super.close()
      return
    }
    let alert = NSAlert()
    alert.messageText = "放弃未保存的修改？"
    alert.informativeText = "关闭后，本次修改不会保存。"
    alert.addButton(withTitle: "继续编辑")
    alert.addButton(withTitle: "放弃修改")
    alert.beginSheetModal(for: window) { [weak self] response in
      guard let self else { return }
      if response == .alertSecondButtonReturn { self.finishDiscard() }
    }
  }
}
