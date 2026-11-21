import AppKit
import Observation
import PaxCore
import PaxUI

/// 分流页只转发用户意图；实际连接与配置由隔离 Store 提供。
@MainActor
final class RoutingWorkbenchViewController: NSViewController, InterfaceLanguageConsumer {
  let interfaceCopy = InterfaceCopyBindings()
  let objectIcons: WorkbenchObjectIcons
  private var detailTask: Task<Void, Never>?
  private var pageVisible = false
  private var pageRefresh: PaxRefresh?
  private var entryPending = false
  private var visibleRefreshKey: String?
  private var pendingTrafficGeneration: UInt64?
  private var liveConnection: ConnectionStore?
  let store: RoutingWorkbenchStore
  private let tooltip: PaxChartTooltipCoordinator
  private let mode = PaxTabs(["代理", "直连"], style: .mode)
  private let tabs = PaxTabBar(style: .detail)
  private let dimension = PaxTabs(["应用", "域名", "系统与进程"])
  private let direction = PaxTabs(["全部", "代理", "直连"])
  private let period = PaxRoutePicker(style: .toolbar)
  private let search = PaxRouteSearchField()
  private let notice = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private let subtotalNote = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private lazy var footer = NSStackView(
    views: [subtotalNote, unknownSource, dnsSummary, notice, NSView()])
  private let content = NSView()
  private let traffic = NSView()
  private let rules = NSView()
  private lazy var packagesController = RoutingPackagesController(
    store: store, objectIcons: objectIcons)
  private let dnsSummary = RoutingDNSSummary(frame: .zero)
  private lazy var unknownSource = PaxRouteButton(
    "", style: .quiet, target: self, action: #selector(showUnknownSource))
  private var activeTrafficTable: PaxDataTable { list.activeTable }
  private let tableHost = RoutingTrafficHost()
  private let list: RoutingTrafficList
  init(
    session: WorkbenchDemoStore, tooltip: PaxChartTooltipCoordinator, backendAvailable: Bool = true,
    objectIcons: WorkbenchObjectIcons = WorkbenchObjectIcons(),
    initialPeriod: RoutingWorkbenchStore.Period = .minute
  ) {
    self.objectIcons = objectIcons
    store = RoutingWorkbenchStore(
      session: session, backendAvailable: backendAvailable, initialPeriod: initialPeriod)
    self.tooltip = tooltip
    list = RoutingTrafficList(store: store, icons: objectIcons, tooltip: tooltip)
    super.init(nibName: nil, bundle: nil)
  }
  private func applyLanguage() {
    mode.apply(labels: [interfaceCopy.text("common.proxy"), interfaceCopy.text("common.direct")])
    let context = interfaceCopy.context
    dimension.apply(
      labels: RoutingWorkbenchStore.Dimension.allCases.map { $0.title(context: context) })
    direction.apply(labels: [
      interfaceCopy.text("common.all"), interfaceCopy.text("common.proxy"),
      interfaceCopy.text("common.direct"),
    ])
    tabs.apply(
      [
        .init(id: "traffic", title: interfaceCopy.text("ai_page.overview")),
        .init(id: "rules", title: interfaceCopy.text("traffic.rules")),
      ], selected: store.tab == .traffic ? "traffic" : "rules")
    list.applyLanguage(interfaceCopy.context)
    render()
    activeTrafficTable.refreshVisibleCopy()
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  convenience init(
    telemetry: TelemetryStore, connection: ConnectionStore? = nil,
    tooltip: PaxChartTooltipCoordinator, objectIcons: WorkbenchObjectIcons = WorkbenchObjectIcons(),
    refresh: PaxRefresh? = nil
  ) {
    self.init(
      session: WorkbenchDemoStore(), tooltip: tooltip, backendAvailable: false,
      objectIcons: objectIcons, initialPeriod: .day)
    store.bind(telemetry, connection)
    liveConnection = connection
    list.collecting = { [weak connection] in connection?.proxyEnabled == true }
    pageRefresh = refresh
    refresh?.bind(.traffic) { [weak self] in self?.fetchLive() }
  }
  override func viewWillAppear() {
    super.viewWillAppear()
    pageVisible = true
    entryPending = true
    syncLiveRefresh()
    pageRefresh?.pageDidAppear(.traffic)
  }
  override func viewWillDisappear() {
    detailTask?.cancel()
    super.viewWillDisappear()
    pageVisible = false
    pageRefresh?.pageDidDisappear(.traffic)
    syncLiveRefresh()
  }
  /// PaxRefresh 的拉取：进页回到最新；之后随流量推送（最小 60s）换最新，用户翻页 / 选中后冻结。
  private func fetchLive() {
    guard let telemetry = store.telemetry, telemetry.isShown(.traffic, focus: false) else { return }
    let entry = entryPending
    entryPending = false
    if store.tab == .traffic {
      if entry {
        store.liveFrozen = false
        store.livePaginated = false
      }
      store.scheduleLive(force: true, automatic: !entry)
    } else if entry {
      store.reloadLibrary()
    }
  }
  private func syncLiveRefresh() {
    guard store.telemetry != nil else { return }
    guard let telemetry = store.telemetry else { return }
    let visible =
      pageVisible && telemetry.isShown(.traffic, focus: pageRefresh == nil, tab: false)
      && telemetry.connected
    let key = visible ? "\(store.tab.rawValue):\(telemetry.sessionGeneration)" : nil
    guard visibleRefreshKey != key else { return }
    let wasVisible = visibleRefreshKey != nil
    visibleRefreshKey = key
    store.stopLive()
    store.stopLibraryRead()
    guard visible else { return }
    // 有 PaxRefresh 时，进页 / 重连的读取由它触发；页内切标签仍在这里直接读。
    if pageRefresh != nil && !wasVisible { return }
    if store.tab == .traffic {
      store.liveFrozen = false
      store.scheduleLive(force: true)
    } else {
      store.reloadLibrary()
    }
  }

  override func loadView() {
    let surface = WorkbenchSurface()
    surface.surfaceColor = PaxTheme.Color.workspace
    view = surface
    let header = PaxPageHeader(
      title: self.interfaceCopy.text("navigation.traffic"),
      accessory: ProxyStatusAccessory(mode: mode, connection: liveConnection, presenter: self))
    interfaceCopy.bind(header, key: "navigation.traffic") { $0.apply(title: $1) }
    interfaceCopy.onChange = { [weak self] in self?.applyLanguage() }
    mode.onSelect = { [weak self] index in
      if let self, let connection = self.liveConnection {
        self.mode.selectedIndex = connection.proxyEnabled ? 0 : 1
        ProxyTransitionPresenter.request(from: self, connection: connection, enabled: index == 0)
        return
      }
      guard let self, self.store.canEdit, (index == 0) != self.store.session.isRunning else {
        return
      }
      self.store.session.perform(.toggleProxy)
    }
    tabs.apply(
      [
        .init(id: "traffic", title: interfaceCopy.text("ai_page.overview")),
        .init(id: "rules", title: interfaceCopy.text("traffic.rules")),
      ], selected: "traffic")
    tabs.onSelect = { [weak self] id in
      self?.store.perform(.tab(id == "traffic" ? .traffic : .rules))
    }
    buildTraffic()
    buildRules()
    for page in [traffic, rules] {
      page.translatesAutoresizingMaskIntoConstraints = false
      content.addSubview(page)
      NSLayoutConstraint.activate([
        page.leadingAnchor.constraint(equalTo: content.leadingAnchor),
        page.trailingAnchor.constraint(equalTo: content.trailingAnchor),
        page.topAnchor.constraint(equalTo: content.topAnchor),
        page.bottomAnchor.constraint(equalTo: content.bottomAnchor),
      ])
    }
    footer.alignment = .centerY
    footer.spacing = PaxTheme.Spacing.tight
    notice.lineBreakMode = .byTruncatingTail
    notice.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    subtotalNote.lineBreakMode = .byTruncatingTail
    unknownSource.setAccessibilityIdentifier("routing-unknown-details")
    interfaceCopy.button(unknownSource, key: "traffic.unknown_details")
    let stack = NSStackView(views: [header, tabs, content, footer])
    stack.orientation = .vertical
    stack.alignment = .leading
    stack.spacing = PaxTheme.Spacing.medium
    stack.translatesAutoresizingMaskIntoConstraints = false
    view.addSubview(stack)
    for item in stack.arrangedSubviews {
      item.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
    }
    NSLayoutConstraint.activate([
      stack.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: PaxTheme.Spacing.page),
      stack.trailingAnchor.constraint(
        equalTo: view.trailingAnchor, constant: -PaxTheme.Spacing.page),
      stack.topAnchor.constraint(
        equalTo: view.safeAreaLayoutGuide.topAnchor, constant: PaxTheme.Spacing.pageTop),
      stack.bottomAnchor.constraint(equalTo: view.bottomAnchor, constant: -PaxTheme.Spacing.page),
      header.heightAnchor.constraint(equalToConstant: PaxTheme.Dashboard.pageHeader),
      content.heightAnchor.constraint(greaterThanOrEqualToConstant: PaxTheme.Routing.minimumTable),
    ])
    observe()
  }
  private func buildTraffic() {
    dimension.setAccessibilityIdentifier("routing-dimension")
    direction.setAccessibilityIdentifier("routing-direction")
    mode.setAccessibilityIdentifier("routing-power")
    dimension.onSelect = { [weak self] index in
      if let value = RoutingWorkbenchStore.Dimension(rawValue: index) {
        self?.detailTask?.cancel()
        self?.store.perform(.dimension(value))
      }
    }
    direction.onSelect = { [weak self] index in
      if let value = RoutingWorkbenchStore.Direction(rawValue: index) {
        self?.store.perform(.direction(value))
      }
    }
    interfaceCopy.bind(search, key: "action.traffic.search") {
      $0.placeholderString = $1
      $0.setAccessibilityLabel($1)
    }
    search.sendsSearchStringImmediately = true
    search.target = self
    search.action = #selector(searchChanged)
    search.setAccessibilityIdentifier("routing-search")
    let tools = NSStackView(views: [dimension, direction, NSView(), search, period])
    tools.spacing = PaxTheme.Spacing.tight
    search.widthAnchor.constraint(lessThanOrEqualToConstant: PaxTheme.Layout.searchWidth)
      .isActive = true
    search.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    search.heightAnchor.constraint(equalToConstant: PaxTheme.Routes.controlHeight).isActive = true
    let periods = RoutingWorkbenchStore.Period.allCases
    period.addItems(withTitles: periods.map { $0.title(context: interfaceCopy.context) })
    interfaceCopy.bind(period) { picker, context in
      for (index, period) in periods.enumerated() {
        picker.item(at: index)?.title = period.title(context: context)
      }
    }
    period.target = self
    period.action = #selector(periodChanged)
    period.setAccessibilityIdentifier("routing-period")
    interfaceCopy.bind(period, key: "action.traffic.window_ax") { $0.setAccessibilityLabel($1) }
    let card = PaxCard(insets: NSEdgeInsets())
    card.clipsContent = true
    let minimum = card.heightAnchor.constraint(
      greaterThanOrEqualToConstant: PaxTheme.Routing.minimumTable)
    minimum.priority = .defaultHigh
    minimum.isActive = true
    card.contentView = tableHost
    tableHost.bottomAnchor.constraint(equalTo: card.bottomAnchor).isActive = true
    list.systemTable.isHidden = true
    tableHost.show(list.table)
    list.onOpen = { [weak self] id in self?.showTraffic(id) }
    dnsSummary.onOpen = { [weak self] in
      if let id = self?.store.livePage.dnsSummary?.id { self?.showTraffic(id) }
    }
    let stack = NSStackView(views: [tools, card])
    mount(stack, in: traffic)
  }
  private func buildRules() {
    let controller = packagesController
    addChild(controller)
    let page = controller.view
    page.translatesAutoresizingMaskIntoConstraints = false
    rules.addSubview(page)
    NSLayoutConstraint.activate([
      page.leadingAnchor.constraint(equalTo: rules.leadingAnchor),
      page.trailingAnchor.constraint(equalTo: rules.trailingAnchor),
      page.topAnchor.constraint(equalTo: rules.topAnchor),
      page.bottomAnchor.constraint(equalTo: rules.bottomAnchor),
    ])
  }
  func mount(_ stack: NSStackView, in parent: NSView) {
    stack.orientation = .vertical
    stack.alignment = .leading
    stack.spacing = PaxTheme.Spacing.medium
    stack.translatesAutoresizingMaskIntoConstraints = false
    parent.addSubview(stack)
    for item in stack.arrangedSubviews {
      item.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
    }
    NSLayoutConstraint.activate([
      stack.leadingAnchor.constraint(equalTo: parent.leadingAnchor),
      stack.trailingAnchor.constraint(equalTo: parent.trailingAnchor),
      stack.topAnchor.constraint(equalTo: parent.topAnchor),
      stack.bottomAnchor.constraint(equalTo: parent.bottomAnchor),
    ])
  }
  private func observe() {
    withObservationTracking { [weak self] in
      self?.syncLiveRefresh()
      self?.render()
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observe() }
    }
  }
  private func render() {
    if let telemetry = store.telemetry,
      !telemetry.isShown(.traffic, focus: pageRefresh == nil, tab: false)
    {
      return
    }

    let modeIndex = (liveConnection?.proxyEnabled ?? store.session.isRunning) ? 0 : 1
    let modeEnabled = liveConnection?.canToggleProxy ?? store.canEdit
    if mode.selectedIndex != modeIndex { mode.selectedIndex = modeIndex }
    if mode.isEnabled != modeEnabled { mode.isEnabled = modeEnabled }
    if dimension.selectedIndex != store.dimension.rawValue {
      dimension.selectedIndex = store.dimension.rawValue
    }
    if direction.selectedIndex != store.direction.rawValue {
      direction.selectedIndex = store.direction.rawValue
    }
    let periodIndex = RoutingWorkbenchStore.Period.allCases.firstIndex(of: store.period) ?? 0
    if period.indexOfSelectedItem != periodIndex { period.selectItem(at: periodIndex) }
    if search.currentEditor() == nil, search.stringValue != store.search {
      search.stringValue = store.search
    }
    tabs.apply(
      [
        .init(id: "traffic", title: interfaceCopy.text("ai_page.overview")),
        .init(id: "rules", title: interfaceCopy.text("traffic.rules")),
      ],
      selected: store.tab == .traffic ? "traffic" : "rules")
    let showingTraffic = store.tab == .traffic
    if traffic.isHidden == showingTraffic { traffic.isHidden = !showingTraffic }
    if rules.isHidden != showingTraffic { rules.isHidden = showingTraffic }
    let message = store.stateMessage(context: interfaceCopy.context)
    if notice.stringValue != message {
      notice.stringValue = message
      notice.toolTip = message.isEmpty ? nil : message
    }
    if notice.isHidden != message.isEmpty { notice.isHidden = message.isEmpty }
    tableHost.show(activeTrafficTable)
    let page = store.livePage
    let showingDNS = showingTraffic && store.dimension == .system
    if dnsSummary.isHidden == showingDNS { dnsSummary.isHidden = !showingDNS }
    if showingDNS {
      dnsSummary.update(
        page.dnsSummary,
        unavailable: store.liveLoading
          ? self.interfaceCopy.text("interface_detail.c039e85d8a")
          : store.liveError.isEmpty ? nil : self.interfaceCopy.text("interface_detail.089bd0e27f"),
        context: interfaceCopy.context)
    }
    let showingApplication = showingTraffic && store.dimension == .application
    let unknown = showingApplication ? page.unknownSource : nil
    if unknownSource.isHidden != (unknown == nil) { unknownSource.isHidden = unknown == nil }
    let note =
      showingApplication
      ? RoutingWorkbenchStore.subtotalNote(page, context: interfaceCopy.context) : nil
    if subtotalNote.isHidden != (note == nil) { subtotalNote.isHidden = note == nil }
    if subtotalNote.stringValue != (note ?? "") {
      subtotalNote.stringValue = note ?? ""
      subtotalNote.toolTip = note
    }
    footer.isHidden = !showingDNS && note == nil && unknown == nil && message.isEmpty
    if let unknown {
      let title =
        self.interfaceCopy.text(
          "interface_detail.8c944370ef",
          [
            "arg0": String(describing: unknown.connections),
            "arg1": String(describing: TrafficFormatting.bytes(unknown.upload)),
            "arg2": String(describing: TrafficFormatting.bytes(unknown.download)),
          ])
      if unknownSource.toolTip != title { unknownSource.toolTip = title }
    }
    if store.telemetry != nil {
      if list.render(emptyMessage: list.emptyMessage(unknownSourceBelow: true)) {
        let generation = store.liveGeneration
        pendingTrafficGeneration = generation
        DispatchQueue.main.async { [weak self] in
          guard let self, self.store.liveGeneration == generation,
            self.pendingTrafficGeneration == generation
          else { return }
          self.pendingTrafficGeneration = nil
          self.activeTrafficTable.isHidden = false
        }
      }
      if pendingTrafficGeneration != store.liveGeneration { pendingTrafficGeneration = nil }
      if pendingTrafficGeneration == nil, activeTrafficTable.isHidden {
        activeTrafficTable.isHidden = false
      }
      return
    }
    activeTrafficTable.isHidden = false
    list.render(emptyMessage: list.emptyMessage(unknownSourceBelow: true))
  }
  func trafficCell(_ id: String, column: Int) -> NSView? { list.cell(id, column: column) }
  func focusSearch() {
    if store.tab == .rules {
      packagesController.focusSearch()
    } else {
      view.window?.makeFirstResponder(search)
    }
  }
  @objc private func showUnknownSource() {
    if let id = store.livePage.unknownSource?.id { showTraffic(id) }
  }
  @objc private func searchChanged() { store.perform(.search(search.stringValue)) }
  @objc private func periodChanged() {
    let values = RoutingWorkbenchStore.Period.allCases
    if values.indices.contains(period.indexOfSelectedItem) {
      store.perform(.period(values[period.indexOfSelectedItem]))
    }
  }
  private func showTraffic(_ id: String) {
    if store.telemetry != nil {
      store.liveFrozen = true
      detailTask?.cancel()
      detailTask = list.loadDetail(
        id, visibleOn: .traffic,
        present: { [weak self] in self?.presentWorkbenchModal($0) },
        fail: { [weak self] in self?.notice.stringValue = $0 })
      return
    }

    guard presentedViewControllers?.isEmpty != false,
      let row = store.visibleRows.first(where: { $0.id == id })
    else { return }
    tooltip.close()
    let hits = row.hits
    let controller = WorkbenchTrafficDetailController(
      snapshot: store.trafficDetail(for: row), tooltip: tooltip, icons: objectIcons,
      onLocate: { [weak store] key in
        guard let store, let index = Int(key), hits.indices.contains(index) else {
          return self.interfaceCopy.text("interface_detail.6a861b4be6")
        }
        store.perform(.locate(hits[index]))
        return store.tab == .rules ? nil : store.packageMessage
      })
    presentWorkbenchModal(controller)
  }
}
