import AppKit
import Observation
import PaxCore
import PaxUI

/// 产品侧栏由同一壳承载；各页面在预览与正式入口分别注入数据依赖。
@MainActor
public final class WorkbenchShellViewController: NSSplitViewController {
  public let dashboard: WorkbenchDashboardViewController
  var menuConnection: ConnectionStore?
  private var liveTelemetry: TelemetryStore?
  private let pages = NSTabViewController()
  public let language: InterfaceLanguageStore?
  public var onPresentationChange: ((PresentationContext) -> Void)?
  private var presentationRevision: UInt64?

  let gettingStarted = GettingStartedView()
  let onboarding = OnboardingStore(
    key: "pax.first-run.v1." + (ProcessInfo.processInfo.environment["PAX_SOCKET"] ?? "production"))
  var onboardingUsage: AIUsageViewController?
  var onboardingRoutes: RoutesStore?
  var onboardingData: AIUsageDataStore?

  private let tooltip = PaxChartTooltipCoordinator()
  private let navigation: WorkbenchNavigation
  private let routes: RouteWorkbenchViewController
  private let routing: RoutingWorkbenchViewController?
  private let logs: LogsWorkbenchViewController?
  private let settings: SettingsWorkbenchViewController?
  private weak var demoStore: WorkbenchDemoStore?
  public init(store: WorkbenchDemoStore, language: InterfaceLanguageStore? = nil) {
    self.language = language
    self.demoStore = store
    store.presentationContext = language?.context ?? .chinese
    let icons = WorkbenchObjectIcons()
    dashboard = WorkbenchDashboardViewController(store: store, tooltip: tooltip, objectIcons: icons)
    navigation = WorkbenchNavigation()
    routes = RouteWorkbenchViewController(session: store, tooltip: tooltip)
    let routing = RoutingWorkbenchViewController(
      session: store, tooltip: tooltip, objectIcons: icons)
    self.routing = routing
    let logs = LogsWorkbenchViewController(session: store, objectIcons: icons)
    self.logs = logs
    let settings = SettingsWorkbenchViewController(language: language)
    self.settings = settings
    super.init(nibName: nil, bundle: nil)
    pages.tabStyle = .unspecified
    pages.addTabViewItem(NSTabViewItem(viewController: dashboard))
    pages.addTabViewItem(
      NSTabViewItem(viewController: routes))
    pages.addTabViewItem(
      NSTabViewItem(viewController: routing))
    pages.addTabViewItem(
      NSTabViewItem(viewController: logs))
    pages.addTabViewItem(
      NSTabViewItem(
        viewController: AIUsageViewController(
          store: AIUsageStore(preview: true, english: language?.context.language == .english),
          tooltip: tooltip, session: store))
    )
    pages.addTabViewItem(
      NSTabViewItem(viewController: settings))
    pages.addTabViewItem(
      NSTabViewItem(viewController: AboutViewController(store: AboutStore(preview: true))))
    pages.addTabViewItem(
      NSTabViewItem(viewController: ComponentGalleryViewController(tooltip: tooltip)))
    installNavigation()
    installDashboardLinks()
    observeLanguage()
  }
  public init(
    routes: RoutesStore, connection: ConnectionStore, telemetry: TelemetryStore? = nil,
    aiProfiles: AIProfilesStore? = nil, aiUsage: AIUsageDataStore? = nil,
    dashboardSources: DashboardSources? = nil, refresh: PaxRefresh? = nil,
    mcpAccess: MCPAccessActions? = nil, language: InterfaceLanguageStore? = nil,
    trafficAlerts: TrafficAlertsStore? = nil
  ) {
    self.language = language
    let icons = WorkbenchObjectIcons(telemetry: telemetry)
    onboardingRoutes = routes
    onboardingData = aiUsage
    liveTelemetry = telemetry
    menuConnection = connection
    telemetry?.selectPage(.dashboard)
    dashboard = WorkbenchDashboardViewController(
      routes: routes, connection: connection, telemetry: telemetry, sources: dashboardSources,
      refresh: refresh, tooltip: tooltip, objectIcons: icons)
    navigation = WorkbenchNavigation()
    self.routes = RouteWorkbenchViewController(
      store: RouteWorkbenchStore(routes: routes, connection: connection, refresh: refresh),
      tooltip: tooltip)
    let inactive = WorkbenchDemoStore()
    inactive.perform(.selectScenario(.off))
    let routing: RoutingWorkbenchViewController
    if let telemetry {
      routing = RoutingWorkbenchViewController(
        telemetry: telemetry, connection: connection, tooltip: tooltip, objectIcons: icons,
        refresh: refresh)
    } else {
      routing = RoutingWorkbenchViewController(
        session: inactive, tooltip: tooltip, backendAvailable: false, objectIcons: icons)
    }
    self.routing = routing
    let logs = LogsWorkbenchViewController(
      store: telemetry.map(LogsWorkbenchStore.init(telemetry:))
        ?? LogsWorkbenchStore(backendAvailable: false), connection: connection, objectIcons: icons,
      refresh: refresh)
    self.logs = logs
    let settings = SettingsWorkbenchViewController(
      measurement: MeasurementSettingsViewController(routes: routes), telemetry: telemetry,
      privacyRoutes: routes, mcpAccess: mcpAccess, connection: connection, language: language,
      trafficAlerts: trafficAlerts)
    self.settings = settings
    super.init(nibName: nil, bundle: nil)
    pages.tabStyle = .unspecified
    pages.addTabViewItem(NSTabViewItem(viewController: dashboard))
    pages.addTabViewItem(NSTabViewItem(viewController: self.routes))
    pages.addTabViewItem(NSTabViewItem(viewController: routing))
    pages.addTabViewItem(NSTabViewItem(viewController: logs))
    let usage = AIUsageViewController(
      store: AIUsageStore(configuration: aiProfiles, data: aiUsage), tooltip: tooltip,
      connection: connection, refresh: refresh)
    onboardingUsage = usage
    pages.addTabViewItem(NSTabViewItem(viewController: usage))
    pages.addTabViewItem(NSTabViewItem(viewController: settings))
    let about = AboutViewController(store: AboutStore(connection: connection))
    pages.addTabViewItem(NSTabViewItem(viewController: about))
    installNavigation()
    installDashboardLinks()
    observeLanguage()
    installOnboarding()
    if let telemetry {
      logs.onRoute = { [weak self] id in
        guard let self else { return }
        guard self.routes.store.node(id) != nil else {
          let context = self.language?.context ?? .chinese
          self.showHistoryMessage(context.text("logs.removed_route"), context: context)
          return
        }
        self.navigation.select(1)
        self.routes.presentWorkbenchModal(
          RouteDetailController(store: self.routes.store, id: id, tooltip: self.tooltip))
      }
      logs.onTraffic = { [weak self] key in
        guard let self else { return }
        self.navigation.select(2)
        var q = TrafficDetailQuery()
        q.connectionKey = key
        q.groupConnections = true
        Task { [weak self] in
          do {
            let detail = try await telemetry.trafficDetail(q)
            guard let self else { return }
            self.routing?.presentWorkbenchModal(
              WorkbenchTrafficDetailController(
                detail: detail, dimension: .application, telemetry: telemetry,
                tooltip: self.tooltip, icons: self.routing?.objectIcons,
                ruleStore: self.routing?.store, connectionKey: key
              ))
          } catch {
            let context = self?.language?.context ?? .chinese
            self?.showHistoryMessage(
              ProductErrorPresentation.display(error, context: context), context: context)
          }
        }
      }
      dashboard.onShowLogs = { [weak self] in self?.navigation.select(3) }
      dashboard.onLog = { [weak self, weak logs] id in
        self?.navigation.select(3)
        logs?.showEvent(id)
      }
    }
  }
  private func observeLanguage() {
    guard let language else { return }
    withObservationTracking {
      let context = language.context
      if presentationRevision != context.revision {
        presentationRevision = context.revision
        demoStore?.presentationContext = context
        navigation.apply(context)
        updateOnboarding()
        for child in pages.children { child.applyInterfaceLanguage(context) }
        onPresentationChange?(context)
      }
    } onChange: { [weak self] in
      Task { @MainActor in self?.observeLanguage() }
    }
  }
  /// 概览卡片上的跳转入口；日志入口在有 telemetry 时另行接线。
  private func installDashboardLinks() {
    dashboard.onShowRoutes = { [weak self] in self?.navigation.select(1) }
    dashboard.onShowAIUsage = { [weak self] in self?.navigation.select(4) }
  }
  private func showHistoryMessage(_ message: String, context: PresentationContext) {
    guard let window = view.window else { return }
    let alert = NSAlert()
    alert.messageText = message
    alert.addButton(withTitle: context.text("common.ok"))
    alert.beginSheetModal(for: window)
  }
  private func installNavigation() {
    navigation.onNavigate = { [weak self] index in
      self?.tooltip.close()
      self?.liveTelemetry?.selectPage(
        index == 0 ? .dashboard : index == 2 ? .traffic : index == 3 ? .logs : nil)
      if index >= 0 { self?.pages.selectedTabViewItemIndex = index }
      if let self, self.onboarding.path != nil {
        if index == 4 { self.onboarding.choose(.models) }
        if index == 1 { self.onboarding.choose(.network) }
      }
      if let self, let context = self.language?.context {
        self.pages.tabView.selectedTabViewItem?.viewController?.applyInterfaceLanguage(context)
      }
      self?.updateOnboarding()
    }
    let sidebar = NSSplitViewItem(sidebarWithViewController: navigation)
    sidebar.canCollapse = false
    sidebar.minimumThickness = PaxTheme.Navigation.widthRange.lowerBound
    sidebar.maximumThickness = PaxTheme.Navigation.widthRange.upperBound
    sidebar.preferredThicknessFraction =
      PaxTheme.Navigation.width / PaxTheme.Layout.defaultWindow.width
    addSplitViewItem(sidebar)
    let body = NSViewController()
    body.addChild(pages)
    let content: NSView
    if menuConnection != nil {
      let stack = NSStackView(views: [gettingStarted, pages.view])
      stack.orientation = .vertical
      stack.spacing = 0
      stack.alignment = .leading
      gettingStarted.isHidden = true
      for child in [gettingStarted, pages.view] {
        child.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
      }
      gettingStarted.setContentHuggingPriority(.required, for: .vertical)
      content = stack
    } else {
      content = pages.view
    }
    body.view = WorkbenchBodyViewport(
      content: content,
      minimumWidth: PaxTheme.Layout.minimumWindow.width - PaxTheme.Navigation.width
        - splitView.dividerThickness)
    addSplitViewItem(NSSplitViewItem(viewController: body))
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override func viewDidAppear() {
    super.viewDidAppear()
    splitView.setPosition(PaxTheme.Navigation.width, ofDividerAt: 0)

  }
  public func showComponents() {
    guard pages.tabViewItems.count > 7 else { return }
    tooltip.close()
    pages.selectedTabViewItemIndex = 7
    navigation.select(-1)
  }
  public func showDashboardPage() {
    navigation.select(0)
  }
  public func showRoutes() {
    navigation.select(1)
  }
  func showRules(packageID: String) {
    navigation.select(2)
    routing?.store.selectedPackageID = packageID
    routing?.store.perform(.tab(.rules))
    routing?.store.perform(.search(""))
  }
  public func showTrafficAnalysis() {
    navigation.select(2)
    routing?.showTrafficOverview()
  }
  public func showRouteImport() {
    navigation.select(1)
    routes.showImport()
  }
  public func showConnectionDiagnostics() {
    guard let connection = menuConnection, let diagnostics = connection.trafficPathStore else {
      return
    }
    navigation.select(0)
    dashboard.presentWorkbenchModal(
      TrafficPathController(store: diagnostics, connection: connection))
  }
  public func requestProxyFromMenu() {
    guard let connection = menuConnection else { return }
    navigation.select(0)
    ProxyTransitionPresenter.request(
      from: dashboard, connection: connection, enabled: !connection.proxyEnabled)
  }
  public func showSettings() { navigation.select(5) }
  public func showAbout() { navigation.select(6) }
  public func showAIUsage() { navigation.select(4) }
  public func showLogs() { navigation.select(3) }
  public func previewLogs(_ name: String) {
    if let scene = LogsWorkbenchStore.Scene(rawValue: name) { logs?.store.perform(.scene(scene)) }
    navigation.select(3)
  }
  public static var logsPreviewScenes: [String] {
    LogsWorkbenchStore.Scene.allCases.map(\.rawValue)
  }
  public func focusSearch() {
    if pages.selectedTabViewItemIndex == 3 {
      logs?.focusSearch()
      return
    }
    if pages.selectedTabViewItemIndex == 2 {
      routing?.focusSearch()
      return
    }
    if pages.selectedTabViewItemIndex == 1 {
      routes.focusSearch()
      return
    }
    navigation.select(0)
    dashboard.focusSearch()
  }
  public func previewRouting(_ name: String) {
    if let scene = RoutingWorkbenchStore.Scene(rawValue: name) {
      routing?.store.perform(.scene(scene))
    }
    navigation.select(2)
  }
  public static var routingPreviewScenes: [String] {
    RoutingWorkbenchStore.Scene.allCases.map(\.rawValue)
  }
}

@MainActor
private final class WorkbenchNavigation: NSViewController {
  var onNavigate: ((Int) -> Void)?
  private var selectedIndex = 0
  override func loadView() {
    let sidebar = PaxSidebar(
      title: "Pax Connect",
      primaryItems: [
        .init(pageIndex: 0, title: "网络概览", symbol: "square.grid.2x2"),
        .init(pageIndex: 1, title: "线路管理", symbol: "point.3.connected.trianglepath.dotted"),
        .init(pageIndex: 2, title: "流量监控", symbol: "arrow.triangle.branch"),
        .init(pageIndex: 3, title: "运行日志", symbol: "list.bullet"),
        .init(pageIndex: 4, title: "模型用量", symbol: "sparkles"),
      ],
      secondaryItems: [
        .init(pageIndex: 5, title: "系统设置", symbol: "gearshape"),
        .init(pageIndex: 6, title: "关于应用", symbol: "info.circle"),
      ])
    sidebar.onSelect = { [weak self] pageIndex in self?.select(pageIndex) }
    sidebar.select(selectedIndex)
    view = sidebar
  }
  func apply(_ context: PresentationContext) {
    loadViewIfNeeded()
    let keys = ["overview", "routes", "traffic", "logs", "usage", "settings", "about"]
    (view as? PaxSidebar)?.apply(
      titles: Dictionary(
        uniqueKeysWithValues:
          keys.enumerated().map { ($0.offset, context.text("navigation." + $0.element)) }),
      selectedLabel: context.text("common.selected"),
      unselectedLabel: context.text("common.unselected"))
  }
  func select(_ index: Int) {
    selectedIndex = index
    loadViewIfNeeded()
    (view as? PaxSidebar)?.select(index)
    onNavigate?(index)
  }
}
