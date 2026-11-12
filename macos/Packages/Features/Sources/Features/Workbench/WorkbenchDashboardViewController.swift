import AppKit
import Observation
import PaxCore
import PaxUI

/// 网络概览 v2。组件只收值与回调；真实数据按依赖区域更新，演示数据走 `refresh`。
@MainActor
public final class WorkbenchDashboardViewController: NSViewController {
  var detailTask: Task<Void, Never>?
  var pageVisible = false
  var refreshOnNextEntry = true
  let noticeHost = NSStackView()
  var telemetry: TelemetryStore?
  private var todayGeneration: UInt64 = 0
  private var todayEvidenceGeneration: UInt64 = 0
  private var todaySession: UInt64 = 0
  var todayValid: Bool {
    guard let telemetry else { return true }
    return todayGeneration == telemetry.trafficDataInvalidationGeneration
      && (!telemetry.connected || todaySession == telemetry.sessionGeneration)
      && (savedTodayPage.asOf.timeIntervalSince1970 == 0
        || Calendar.current.isDateInToday(savedTodayPage.asOf))
  }
  private var savedTodayPage = TrafficPage()
  var todayPage: TrafficPage {
    get {
      guard todayValid else { return TrafficPage() }
      return telemetry.map { $0.trafficInvalidationGeneration != todayEvidenceGeneration } == true
        ? savedTodayPage.withoutLogEvidence : savedTodayPage
    }
    set {
      savedTodayPage = newValue
      todayGeneration = telemetry?.trafficDataInvalidationGeneration ?? 0
      todayEvidenceGeneration = telemetry?.trafficInvalidationGeneration ?? 0
      todaySession = telemetry?.sessionGeneration ?? 0
      todayLoaded = true
    }
  }
  var todayLoaded = false
  var onLog: ((String) -> Void)?
  var onShowLogs: (() -> Void)?
  var onShowAIUsage: (() -> Void)?
  var onShowRoutes: (() -> Void)?
  private let objectIcons: WorkbenchObjectIcons
  /// 「流量分析」与流量页共用同一列表定义和行模型，窗口固定为今日。
  let analysisList: RoutingTrafficList
  var analysisStore: RoutingWorkbenchStore { analysisList.store }
  let store: WorkbenchDemoStore?
  let liveRoutes: RoutesStore?
  let liveConnection: ConnectionStore?
  let sources: DashboardSources?
  let document = WorkbenchGrid()
  let tooltip: PaxChartTooltipCoordinator
  let routeCard: PaxCurrentRouteCard
  let trafficCard: PaxTodayTrafficCard
  let trendCard: PaxModelTrendCard
  let rankCard = PaxModelRankCard()
  let analysis: PaxTrafficAnalysisCard
  let attentionCard = PaxInsightCard(title: "需要关注 · 今日", identifier: "dashboard-attention")
  let changesCard = PaxInsightCard(title: "最近变化", identifier: "dashboard-changes")
  let actionCard = PaxActionCard()
  /// 刷新策略的唯一入口（ticket 107）；nil 时只在进页读取一次（演示 / 无 daemon）。
  let pageRefresh: PaxRefresh?
  var telemetryGate = ""
  var calendarIdentity = DashboardCalendarIdentity()
  var calendarObservers: [NSObjectProtocol] = []
  var workspaceObserver: NSObjectProtocol?
  var fallbackTasks: [DashboardReadGroup: Task<Void, Never>] = [:]
  // Internal observer used by behavior tests; no production logging or counters.
  var onPresentRegion: ((String) -> Void)?
  var live = DashboardLiveState()
  var onboardingActive = false
  var currentAction: DashboardInsightProjection.Action?
  let notice = PaxRouteText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private let mode = PaxTabs(["代理", "直连"], style: .mode)

  public init(
    store: WorkbenchDemoStore, tooltip: PaxChartTooltipCoordinator = PaxChartTooltipCoordinator(),
    objectIcons: WorkbenchObjectIcons = WorkbenchObjectIcons()
  ) {
    self.store = store
    liveRoutes = nil
    liveConnection = nil
    sources = nil
    pageRefresh = nil
    self.tooltip = tooltip
    self.objectIcons = objectIcons
    routeCard = PaxCurrentRouteCard(tooltip: tooltip)
    trafficCard = PaxTodayTrafficCard(
      tooltip: tooltip, axisLabel: DashboardTrafficProjection.axisByteLabel)
    trendCard = PaxModelTrendCard(tooltip: tooltip)
    analysisList = RoutingTrafficList(
      store: RoutingWorkbenchStore(session: store, initialPeriod: .day), icons: objectIcons,
      tooltip: tooltip, style: .dashboard)
    analysis = Self.makeAnalysisCard()
    super.init(nibName: nil, bundle: nil)
  }
  public init(
    routes: RoutesStore, connection: ConnectionStore, telemetry: TelemetryStore? = nil,
    sources: DashboardSources? = nil, refresh: PaxRefresh? = nil,
    tooltip: PaxChartTooltipCoordinator = PaxChartTooltipCoordinator(),
    objectIcons: WorkbenchObjectIcons = WorkbenchObjectIcons()
  ) {
    store = nil
    self.telemetry = telemetry
    liveRoutes = routes
    liveConnection = connection
    self.sources = sources
    pageRefresh = refresh
    self.tooltip = tooltip
    self.objectIcons = objectIcons
    routeCard = PaxCurrentRouteCard(tooltip: tooltip)
    trafficCard = PaxTodayTrafficCard(
      tooltip: tooltip, axisLabel: DashboardTrafficProjection.axisByteLabel)
    trendCard = PaxModelTrendCard(tooltip: tooltip)
    analysisList = RoutingTrafficList(
      store: telemetry.map { RoutingWorkbenchStore(telemetry: $0) }
        ?? RoutingWorkbenchStore(
          session: WorkbenchDemoStore(), backendAvailable: false, initialPeriod: .day),
      icons: objectIcons, tooltip: tooltip, style: .dashboard)
    analysis = Self.makeAnalysisCard()
    super.init(nibName: nil, bundle: nil)
    analysisList.collecting = { [weak connection] in connection?.proxyEnabled == true }
  }
  isolated deinit {
    for observer in calendarObservers { NotificationCenter.default.removeObserver(observer) }
    if let workspaceObserver {
      NSWorkspace.shared.notificationCenter.removeObserver(workspaceObserver)
    }
    for task in fallbackTasks.values { task.cancel() }
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }

  public override func loadView() {
    let root = WorkbenchSurface()
    let scroll = WorkbenchScrollView()
    scroll.drawsBackground = false
    scroll.hasVerticalScroller = true
    document.autoresizingMask = [.width]
    document.frame = NSRect(origin: .zero, size: PaxTheme.Layout.defaultWindow)
    scroll.documentView = document
    scroll.translatesAutoresizingMaskIntoConstraints = false
    root.addSubview(scroll)
    NSLayoutConstraint.activate([
      scroll.leadingAnchor.constraint(equalTo: root.leadingAnchor),
      scroll.trailingAnchor.constraint(equalTo: root.trailingAnchor),
      scroll.topAnchor.constraint(equalTo: root.safeAreaLayoutGuide.topAnchor),
      scroll.bottomAnchor.constraint(equalTo: root.bottomAnchor),
    ])
    view = root
    document.header = makeHeader()
    noticeHost.addArrangedSubview(notice)
    noticeHost.spacing = PaxTheme.Spacing.tight
    noticeHost.isHidden = telemetry == nil
    document.notice = noticeHost
    notice.isHidden = liveConnection == nil
    document.action = actionCard
    document.route = routeCard
    document.traffic = trafficCard
    document.modelTrend = trendCard
    document.modelRank = rankCard
    document.analysis = analysis
    document.attention = attentionCard
    document.changes = changesCard
    document.install()
    mode.onSelect = { [weak self] index in
      guard let self else { return }
      if let connection = self.liveConnection {
        self.mode.selectedIndex = connection.proxyEnabled ? 0 : 1
        ProxyTransitionPresenter.request(
          from: self, connection: connection, enabled: index == 0)
        return
      }
      guard let store = self.store else { return }
      if (index == 0) != (store.isRunning || store.scenario == .starting) {
        store.perform(.toggleProxy)
      }
    }
    mode.setAccessibilityIdentifier("workbench-power")
    installAnalysis()
    installOverviewActions()
    bindRefresh()
    if store == nil {
      observeLiveRegions()
      installCalendarNotifications()
    } else {
      observe()
    }
  }
  private func makeHeader() -> NSView {
    PaxPageHeader(
      title: "网络概览",
      accessory: ProxyStatusAccessory(mode: mode, connection: liveConnection, presenter: self))
  }
  private func observe() {
    withObservationTracking {
      syncTelemetry()
      refresh()
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observe() }
    }
  }
  private func refresh() {
    if telemetry == nil { tooltip.close() }
    guard let store else { return }
    mode.selectedIndex = store.isRunning || store.scenario == .starting ? 0 : 1
    mode.isEnabled = store.canOperate
    applyDemoOverview(store)
    renderAnalysis()
    document.needsLayout = true
  }
  func modeForConnection() {
    guard let connection = liveConnection else { return }
    let running = connection.status == .protected || connection.status == .degraded
    mode.selectedIndex = running || connection.status == .starting ? 0 : 1
    mode.isEnabled = connection.canToggleProxy
    mode.toolTip = connection.proxyMessage
  }
  public override func viewWillDisappear() {
    // The shell changes the selected telemetry page before switching its tab.
    // AppKit also sends appearance callbacks for miniaturization, which leaves it selected.
    if telemetry?.visiblePage != .dashboard { refreshOnNextEntry = true }
    for task in fallbackTasks.values { task.cancel() }
    fallbackTasks.removeAll()
    analysisStore.stopLive()
    pageVisible = false
    pageRefresh?.pageDidDisappear(.dashboard)
    detailTask?.cancel()
    super.viewWillDisappear()
    tooltip.close()
  }
  public override func viewWillAppear() {
    super.viewWillAppear()
    pageVisible = true
    reconcileCalendar()
    pageRefresh?.pageDidAppear(.dashboard, refreshOnEntry: refreshOnNextEntry)
    refreshOnNextEntry = false
    syncTelemetry()
  }
  /// 筛选变化后立即重读，不等下一个推送。
  func restartTelemetry() {
    guard pageVisible else { return }
    requestRead(.analysis, restart: true)
  }
  func showSubscription() {
    guard store != nil else { return }
    let sheet = WorkbenchSheet(title: "添加订阅", explanation: "添加订阅以导入线路。")
    sheet.addAction("添加线路") { [weak self, weak sheet] in
      self?.store?.perform(.addSampleRoute)
      sheet?.dismissSheet()
    }
    presentAsSheet(sheet)
  }
}
