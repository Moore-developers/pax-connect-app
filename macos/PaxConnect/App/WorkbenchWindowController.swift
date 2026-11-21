import AppKit
import Features
import PaxCore
import PaxUI

/// 工作台窗口：生产模式使用 AppDelegate 注入的共享状态，演示模式使用独立离线工厂。
@MainActor
final class WorkbenchWindowController: NSWindowController, NSWindowDelegate, NSMenuItemValidation {
  private var liveTelemetry: TelemetryStore?
  private var liveRefresh: PaxRefresh?
  private let store: WorkbenchDemoStore?
  private let shell: WorkbenchShellViewController
  private var workbenchRoot: NSViewController?

  convenience init(language: InterfaceLanguageStore? = nil) {
    let demo = WorkbenchDemoStore()
    self.init(shell: WorkbenchShellViewController(store: demo, language: language), store: demo)
  }
  convenience init(
    routes: RoutesStore, connection: ConnectionStore, telemetry: TelemetryStore? = nil,
    aiProfiles: AIProfilesStore? = nil, aiUsage: AIUsageDataStore? = nil,
    dashboardSources: DashboardSources? = nil, refresh: PaxRefresh? = nil,
    mcpAccess: MCPAccessActions? = nil, language: InterfaceLanguageStore? = nil
  ) {
    self.init(
      shell: WorkbenchShellViewController(
        routes: routes, connection: connection, telemetry: telemetry, aiProfiles: aiProfiles,
        aiUsage: aiUsage, dashboardSources: dashboardSources, refresh: refresh,
        mcpAccess: mcpAccess, language: language),
      store: nil)
    liveTelemetry = telemetry
    liveRefresh = refresh
    telemetry?.setWindowVisible(true)
    telemetry?.setAppActive(NSApp.isActive)
  }
  private init(shell: WorkbenchShellViewController, store: WorkbenchDemoStore?) {
    self.shell = shell
    self.store = store
    let window = NSWindow(
      contentRect: NSRect(origin: .zero, size: PaxTheme.Layout.defaultWindow),
      styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
      backing: .buffered, defer: false)
    window.appearance = NSAppearance(named: .aqua)
    window.title = "Pax Connect"
    window.minSize = PaxTheme.Layout.minimumWindow
    window.backgroundColor = PaxTheme.Color.workspace
    window.titlebarAppearsTransparent = true
    window.titleVisibility = .hidden
    window.center()
    super.init(window: window)
    window.delegate = self
    let root = NSViewController()
    root.view = NSView(frame: NSRect(origin: .zero, size: PaxTheme.Layout.defaultWindow))
    root.addChild(shell)
    shell.view.frame = root.view.bounds
    shell.view.autoresizingMask = [.width, .height]
    root.view.addSubview(shell.view)
    if let content = shell.splitViewItems.last?.viewController.view {
      let dragArea = WorkbenchTitlebarDragArea()
      dragArea.translatesAutoresizingMaskIntoConstraints = false
      root.view.addSubview(dragArea)
      NSLayoutConstraint.activate([
        dragArea.leadingAnchor.constraint(equalTo: content.leadingAnchor),
        dragArea.trailingAnchor.constraint(equalTo: root.view.trailingAnchor),
        dragArea.topAnchor.constraint(equalTo: root.view.topAnchor),
        dragArea.bottomAnchor.constraint(
          equalTo: root.view.safeAreaLayoutGuide.topAnchor, constant: PaxTheme.Spacing.pageTop),
      ])
    }
    workbenchRoot = root
    contentViewController = root
    fitOversizedWindow()
    let environment = ProcessInfo.processInfo.environment
    if let name = environment["PAX_DEMO_SCENARIO"],
      let preset = WorkbenchDemoStore.Scenario(rawValue: name)
    {
      store?.perform(.selectScenario(preset))
    }
    if environment["PAX_START_PAGE"] == "routes" || environment["PAX_DEMO_ROUTES"] == "1" {
      shell.showRoutes()
    }
    if environment["PAX_DEMO_GALLERY"] == "1" { shell.showComponents() }
    if environment["PAX_DEMO_LOGS"] == "1" { shell.showLogs() }
    if environment["PAX_DEMO_SETTINGS"] == "1" { shell.showSettings() }
    if environment["PAX_START_PAGE"] == "ai-usage" { shell.showAIUsage() }
    installMenu()
    shell.onPresentationChange = { [weak self] context in self?.applyLanguage(context) }
    if let context = shell.language?.context { applyLanguage(context) }
  }
  @available(*, unavailable)
  required init?(coder: NSCoder) { fatalError("代码构建") }

  private func setVisible(_ visible: Bool) {
    liveTelemetry?.setWindowVisible(visible)
    liveRefresh?.setWindowVisible(visible)
  }
  func windowDidBecomeKey(_ notification: Notification) { setVisible(true) }
  func windowDidMiniaturize(_ notification: Notification) { setVisible(false) }
  func windowDidDeminiaturize(_ notification: Notification) { setVisible(true) }
  func windowWillClose(_ notification: Notification) { setVisible(false) }
  /// 最小化、App 隐藏、被完全遮挡都会让窗口不可见，停止刷新并记脏。
  func windowDidChangeOcclusionState(_ notification: Notification) {
    liveRefresh?.setWindowVisible(window?.occlusionState.contains(.visible) ?? false)
  }
  func windowDidChangeScreen(_ notification: Notification) {
    fitOversizedWindow()
  }
  func windowWillResize(_ sender: NSWindow, to frameSize: NSSize) -> NSSize {
    guard !sender.styleMask.contains(.fullScreen), let visible = sender.screen?.visibleFrame
    else { return frameSize }
    return NSSize(
      width: min(frameSize.width, visible.width), height: min(frameSize.height, visible.height))
  }
  func windowWillUseStandardFrame(_ window: NSWindow, defaultFrame: NSRect) -> NSRect {
    window.screen?.visibleFrame ?? defaultFrame
  }
  private func fitOversizedWindow() {
    guard let window, !window.styleMask.contains(.fullScreen),
      let screen = window.screen ?? NSScreen.main
    else { return }
    let visible = screen.visibleFrame
    var frame = window.frame
    guard frame.width > visible.width || frame.height > visible.height else { return }
    frame.size = windowWillResize(window, to: frame.size)
    frame.origin.x = min(max(frame.minX, visible.minX), visible.maxX - frame.width)
    frame.origin.y = min(max(frame.minY, visible.minY), visible.maxY - frame.height)
    window.setFrame(frame, display: true)
  }
  @objc private func showAbout() {
    window?.deminiaturize(nil)
    showWindow(nil)
    NSApp.activate(ignoringOtherApps: true)
    shell.showAbout()
  }
  @objc private func showGallery() { shell.showComponents() }
  @objc private func showGettingStarted() { shell.showGettingStarted() }
  @objc private func find() { shell.focusSearch() }
  // AppKit rewrites the title of undo: using the system language.
  @objc private func undoEdit() { NSApp.keyWindow?.firstResponder?.undoManager?.undo() }
  func validateMenuItem(_ menuItem: NSMenuItem) -> Bool {
    if menuItem.action == #selector(undoEdit) {
      return NSApp.keyWindow?.firstResponder?.undoManager?.canUndo == true
    }
    return true
  }
  @objc private func minimumSize() { window?.setContentSize(PaxTheme.Layout.minimumWindow) }
  @objc private func designSize() {
    window?.setContentSize(PaxTheme.Layout.defaultWindow)
    fitOversizedWindow()
  }
  var menuDemoStore: WorkbenchDemoStore? { store }
  func openMenuDestination(_ destination: MenuBarMenuController.Destination) {
    guard let window else { return }
    window.deminiaturize(nil)
    showWindow(nil)
    NSApp.activate(ignoringOtherApps: true)
    guard window.attachedSheet == nil, !hasPresentedController(shell) else { return }
    switch destination {
    case .dashboard: shell.showDashboardPage()
    case .routes: shell.showRoutes()
    case .importRoutes: shell.showRouteImport()
    case .traffic: shell.showTrafficAnalysis()
    case .logs: shell.showLogs()
    case .settings: shell.showSettings()
    case .diagnostics: shell.showConnectionDiagnostics()
    case .toggleProxy: shell.requestProxyFromMenu()
    }
  }
  private func hasPresentedController(_ controller: NSViewController) -> Bool {
    controller.presentedViewControllers?.isEmpty == false
      || controller.children.contains(where: hasPresentedController)
  }
  func showDashboard() { contentViewController = workbenchRoot }
  func showSetup(_ controller: DaemonSetupViewController) { contentViewController = controller }
  func stopDemo() { store?.cancelPendingAction() }

  @objc private func previewScenario(_ sender: NSMenuItem) {
    store?.perform(.selectScenario(WorkbenchDemoStore.Scenario.allCases[sender.tag]))
  }
  private func installMenu() {
    let main = NSMenu()
    let appItem = NSMenuItem()
    main.addItem(appItem)
    let appMenu = NSMenu()
    appItem.title = "Pax Connect"
    appItem.submenu = appMenu
    let about = appMenu.addItem(
      withTitle: "关于 Pax Connect", action: #selector(showAbout), keyEquivalent: "")
    about.target = self
    appMenu.addItem(.separator())
    appMenu.addItem(
      withTitle: "退出 Pax Connect", action: #selector(NSApplication.terminate(_:)),
      keyEquivalent: "q")
    let editItem = NSMenuItem()
    editItem.title = "编辑"
    main.addItem(editItem)
    let edit = NSMenu(title: "编辑")
    editItem.submenu = edit
    let undo = edit.addItem(withTitle: "撤销", action: #selector(undoEdit), keyEquivalent: "z")
    undo.target = self
    edit.addItem(withTitle: "剪切", action: #selector(NSText.cut(_:)), keyEquivalent: "x")
    edit.addItem(withTitle: "复制", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
    edit.addItem(withTitle: "粘贴", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
    edit.addItem(withTitle: "全选", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
    let viewItem = NSMenuItem()
    viewItem.title = "显示"
    main.addItem(viewItem)
    let menu = NSMenu(title: "显示")
    viewItem.submenu = menu
    let findItem = menu.addItem(withTitle: "搜索当前页面", action: #selector(find), keyEquivalent: "f")
    findItem.target = self
    if store != nil {
      let componentItem = menu.addItem(
        withTitle: "组件目录", action: #selector(showGallery), keyEquivalent: "2")
      componentItem.target = self
    }
    let minimum = menu.addItem(
      withTitle: "最小窗口 · 1000×680", action: #selector(minimumSize), keyEquivalent: "")
    minimum.target = self
    let design = menu.addItem(
      withTitle: "设计窗口 · 1280×860", action: #selector(designSize), keyEquivalent: "")
    design.target = self
    let helpItem = NSMenuItem(title: "帮助", action: nil, keyEquivalent: "")
    let help = NSMenu(title: "帮助")
    helpItem.submenu = help
    let start = help.addItem(
      withTitle: "开始使用", action: #selector(showGettingStarted), keyEquivalent: "")
    start.target = self
    main.addItem(helpItem)
    NSApp.helpMenu = help
    guard store != nil else {
      NSApp.mainMenu = main
      return
    }
    let previewItem = NSMenuItem(title: "预览", action: nil, keyEquivalent: "")
    main.addItem(previewItem)
    let previewMenu = NSMenu(title: "预览")
    previewItem.submenu = previewMenu
    for (index, value) in WorkbenchDemoStore.Scenario.allCases.enumerated() {
      let item = previewMenu.addItem(
        withTitle: "场景：" + value.rawValue, action: #selector(previewScenario(_:)), keyEquivalent: ""
      )
      item.tag = index
      item.target = self
    }
    previewMenu.addItem(.separator())
    for scene in WorkbenchShellViewController.routingPreviewScenes {
      let item = previewMenu.addItem(
        withTitle: "分流：" + scene, action: #selector(previewRouting(_:)), keyEquivalent: "")
      item.representedObject = scene
      item.target = self
    }
    for scene in WorkbenchShellViewController.logsPreviewScenes {
      let item = previewMenu.addItem(
        withTitle: "日志：" + scene, action: #selector(previewLogs(_:)), keyEquivalent: "")
      item.representedObject = scene
      item.target = self
    }
    NSApp.mainMenu = main
  }
  private func applyLanguage(_ context: PresentationContext) {
    guard let main = NSApp.mainMenu else { return }
    let keys: [Selector: String] = [
      #selector(showAbout): "app.about", #selector(NSApplication.terminate(_:)): "app.quit",
      #selector(undoEdit): "app.undo", #selector(NSText.cut(_:)): "app.cut",
      #selector(NSText.copy(_:)): "app.copy", #selector(NSText.paste(_:)): "app.paste",
      #selector(NSText.selectAll(_:)): "app.select_all", #selector(find): "app.search",
      #selector(showGallery): "app.components", #selector(minimumSize): "app.minimum_window",
      #selector(designSize): "app.design_window",
      #selector(showGettingStarted): "app.getting_started",
    ]
    for item in main.items {
      if item.submenu === NSApp.helpMenu { item.title = context.text("app.help") }
      if item.submenu?.items.contains(where: { $0.action == #selector(NSText.copy(_:)) }) == true {
        item.title = context.text("app.edit")
      }
      if item.submenu?.items.contains(where: { $0.action == #selector(find) }) == true {
        item.title = context.text("app.view")
      }
      item.submenu?.title = item.title
      for child in item.submenu?.items ?? [] {
        if let action = child.action, let key = keys[action] { child.title = context.text(key) }
      }
    }
  }
  @objc private func previewLogs(_ sender: NSMenuItem) {
    if let scene = sender.representedObject as? String { shell.previewLogs(scene) }
  }
  @objc private func previewRouting(_ sender: NSMenuItem) {
    if let scene = sender.representedObject as? String { shell.previewRouting(scene) }
  }
}

/// Extends the transparent title bar over the workspace's empty top inset.
private final class WorkbenchTitlebarDragArea: NSView {
  override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

  override func mouseDown(with event: NSEvent) {
    guard let window, !window.styleMask.contains(.fullScreen) else { return }
    if event.clickCount == 1 {
      window.performDrag(with: event)
    } else if event.clickCount == 2 {
      switch UserDefaults.standard.string(forKey: "AppleActionOnDoubleClick") {
      case "Minimize": window.miniaturize(nil)
      case "Maximize", "Zoom", nil: window.zoom(nil)
      default: break
      }
    }
  }
}
