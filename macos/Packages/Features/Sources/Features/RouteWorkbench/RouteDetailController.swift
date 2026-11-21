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
    pane.identityActions.arrangedSubviews.last?.setAccessibilityLabel(
      self.interfaceCopy.text("action.routes.close_detail"))
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
  private func observe() {
    withObservationTracking {
      _ = store.info(id, context: self.interfaceCopy.context)
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
      showError(
        RouteWorkbenchStore.EditError(self.interfaceCopy.text("interface_detail.38aeb4ea3c")))
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
      if tab == "raw", store.info(id, context: self.interfaceCopy.context).chain == nil,
        var value = draft
      {
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
      if store.info(id, context: self.interfaceCopy.context).chain == nil {
        try store.synchronizeJSON(&value)
      }
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
    pane.applyTitle(store.node(id)?.name ?? self.interfaceCopy.text("modal.route.detail"))
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
      .init(
        id: "overview", title: self.interfaceCopy.text("ai_page.overview"),
        symbol: "square.grid.2x2"),
      .init(id: "config", title: self.interfaceCopy.text("profile_editor.configuration_tab")),
    ]
    if store.info(id, context: self.interfaceCopy.context).chain == nil {
      detailTabs.append(
        .init(id: "raw", title: self.interfaceCopy.text("interface_detail.ca83663bda")))
    }
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
        ? []
        : [
          button(self.interfaceCopy.text("common.cancel"), #selector(close)),
          button(self.interfaceCopy.text("action.common.save"), #selector(save), primary: true),
        ])
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
    let p = store.info(id, context: self.interfaceCopy.context)
    var metrics = p
    metrics.uploadMB = nil
    metrics.downloadMB = nil
    metrics.activeConnections = nil
    metrics.windowConnections = nil
    metrics.transferSamples = nil
    return OverviewState(
      path: store.path(id, context: self.interfaceCopy.context), current: store.isCurrent(id),
      running: store.isRunning,
      title: store.node(id)?.name ?? self.interfaceCopy.text("modal.route.detail"),
      metrics: metrics,
      availability: [
        store.canTest(id), store.canDownload(id), store.canDiagnose(id), store.canManage,
        store.isDownloading(id), store.sameDraft(draft, original), store.canTestCF(id),
      ],
      captions: [
        store.downloadCaption(id, context: self.interfaceCopy.context),
        store.measurementCaption(id, context: interfaceCopy.context),
        store.physicalCaption(context: interfaceCopy.context),
      ],
      history: store.historyRows(id, context: self.interfaceCopy.context),
      historyCaption: store.measurementHistoryCaption(id, context: self.interfaceCopy.context),
      more: store.hasMoreHistory(id), moreUsage: store.hasMoreUsage(id),
      upload: p.uploadMB, download: p.downloadMB, connections: p.activeConnections,
      windowConnections: p.windowConnections, transfer: p.transferSamples,
      usageCaption: store.dataCaption(id, context: self.interfaceCopy.context),
      certificateKind: store.node(id).map {
        ($0.chain == nil && $0.security == "tls")
          ? "tls" : store.certificateExplanation(id, context: self.interfaceCopy.context)
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
        self.interfaceCopy.text("action.traffic.connection_path"),
        symbol: "point.3.connected.trianglepath.dotted",
        detail: store.isCurrent(id) && store.isRunning
          ? self.interfaceCopy.text("interface_detail.e403ba5798")
          : self.interfaceCopy.text("interface_detail.52a78a6e11")))
    addBody(PaxConnectionPath(nodes: store.path(id, context: self.interfaceCopy.context)))
    if let node = store.node(id), let notice = ProxyRoutePresentation.transportNotice(node) {
      addBody(PaxRouteText.label(notice, secondary: true, wrapping: true))
    }
    let p = store.info(id, context: self.interfaceCopy.context)
    if let value = p.exitLocation {
      section(
        self.interfaceCopy.text("interface_detail.6f16af09ae"), symbol: "globe",
        views: [
          RouteExitInfoView(value: value, historical: false, context: self.interfaceCopy.context)
        ])
    } else if p.exitMessage != nil || p.lastExitLocation != nil {
      let status = PaxRouteText.label(
        p.exitMessage ?? self.interfaceCopy.text("interface_detail.d919bc11a4"),
        font: PaxTheme.Typography.caption(), wrapping: true)
      status.textColor = PaxTheme.Color.warning
      var content: [NSView] = [status]
      if let previous = p.lastExitLocation {
        content.append(
          RouteExitInfoView(value: previous, historical: true, context: self.interfaceCopy.context))
      }
      section(
        self.interfaceCopy.text("interface_detail.6f16af09ae"), symbol: "globe", views: content,
        detail: self.interfaceCopy.text("interface_detail.0b9428d71c"))
    }
  }

  private func certificateSettings() {
    if store.isLive, let node = store.node(id), node.chain == nil,
      node.security == "tls", let connection = store.connectionStore
    {
      if tlsEditor == nil {
        let editor = RouteTLSSettingsViewController(
          store: connection, route: node, embedded: true, context: self.interfaceCopy.context)
        editor.onContentResize = { [weak self] in self?.pane.invalidateContentSize() }
        addChild(editor)
        tlsEditor = editor
      }
      if let editor = tlsEditor {
        editor.loadViewIfNeeded()
        section(
          self.interfaceCopy.text("event.settings.certificate"), symbol: "checkmark.shield",
          views: [editor.view])
      }
    } else {
      let text = store.certificateExplanation(id, context: self.interfaceCopy.context)
      if !text.isEmpty {
        addBody(
          PaxRouteText.label(
            text, font: PaxTheme.Typography.caption(), secondary: true, wrapping: true))
      }
    }
  }
  private func measurementResults() {
    let c = interfaceCopy.context
    let p = store.info(id, context: c)
    let row = PaxRouteMetricRow([
      RouteMetricPresentation.make(
        c.text("interface_detail.c1ef5f1bb7"), "bolt", p.firstConnection,
        kind: .latency, color: p.firstConnection.latencyColor, context: c),
      RouteMetricPresentation.make(
        p.latency.historical
          ? c.text("interface_detail.c5a571f36f")
          : c.text("interface_detail.31421eb9bd"), "timer", p.latency,
        kind: .latency, color: p.latency.latencyColor, context: c),
      RouteMetricPresentation.make(
        c.text("settings.measurement.download"), "arrow.down", p.speed,
        kind: .speed, color: PaxTheme.DashboardColor.green, context: c),
    ])
    row.spacing = PaxTheme.Spacing.regular
    if p.cfIP != nil && p.chain == nil {
      row.addMetric(
        RouteMetricPresentation.make(
          c.text("interface_detail.a360eca405"), "cloud", p.cfLatency, kind: .cf,
          color: p.cfLatency.latencyColor, context: c))
    }
    let tests = NSStackView(views: [
      button(self.interfaceCopy.text("action.routes.lookup_exit"), #selector(queryExit)),
      button(self.interfaceCopy.text("action.routes.test_latency"), #selector(testLatency)),
      button(self.interfaceCopy.text("action.routes.test_download"), #selector(testSpeed)),
    ])
    if store.isLive {
      tests.addArrangedSubview(
        button(self.interfaceCopy.text("action.routes.diagnose"), #selector(diagnoseRoute)))
    }
    if let chain = p.chain {
      if store.supportsCF(chain.front) {
        tests.addArrangedSubview(
          button(self.interfaceCopy.text("action.cf.verify_front"), #selector(frontCF)))
      }
      if store.supportsCF(chain.final) {
        tests.addArrangedSubview(
          button(self.interfaceCopy.text("action.cf.verify_final"), #selector(finalCF)))
      }
    }
    tests.spacing = PaxTheme.Spacing.tight
    if p.cfIP != nil && p.chain == nil {
      tests.addArrangedSubview(
        button(self.interfaceCopy.text("action.cf.test_entry"), #selector(testCF)))
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
        button.title = self.interfaceCopy.text("interface_detail.d3a18faf18")
      }
      if button.action == #selector(queryExit), store.isQueryingExit(id) {
        button.title = self.interfaceCopy.text("interface_detail.d4d18a6e9e")
      }
      if button.action == #selector(testCF), store.isTestingCF(id) {
        button.title = self.interfaceCopy.text("interface_detail.105a9082c3")
      }
      if button.action == #selector(testSpeed), store.isDownloading(id) {
        button.title =
          store.isDownloadQueued(id)
          ? self.interfaceCopy.text("interface_detail.16c1f9b337")
          : self.interfaceCopy.text("interface_detail.4a372f90da")
      }
      if button.action == #selector(testSpeed), !store.isDownloading(id) {
        button.toolTip = self.interfaceCopy.text("interface_detail.332b2cac5f")
      }
      button.isEnabled =
        (button.action == #selector(testLatency) && store.isTestingLatency(id))
        || (button.action == #selector(testSpeed) && store.isDownloading(id))
        || (available && store.sameDraft(draft, original))
      if !available { button.toolTip = self.interfaceCopy.text("interface_detail.cee462daef") }
    }
    var items: [NSView] = [row, tests]
    items.append(
      PaxRouteText.label(
        store.downloadCaption(id, context: c),
        font: PaxTheme.Typography.caption(), secondary: true,
        wrapping: true))
    items.append(
      PaxRouteText.label(
        store.measurementCaption(id, context: c),
        font: PaxTheme.Typography.caption(), secondary: true,
        wrapping: true))
    items.append(
      PaxRouteText.label(
        store.physicalCaption(context: c), font: PaxTheme.Typography.caption(), secondary: true))
    if store.isMeasuring(id) || p.latency.running || p.speed.running {
      let loading = PaxLoadingStateView()
      loading.apply(
        .loading,
        message: p.speed.running
          ? (store.isDownloadQueued(id)
            ? self.interfaceCopy.text("interface_detail.c4ecefc021")
            : self.interfaceCopy.text("interface_detail.34376d8b23"))
          : store.measurementProgressCaption(id, context: self.interfaceCopy.context))
      items.append(loading)
    }
    if p.latency.samples.count >= 3 {
      items.append(
        PaxRouteButton(
          historyOpen
            ? self.interfaceCopy.text("interface_detail.ede1ee8a27")
            : self.interfaceCopy.text(
              "interface_detail.065b02887a", ["arg0": String(describing: p.latency.samples.count)]),
          symbol: "chart.xyaxis.line", style: .quiet, target: self, action: #selector(toggleHistory)
        ))
      if historyOpen {
        items.append(
          trend(
            p.latency, color: PaxTheme.DashboardColor.amber, height: PaxTheme.Routes.chartHeight))
      }
    }
    section(
      self.interfaceCopy.text("interface_detail.b91effb8d3"), symbol: "waveform.path.ecg",
      views: items, detail: self.interfaceCopy.text("interface_detail.13baa1b773"))
  }
  private func measurementHistory() {
    guard store.isLive else { return }
    let records = store.historyRows(id, context: self.interfaceCopy.context)
    let more = store.hasMoreHistory(id)
    if records.isEmpty, let error = store.measurementHistoryError(id) {
      let message = PaxRouteText.label(
        error, font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
      message.textColor = PaxTheme.Color.error
      section(
        self.interfaceCopy.text("interface_detail.45eeea7be8"), symbol: "clock.arrow.circlepath",
        views: [
          message, button(self.interfaceCopy.text("action.logs.reload"), #selector(reloadHistory)),
        ], detail: self.interfaceCopy.text("interface_detail.6aa27f14bf"))
      return
    }
    guard !records.isEmpty || more else { return }
    let toggle = PaxRouteButton(
      measurementHistoryOpen
        ? self.interfaceCopy.text("interface_detail.958304b7fd")
        : self.interfaceCopy.text("interface_detail.336a2914c5"),
      symbol: measurementHistoryOpen ? "chevron.up" : "chevron.down", style: .quiet, target: self,
      action: #selector(toggleMeasurementHistory))
    let heading = NSStackView(views: [
      PaxRouteText.label(
        store.measurementHistoryCaption(id, context: self.interfaceCopy.context),
        font: PaxTheme.Typography.caption(), secondary: true),
      NSView(), toggle,
    ])
    heading.distribution = .fill
    var items: [NSView] = [heading]
    if measurementHistoryOpen {
      let table = PaxDataTable(
        columns: [
          .init(
            self.interfaceCopy.text("interface_detail.07c97746f5"),
            width: PaxTheme.Workbench.directionColumn, growth: 0.5, sortable: false),
          .init(
            self.interfaceCopy.text("interface_detail.79f326be44"),
            width: PaxTheme.Routes.pathIcon * 2, growth: 0.25, sortable: false),
          .init(
            self.interfaceCopy.text("event.field.result"),
            width: PaxTheme.Workbench.directionColumn, numeric: true, growth: 2,
            alignment: .left, sortable: false),
          .init(
            self.interfaceCopy.text("logs.status"), width: PaxTheme.Routes.pathIcon * 2,
            growth: 0.25, sortable: false),
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
            self.interfaceCopy.text("action.common.load_more_records"), symbol: "clock",
            style: .quiet, target: self, action: #selector(moreHistory)))
      }
    }
    section(
      self.interfaceCopy.text("interface_detail.45eeea7be8"), symbol: "clock.arrow.circlepath",
      views: items,
      detail: self.interfaceCopy.text(
        "interface_detail.0adfac2e97", ["arg0": String(describing: records.count)]))
  }
  func controlTextDidChange(_ obj: Notification) { saved = "" }
  private func rawConfiguration() {
    if let chain = store.info(id, context: self.interfaceCopy.context).chain {
      section(
        self.interfaceCopy.text("interface_detail.e021a7287c"),
        symbol: "point.3.connected.trianglepath.dotted",
        views: [
          PaxRouteText.label(
            self.interfaceCopy.text(
              "interface_detail.1e91559d9d",
              ["arg0": String(describing: chain.front), "arg1": String(describing: chain.final)]),
            secondary: true,
            wrapping: true)
        ])
      return
    }
    let format = button(
      self.interfaceCopy.text("action.routes.check_parameters"), #selector(formatJSON))
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
      self.interfaceCopy.text("interface_detail.a9ea76302f"), symbol: "curlybraces",
      views: [
        format, scroll,
        self.interfaceCopy.routeLabel(
          "action.routes.node_editor_note", font: PaxTheme.Typography.caption(), secondary: true),
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
    guard store.info(id, context: self.interfaceCopy.context).chain != nil else { return }
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
      if tab == "raw" && store.info(id, context: self.interfaceCopy.context).chain == nil {
        try store.acceptJSON(&value)
      }
      if store.isLive {
        guard !saving else { return }
        saving = true
        for input in inputs.values { input.isEnabled = false }
        editor?.isEditable = false
        proxyForm?.setEnabled(false, editing: true)
        draft = value
        pane.status.stringValue = self.interfaceCopy.text("profile_editor.saving")
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
            self.saved =
              self.store.isCurrent(self.id)
              ? self.interfaceCopy.text("interface_detail.2a3454c016")
              : self.interfaceCopy.text("interface_detail.1bd91a7d0c")
            self.render()
          } catch { if !Task.isCancelled { self.showError(error) } }
        }
        return
      }
      try store.save(value)
      draft = store.draft(id)
      original = draft
      tab = "overview"
      saved =
        store.info(id, context: self.interfaceCopy.context).override
        ? self.interfaceCopy.text("interface_detail.eb7b38440f")
        : self.interfaceCopy.text("interface_detail.1bd91a7d0c")
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
      pane.status.stringValue = self.interfaceCopy.text("interface_detail.87aa0779c8")
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
      pane.status.stringValue = self.interfaceCopy.text("interface_detail.134f2e2443")
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
    alert.messageText = self.interfaceCopy.text("interface_detail.01aeaa37ee")
    alert.informativeText = self.interfaceCopy.text("interface_detail.c3542282cc")
    alert.addButton(withTitle: self.interfaceCopy.text("profile_editor.keep_editing"))
    alert.addButton(withTitle: self.interfaceCopy.text("interface_detail.9b7824cefa"))
    alert.beginSheetModal(for: window) { [weak self] response in
      guard let self else { return }
      if response == .alertSecondButtonReturn { self.finishDiscard() }
    }
  }
}
