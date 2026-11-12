import AppKit
import Observation
import PaxCore
import PaxUI

/// 当前原生线路页：只通过隔离目录 Store 发产品意图。
@MainActor
final class RouteWorkbenchViewController: NSViewController {
  private var importButton: PaxRouteButton?
  private var createButton: PaxRouteButton?
  private var revealRouteID: String?
  private let certificateButton = PaxRouteButton(
    "处理证书异常", symbol: "exclamationmark.shield", style: .quiet)
  private var groupButton: PaxRouteButton?
  let store: RouteWorkbenchStore
  let tooltip: PaxChartTooltipCoordinator
  private let table = PaxRouteTable()
  private var displayedGroup: String?
  private var displayedOrder: [String] = []
  private var orderingKey = ""
  private var readingList = false
  private let tabs = PaxTabBar()
  private let search = PaxRouteSearchField()
  private let resetSort = PaxRouteButton("默认排序", symbol: "arrow.uturn.backward", style: .quiet)
  private let notice = PaxRouteText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private let tunNotice = PaxRouteText.label(
    "", font: PaxTheme.Typography.caption(), secondary: true)
  private let empty = PaxEmptyState(
    symbol: "tray", title: "暂无线路", caption: "新增代理线路，或导入分享链接与订阅", identifier: "routes-empty")
  let refreshButton = PaxRouteButton("刷新订阅", symbol: "arrow.clockwise", style: .quiet)
  let testButton = PaxRouteButton("测试线路", symbol: "waveform.path.ecg")
  private let pathButton = PaxRouteButton("线路诊断", symbol: "network", style: .quiet)
  private let reconnectButton = PaxRouteButton(
    "重新连接", symbol: "arrow.clockwise.circle", style: .quiet)
  private let mode = PaxTabs(["代理", "直连"], style: .mode)
  let destructiveTitles = PaxMenuDestructiveTitles()
  convenience init(session: WorkbenchDemoStore, tooltip: PaxChartTooltipCoordinator) {
    self.init(store: RouteWorkbenchStore(session: session), tooltip: tooltip)
  }
  init(store: RouteWorkbenchStore, tooltip: PaxChartTooltipCoordinator) {
    self.store = store
    self.tooltip = tooltip
    super.init(nibName: nil, bundle: nil)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    let surface = WorkbenchSurface()
    surface.surfaceColor = PaxTheme.Routes.canvas
    view = surface
    let header = PaxPageHeader(
      title: "线路管理",
      accessory: ProxyStatusAccessory(
        mode: mode, connection: store.connectionStore, presenter: self))
    mode.onSelect = { [weak self] index in
      guard let self else { return }
      self.mode.selectedIndex = self.store.isRunning || self.store.isStarting ? 0 : 1
      if let connection = self.store.connectionStore {
        ProxyTransitionPresenter.request(
          from: self, connection: connection, enabled: index == 0)
      } else {
        self.store.toggleProxy()
      }
    }
    mode.setAccessibilityIdentifier("routes-power")
    let manage = PaxRouteButton(
      "管理分组", symbol: "slider.horizontal.3", style: .quiet, target: self,
      action: #selector(manageGroups))
    let add = PaxRouteButton(
      "导入线路", style: .secondary, target: self, action: #selector(importRoutes))
    add.image = PaxRouteIcon.image("square.and.arrow.down")
    add.imagePosition = .imageLeading
    add.setAccessibilityIdentifier("route-import")
    manage.setAccessibilityIdentifier("route-groups")
    manage.isEnabled = store.canManageGroups
    groupButton = manage
    importButton = add
    add.isEnabled = store.canImport
    if !store.canManageGroups {
      manage.toolTip = "当前无法管理分组，请检查后台连接"
      if !store.canImport { add.toolTip = "导入线路尚未接入后台" }
    }
    certificateButton.target = self
    certificateButton.action = #selector(confirmCertificates)
    certificateButton.setAccessibilityIdentifier("tls-batch")
    let create = PaxRouteButton(
      "新增线路", symbol: "plus", style: .primary, target: self, action: #selector(createProxy))
    create.setAccessibilityIdentifier("route-create")
    createButton = create
    let groupRow = NSStackView(views: [tabs, certificateButton, manage, add, create])
    groupRow.spacing = PaxTheme.Spacing.tight
    tabs.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    tabs.onSelect = { [weak self] id in self?.store.perform(.group(id)) }
    search.font = PaxTheme.Typography.caption()
    search.heightAnchor.constraint(equalToConstant: PaxTheme.Routes.controlHeight).isActive = true
    search.placeholderString = "搜索线路、地址或协议"
    search.focusRingType = .none
    search.target = self
    search.action = #selector(searchChanged)
    search.sendsSearchStringImmediately = true
    search.setAccessibilityIdentifier("route-search")
    table.onScrollPosition = { [weak self] atTop in self?.readingList = !atTop }
    table.onSort = { [weak self] column, ascending in
      self?.store.sortByColumn(column, ascending: ascending)
    }
    table.onActivate = { [weak self] id in
      guard let self else { return }
      self.presentRouteModal(
        RouteDetailController(store: self.store, id: id, tooltip: self.tooltip))
    }
    refreshButton.target = self
    refreshButton.action = #selector(refresh)
    refreshButton.showsDisclosure = true
    resetSort.target = self
    resetSort.action = #selector(resetSorting)
    resetSort.setAccessibilityIdentifier("routes-sort-reset")
    testButton.target = self
    testButton.action = #selector(testVisible)
    testButton.showsDisclosure = true
    pathButton.target = self
    pathButton.action = #selector(diagnoseTrafficPath)
    pathButton.setAccessibilityIdentifier("traffic-path-diagnostics")
    reconnectButton.target = self
    reconnectButton.action = #selector(reconnectProxy)
    reconnectButton.setAccessibilityIdentifier("routes-reconnect")
    let tools = NSStackView(views: [
      search, NSView(),
      resetSort, refreshButton, reconnectButton, pathButton, testButton,
    ])
    tools.spacing = PaxTheme.Spacing.tight
    let searchWidth = search.widthAnchor.constraint(equalToConstant: PaxTheme.Routes.searchWidth)
    searchWidth.priority = .defaultHigh
    searchWidth.isActive = true
    search.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    let card = PaxCard(insets: NSEdgeInsets(), style: .routesList)
    card.contentView = table
    let list = NSView()
    list.addSubview(card)
    list.addSubview(empty)
    card.translatesAutoresizingMaskIntoConstraints = false
    empty.translatesAutoresizingMaskIntoConstraints = false
    NSLayoutConstraint.activate([
      card.leadingAnchor.constraint(equalTo: list.leadingAnchor),
      card.trailingAnchor.constraint(equalTo: list.trailingAnchor),
      card.topAnchor.constraint(equalTo: list.topAnchor),
      card.bottomAnchor.constraint(equalTo: list.bottomAnchor),
      table.bottomAnchor.constraint(equalTo: card.bottomAnchor),
      empty.centerXAnchor.constraint(equalTo: list.centerXAnchor),
      empty.centerYAnchor.constraint(equalTo: list.centerYAnchor),
    ])
    let stack = NSStackView(views: [header, groupRow, tools, tunNotice, list, notice])
    stack.orientation = .vertical
    stack.alignment = .leading
    stack.spacing = PaxTheme.Routes.toolsGap
    stack.translatesAutoresizingMaskIntoConstraints = false
    view.addSubview(stack)
    for child in stack.arrangedSubviews {
      child.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
    }
    NSLayoutConstraint.activate([
      stack.leadingAnchor.constraint(
        equalTo: view.leadingAnchor, constant: PaxTheme.Routes.pageInset),
      stack.trailingAnchor.constraint(
        equalTo: view.trailingAnchor, constant: -PaxTheme.Routes.pageInset),
      stack.topAnchor.constraint(
        equalTo: view.safeAreaLayoutGuide.topAnchor, constant: PaxTheme.Routes.pageTop),
      stack.bottomAnchor.constraint(
        equalTo: view.bottomAnchor, constant: -PaxTheme.Routes.pageInset),
      header.heightAnchor.constraint(equalToConstant: PaxTheme.Dashboard.pageHeader),
      list.heightAnchor.constraint(greaterThanOrEqualToConstant: PaxTheme.Routes.headerHeight),
    ])
    table.menuForRow = { [weak self] id in self?.menu(id) ?? NSMenu() }
    observe()
  }
  override func viewDidAppear() {
    super.viewDidAppear()
    displayedOrder = []
    orderingKey = ""
    readingList = false
    render()
    table.showFirstRowIfNeeded()
  }
  func focusSearch() { view.window?.makeFirstResponder(search) }
  private func observe() {
    withObservationTracking {
      render()
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observe() }
    }
  }
  private func render() {
    reconnectButton.isEnabled = store.connectionStore?.canReconnect == true
    reconnectButton.isHidden = store.connectionStore?.proxyEnabled != true
    pathButton.isEnabled =
      store.connectionStore?.trafficPathStore != nil
      && store.connectionStore?.connection == .connected
      && store.connectionStore?.routesStore?.capabilities.contains(
        "network.connection_diagnostics.v1")
        == true
    table.setSortIndicator(column: store.sortColumn, ascending: store.sortAscending)
    resetSort.isEnabled = store.sort != .source
    tabs.apply(
      [
        .init(id: "all", title: "全部", count: store.nodes.filter { !$0.hidden }.count),
        .init(
          id: "default", title: "默认", accessibilityLabel: "默认，未加入分组的本地线路",
          count: store.defaultMembers.count),
      ]
        + store.displayGroups.map {
          .init(
            id: $0.id, title: $0.name,
            symbol: $0.kind == .subscription ? "link" : nil,
            accessibilityLabel: ($0.kind == .subscription ? "订阅分组，" : "自定义分组，")
              + "\($0.name)，\($0.members.count) 条线路", count: $0.members.count)
        },
      selected: store.selectedGroup)
    mode.selectedIndex = store.isRunning || store.isStarting ? 0 : 1
    mode.isEnabled = store.canToggleProxy
    importButton?.isEnabled = store.canImport
    createButton?.isEnabled = store.canCreateProxy
    createButton?.toolTip = store.canCreateProxy ? nil : "当前后台不可用或不支持新增代理线路"
    certificateButton.isHidden = store.certificateFailures.isEmpty
    certificateButton.title = "处理 \(store.certificateFailures.count) 条证书异常"
    certificateButton.isEnabled = store.canConfirmCertificateFailures
    groupButton?.isEnabled = store.canManageGroups
    groupButton?.toolTip = store.canManageGroups ? nil : "当前无法管理分组，请检查后台连接"
    importButton?.toolTip = store.canImport ? nil : "当前无法导入，请检查后台连接"
    let key = "\(store.selectedGroup)|\(store.sort.rawValue)|\(search.stringValue)"
    let nodes = store.visible
    if !readingList || key != orderingKey {
      orderingKey = key
      displayedOrder = []
    }
    let positions = Dictionary(
      uniqueKeysWithValues: displayedOrder.enumerated().map { ($0.element, $0.offset) })
    let ordered = nodes.enumerated().sorted {
      (positions[$0.element.id] ?? displayedOrder.count + $0.offset)
        < (positions[$1.element.id] ?? displayedOrder.count + $1.offset)
    }.map(\.element)
    displayedOrder = ordered.map(\.id)
    table.apply(
      ordered.map { n in
        let p = store.info(n.id)
        let chain = p.chain
        let stale = p.staleCaptions
        let cf =
          chain.flatMap { store.info($0.front).cfIP ?? store.info($0.final).cfIP } ?? p.cfIP
        return PaxRouteTableRow(
          id: n.id, name: n.name,
          connection: ProxyRoutePresentation.connection(n),
          cf: cf == nil ? nil : "CF", chained: chain != nil,
          current: store.isCurrent(n.id), selected: store.isSelected(n.id),
          certificateWarning: store.certificateWarning(n.id),

          address: p.exitIP ?? p.exitMessage ?? "未获取",
          addressCaption: [store.exitRegion(n.id), stale.exit].filter { !$0.isEmpty }
            .joined(separator: " · "),
          latency: p.latency.display(.latency), latencyCaption: stale.latency,
          speed: p.speed.display(.speed),
          latencyColor: p.latency.latencyColor)
      }, resetScroll: displayedGroup != store.selectedGroup)
    displayedGroup = store.selectedGroup
    empty.isHidden = !store.visible.isEmpty
    refreshButton.title = store.refreshing ? "刷新中…" : "刷新订阅"
    refreshButton.isEnabled = store.canRefreshSubscription(store.selectedGroup)
    refreshButton.toolTip =
      store.currentSubscription == nil
      ? "请先切换到订阅分组，再刷新该订阅" : store.subscriptionStatus(store.selectedGroup)
    let running = store.visible.filter {
      store.isMeasuring($0.id) || store.info($0.id).latency.running
        || store.info($0.id).speed.running
    }.count
    testButton.title = running > 0 ? "测试中 · \(running)" : "测试线路"
    testButton.isEnabled =
      store.hasDownload
      || (!store.visible.isEmpty
        && (store.canManage
          || store.visible.contains { store.canTest($0.id) || store.canDownload($0.id) }))
    tunNotice.stringValue = RouteExternalTUNNotice.text(store.connectionStore?.externalTUN) ?? ""
    tunNotice.isHidden = tunNotice.stringValue.isEmpty
    if let id = revealRouteID, ordered.contains(where: { $0.id == id }) {
      table.reveal(id)
      revealRouteID = nil
    }
    notice.stringValue = store.statusMessage
    notice.isHidden = store.statusMessage.isEmpty
  }
  @objc private func diagnoseTrafficPath() {
    guard let diagnostics = store.connectionStore?.trafficPathStore else { return }
    presentRouteModal(TrafficPathController(store: diagnostics, connection: store.connectionStore))
  }
  @objc private func reconnectProxy() {
    guard let connection = store.connectionStore else { return }
    Task { await connection.reconnectProxy() }
  }
  @objc private func createProxy() {
    presentRouteModal(
      RouteProxyController(store: store) { [weak self] id in
        guard let self else { return }
        self.search.stringValue = ""
        self.readingList = false
        self.revealRouteID = id
        self.render()
      })
  }
  @objc private func searchChanged() { store.perform(.search(search.stringValue)) }
}
