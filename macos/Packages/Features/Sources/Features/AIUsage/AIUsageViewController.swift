import AppKit
import Observation
import PaxCore
import PaxUI

@MainActor
final class AIUsageViewController: NSViewController {
  let store: AIUsageStore
  let actions = AIUsageActions()
  let content = NSStackView()
  var sessionContent: AISessionLiveLimitsView?
  var logContent: AIUsageLogView?
  var refreshMeasuresOnly = false
  var contentRefreshers: [() -> Void] = []
  private var renderedPage: AIUsageStore.Page?
  var clientAnalysisContent: NSStackView?
  var trendAnalysisContent: NSStackView?
  var trendModelPicker: PaxRoutePicker?
  var trendModelWidth: NSLayoutConstraint?
  var trendMetaLabel: NSTextField?
  private let scroll = NSScrollView()
  private let tabs = PaxTabBar(style: .detail)
  private let mode = PaxTabs(["代理", "直连"], style: .mode)
  private let session: WorkbenchDemoStore?
  private let connection: ConnectionStore?
  private let periods = PaxTabs(AIUsageViewController.selectablePeriods.map(\.rawValue))
  private lazy var dateButton = PaxCompactButton("", target: self, action: #selector(openDates))
  /// Shared by the overview and logs pages.
  static let selectablePeriods: [AIUsagePeriod] = [.today, .yesterday, .week, .month, .year]
  let tooltip: PaxChartTooltipCoordinator
  private var bindingPopoverOpen = false
  private var wasConnected = false
  private var configurationTask: Task<Void, Never>?
  private var foregroundObserver: NSObjectProtocol?
  private let pageRefresh: PaxRefresh?
  private var pageVisible = false
  private var shownPage: AIUsageStore.Page?
  private var retainedPopover: NSPopover?
  init(
    store: AIUsageStore, tooltip: PaxChartTooltipCoordinator, session: WorkbenchDemoStore? = nil,
    connection: ConnectionStore? = nil, refresh: PaxRefresh? = nil
  ) {
    self.session = session
    self.connection = connection
    self.store = store
    self.tooltip = tooltip
    pageRefresh = refresh
    super.init(nibName: nil, bundle: nil)
    refresh?.bind(.aiOverview) { [weak self] in self?.store.data?.fetch(.overview) }
    refresh?.bind(.aiSessions) { [weak self] in self?.store.data?.sessions?.refresh() }
    refresh?.bind(.aiLogs) { [weak self] in self?.store.data?.fetch(.logs) }
    refresh?.bind(.aiProfiles) { [weak self] in self?.store.data?.fetch(.profiles) }
  }
  /// 概览、日志、Profile 各自是一个刷新页面：只读当前子页需要的数据，离开即停。
  private func syncRefreshPage() {
    let target = pageVisible ? store.page : nil
    guard target != shownPage else { return }
    if let old = shownPage { pageRefresh?.pageDidDisappear(Self.refreshPage(old)) }
    shownPage = target
    store.setUsagePart(target.flatMap(Self.part), fetchNow: pageRefresh == nil)
    store.data?.sessions?.setActive(target == .sessions, fetchNow: pageRefresh == nil)
    if let target { pageRefresh?.pageDidAppear(Self.refreshPage(target)) }
  }
  private static func refreshPage(_ page: AIUsageStore.Page) -> RefreshPage {
    switch page {
    case .sessions: .aiSessions
    case .overview: .aiOverview
    case .logs: .aiLogs
    case .profiles: .aiProfiles
    }
  }
  private static func part(_ page: AIUsageStore.Page) -> AIUsageDataStore.Part? {
    switch page {
    case .sessions: nil
    case .overview: .overview
    case .logs: .logs
    case .profiles: .profiles
    }
  }
  override func viewWillAppear() {
    super.viewWillAppear()
    pageVisible = true
    syncRefreshPage()
  }
  override func viewWillDisappear() {
    super.viewWillDisappear()
    pageVisible = false
    syncRefreshPage()
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    view = WorkbenchSurface()
    view.setAccessibilityIdentifier("ai-usage-page")
    let header = PaxPageHeader(
      title: "模型用量",
      accessory: ProxyStatusAccessory(mode: mode, connection: connection, presenter: self))
    mode.onSelect = { [weak self] index in
      guard let self else { return }
      if let connection = self.connection {
        self.mode.selectedIndex = connection.proxyEnabled ? 0 : 1
        ProxyTransitionPresenter.request(from: self, connection: connection, enabled: index == 0)
      } else if let session = self.session, (index == 0) != session.isRunning {
        session.perform(.toggleProxy)
      }
    }
    let navigation = AIUsageLayout.stack([tabs, periods, dateButton], vertical: false)
    periods.onSelect = { [weak self] index in
      self?.store.perform(.period(Self.selectablePeriods[index]))
    }
    dateButton.image = PaxRouteIcon.image("calendar")
    dateButton.trailingImage = PaxIcon.image("chevron.down", size: PaxTheme.DateRange.chevronSize)
    dateButton.setAccessibilityLabel("选择模型用量日期")
    dateButton.heightAnchor.constraint(equalToConstant: PaxTheme.Dashboard.tabHeight).isActive =
      true
    tabs.onSelect = { [weak self] id in
      guard let page = AIUsageStore.Page(rawValue: id) else { return }
      self?.store.perform(.page(page))
    }
    content.orientation = .vertical
    content.alignment = .leading
    content.spacing = PaxTheme.Spacing.roomy
    let document = WorkbenchDocument()
    scroll.documentView = document
    scroll.drawsBackground = false
    scroll.hasVerticalScroller = true
    scroll.autohidesScrollers = true
    scroll.automaticallyAdjustsContentInsets = false
    document.addSubview(content)
    for item in [header, navigation, scroll] {
      view.addSubview(item)
      item.translatesAutoresizingMaskIntoConstraints = false
    }
    document.translatesAutoresizingMaskIntoConstraints = false
    content.translatesAutoresizingMaskIntoConstraints = false
    let inset = PaxTheme.Spacing.page
    NSLayoutConstraint.activate([
      header.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: inset),
      header.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -inset),
      header.topAnchor.constraint(
        equalTo: view.safeAreaLayoutGuide.topAnchor, constant: PaxTheme.Spacing.pageTop),
      header.heightAnchor.constraint(equalToConstant: PaxTheme.Dashboard.pageHeader),
      navigation.leadingAnchor.constraint(equalTo: header.leadingAnchor),
      navigation.trailingAnchor.constraint(equalTo: header.trailingAnchor),
      navigation.topAnchor.constraint(
        equalTo: header.bottomAnchor, constant: PaxTheme.Spacing.medium),
      scroll.topAnchor.constraint(
        equalTo: navigation.bottomAnchor, constant: PaxTheme.Spacing.medium),
      scroll.leadingAnchor.constraint(equalTo: header.leadingAnchor),
      scroll.trailingAnchor.constraint(equalTo: header.trailingAnchor),
      scroll.bottomAnchor.constraint(equalTo: view.bottomAnchor),
      document.widthAnchor.constraint(equalTo: scroll.contentView.widthAnchor),
      content.leadingAnchor.constraint(equalTo: document.leadingAnchor),
      content.trailingAnchor.constraint(equalTo: document.trailingAnchor),
      content.topAnchor.constraint(equalTo: document.topAnchor),
      content.bottomAnchor.constraint(equalTo: document.bottomAnchor, constant: -inset),
    ])
    observe()
  }
  override func viewDidAppear() {
    super.viewDidAppear()
    refreshConfiguration()
    if foregroundObserver == nil {
      foregroundObserver = NotificationCenter.default.addObserver(
        forName: NSApplication.didBecomeActiveNotification, object: nil, queue: .main
      ) { [weak self] _ in Task { @MainActor in self?.refreshConfiguration() } }
    }
  }
  override func viewDidDisappear() {
    super.viewDidDisappear()
    configurationTask?.cancel()
    store.configuration.cancelRefresh()
    if let foregroundObserver { NotificationCenter.default.removeObserver(foregroundObserver) }
    foregroundObserver = nil
  }
  func refreshConfiguration() {
    configurationTask?.cancel()
    store.configuration.cancelRefresh()
    configurationTask = Task { [weak self] in
      guard let self else { return }
      await store.configuration.refresh()
      await store.configuration.refreshMissingModels()
    }
  }
  private func observe() {
    syncRefreshPage()
    store.syncUsage()
    withObservationTracking {
      // Partial chart refreshes must retain observation of the rest of the page state.
      _ = (
        store.bindings, store.profiles, store.minimumCost, store.maximumCost,
        store.page, store.period,
        store.start, store.end, store.measureCost, store.trendFilter, store.profileMeasureCost,
        store.logClient,
        store.logProfileID, store.logModelID, store.logResult, store.configuration.loading,
        store.configuration.error, store.configuration.available
      )
      _ = (
        store.data?.buckets, store.data?.lifetime, store.data?.logs, store.data?.state,
        store.data?.costRangeSupported,
        store.data?.error, store.data?.loading, store.data?.lastUsed,
        store.data?.sessions?.current, store.data?.sessions?.detail, store.data?.sessions?.selected,
        store.data?.sessions?.error, store.data?.sessions?.loading,
        store.data?.sessions?.limitsSupported
      )
      _ = (
        store.data?.sessions?.catalog.items, store.data?.sessions?.catalog.selected,
        store.data?.sessions?.catalog.counts, store.data?.sessions?.catalog.sourceErrors,
        store.data?.sessions?.catalog.loading, store.data?.sessions?.catalog.ready,
        store.data?.sessions?.catalog.error, store.data?.sessions?.catalog.hasMore,
        store.data?.sessions?.catalog.total, store.data?.sessions?.catalog.collectionError
      )
      _ = connection?.connection
      mode.selectedIndex = (connection?.proxyEnabled ?? session?.isRunning ?? false) ? 0 : 1
      mode.isEnabled = connection != nil || session != nil
      render()
    } onChange: { [weak self] in
      Task { @MainActor in self?.observe() }
    }
    let connected = connection?.connection == .connected
    if connected != wasConnected {
      wasConnected = connected
      if connected, view.window != nil {
        refreshConfiguration()
        if pageRefresh == nil { store.data?.retry() }
      }
    }
  }
  func add(_ item: NSView) {
    content.addArrangedSubview(item)
    item.widthAnchor.constraint(equalTo: content.widthAnchor).isActive = true
  }
  private func render() {

    periods.isHidden = store.page == .profiles
    dateButton.isHidden = store.page == .profiles
    tabs.apply(
      zip(AIUsageStore.Page.allCases, ["概览", "会话分析", "Profile", "日志"]).map {
        .init(id: $0.0.rawValue, title: $0.1)
      }, selected: store.page.rawValue)
    periods.selectedIndex = Self.selectablePeriods.firstIndex(of: store.period) ?? -1
    dateButton.title = dateLabel()
    if renderedPage == store.page {
      for refresh in contentRefreshers { refresh() }
      logContent?.refresh()
      sessionContent?.refresh()
      return
    }
    renderedPage = store.page
    contentRefreshers = []
    logContent = nil
    sessionContent = nil
    refreshMeasuresOnly = false
    clientAnalysisContent = nil
    trendAnalysisContent = nil
    trendModelPicker = nil
    trendModelWidth = nil
    trendMetaLabel = nil
    actions.reset()
    for item in content.arrangedSubviews {
      content.removeArrangedSubview(item)
      item.removeFromSuperview()
    }
    periods.selectedIndex = Self.selectablePeriods.firstIndex(of: store.period) ?? -1
    dateButton.title = dateLabel()
    for item in statusItems() {
      add(item)
      // The notice sits directly above the page body instead of a full section gap.
      if store.page != .profiles { content.setCustomSpacing(PaxTheme.Spacing.tight, after: item) }
    }
    switch store.page {
    case .sessions where store.preview:
      // Demo entry only: the limits design runs on in-memory data (P07d-ai-session-limits).
      add(AISessionLimitsView(options: store.sessionOptions))
    case .sessions:
      let analysis = AISessionLiveLimitsView(store: store)
      sessionContent = analysis
      add(analysis)
    case .overview: renderOverview()
    case .profiles: renderProfiles()
    case .logs: renderLogs()
    }
    // A different subpage installs a new set of controls.
    Task { @MainActor [weak self] in
      self?.view.layoutSubtreeIfNeeded()
      self?.view.window?.recalculateKeyViewLoop()
    }
  }
  func show(_ kind: AIUsageDetailController.Kind, anchor: NSView? = nil) {
    let controller = AIUsageDetailController(store: store, kind: kind)
    if let anchor {
      presentPopover(controller, from: anchor)
    } else {
      presentWorkbenchModal(controller)
    }
  }
  func presentBinding(_ client: AIClient, from anchor: NSView) {
    let controller = AIBindingController(store: store, client: client)
    bindingPopoverOpen = true
    controller.onDismiss = { [weak self] in
      guard let self else { return }
      bindingPopoverOpen = false
      Task { @MainActor [weak self] in self?.render() }
    }
    presentPopover(controller, from: anchor)
  }
  func presentPopover(_ controller: WorkbenchModalController, from anchor: NSView) {
    controller.loadViewIfNeeded()
    let width = controller.maximumPresentationWidth ?? AIBindingController.popoverWidth
    controller.view.setFrameSize(.init(width: width, height: 480))
    controller.view.layoutSubtreeIfNeeded()
    controller.preferredContentSize = .init(
      width: width, height: min(600, controller.pane.naturalHeight))
    controller.pane.onContentResize = { [weak controller] in
      guard let controller else { return }
      let height = min(600, controller.pane.naturalHeight)
      if abs(controller.preferredContentSize.height - height) > 1 {
        controller.preferredContentSize = .init(width: width, height: height)
      }
    }
    present(
      controller, asPopoverRelativeTo: anchor.bounds, of: anchor, preferredEdge: .maxY,
      behavior: .transient)
  }
  private func dateLabel() -> String {
    let lower = store.interval.lowerBound
    let upper = store.interval.upperBound.addingTimeInterval(-1)
    return Calendar.current.isDate(lower, inSameDayAs: upper)
      ? AIUsageFormatting.date(lower)
      : AIUsageFormatting.date(lower) + " – " + AIUsageFormatting.date(upper)
  }
  @objc private func openDates() {
    let controller = AIUsageDateController(store: store)
    let popover = NSPopover()
    popover.behavior = .transient
    popover.applyMotionPreference()
    popover.contentViewController = controller
    controller.onApply = { [weak popover] in popover?.close() }
    retainedPopover = popover
    popover.show(relativeTo: dateButton.bounds, of: dateButton, preferredEdge: .maxY)
  }
}
