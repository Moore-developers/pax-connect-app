import AppKit
import Features
import PaxCore

/// App 生命周期与依赖装配（architecture.md §10：App target 只做组装）。
///
/// 建窗口与唯一连接会话，把有序更新灌进 Store。
/// 用户后台由 PaxdLauncher 作为子进程启动与退出。
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
  lazy var commandConnection = DaemonConnection(
    socketPath: debugSocket ?? paxdSocket,
    expectedOwnerPID: debugSocket == nil ? UInt32(ProcessInfo.processInfo.processIdentifier) : nil,
    expectedFingerprint: debugSocket == nil ? DaemonInstaller.fingerprint : nil)
  lazy var aiProfilesStore = AIProfilesStore(connection: commandConnection)
  lazy var aiUsageStore = AIUsageDataStore(connection: commandConnection)
  private lazy var routesStore: RoutesStore = makeRoutesStore()
  private let diagnosticBrowser = DiagnosticBrowserOpener()
  private func makeRoutesStore() -> RoutesStore {
    let connection = commandConnection
    return RoutesStore(
      preview: { try await connection.previewRoutes($0, subscription: $1, sourceName: $2) },
      cancel: { try await connection.cancelPreview($0) },
      cancelOperation: { try await connection.cancelOperation($0) },
      editor: { try await connection.routeEditor($0) },
      command: { intent in
        switch intent {
        case .select(let route): try await connection.selectRoute(route)
        case .delete(let route): try await connection.deleteRoute(route)
        case .save(let edit): try await connection.saveRoute(edit)
        case .saveChain(let edit): try await connection.saveChainRoute(edit)
        case .saveGroups(let edit): try await connection.saveRouteGroups(edit)
        case .confirmTLSBatch(let routes): try await connection.confirmTLSBatch(routes)
        case .automaticImport(let preview, let items):
          try await connection.confirmPreview(preview, items: items, automatic: true)
        case .confirm(let preview, let items):
          try await connection.confirmPreview(preview, items: items)
        case .restore(let route): try await connection.restoreRoute(route)
        case .refresh(let source): try await connection.refreshSubscription(source)
        case .refreshSaved(let source):
          try await connection.refreshSubscription(source, useLastChannel: true)
        case .replaceSource(let source, let address, let agent):
          try await connection.refreshSubscription(
            source, replacementURL: address, userAgent: agent, useLastChannel: true)
        case .refreshVia(let source, let route):
          try await connection.refreshSubscription(source, via: route)
        case .deleteSubscription(let source, let revision):
          try await connection.deleteSubscription(source, catalogRevision: revision)
        case .test(let route): try await connection.testRoute(route)
        case .diagnose(let route): try await connection.testRoute(route, diagnostic: true)
        case .queryExit(let route): try await connection.testRoute(route, exit: true)
        case .queryExitBatch(let routes):
          try await connection.testRoutes(routes, download: false, exit: true)
        case .download(let route): try await connection.testRoute(route, download: true)
        case .testBatch(let routes, let download):
          try await connection.testRoutes(routes, download: download)
        }
      }, history: { try await connection.routeHistory($0, cursor: $1) },
      periodUsage: { try await connection.routeUsage($0, window: $1) },
      usage: { try await connection.routeUsage($0, cursor: $1, from: $2) },
      measurement: { try await connection.measurement($0) })
  }
  lazy var telemetry: TelemetryStore = makeTelemetry()
  private func makeTelemetry() -> TelemetryStore {
    let c = commandConnection
    return TelemetryStore(
      geoRules: { try await c.geoRules(search: $0, offset: $1) },
      routingLibrary: { try await c.routingLibrary() },
      mutateRoutingLibrary: { try await c.mutateRoutingLibrary($0) },
      routingRules: { try await c.routingRules() },
      icon: { try await c.objectIcon(host: $0, fetch: $1, generation: $2) },
      recordDetail: { try await c.logRecordDetail($0) }, updates: { try await c.logUpdates($0) },
      logs: { try await c.queryLogs($0) }, objects: { try await c.queryLogObjects($0) },
      detail: { try await c.logDetail($0) }, traffic: { try await c.queryTraffic($0) },
      trafficDetail: { try await c.trafficDetail($0) },
      clear: { try await c.clearLogs($0, at: $1) },
      retention: { try await c.saveLogRetention($0, revision: $1, id: $2, at: $3) })
  }
  /// 各页刷新策略的唯一入口：由 Watch 更新驱动，页面通过 `bind` / `pageDidAppear` 接入。
  lazy var refresh = PaxRefresh(connection: commandConnection, telemetry: telemetry)
  lazy var store: ConnectionStore = makeStore()
  private func makeStore() -> ConnectionStore {
    let connection = commandConnection
    let diagnosticBrowser = diagnosticBrowser
    let history =
      debugSocket == nil
      ? FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first.map {
        NetworkDiagnosticHistory(
          url: $0.appendingPathComponent("Pax Connect/diagnostics/latest-network.json"))
      } : nil
    let store = ConnectionStore(
      logCapacity: 20, daemonVersion: nil,
      proxyCommand: { enabled in
        try await connection.setProxyEnabled(enabled)
      }, tlsCommand: { request in try await connection.updateRouteTLS(request) },
      routesStore: routesStore,
      trafficPathStore: TrafficPathStore(
        network: NetworkDiagnosticStore(
          currentEnvironment: EnvironmentDiagnosticStore(
            history: history,
            request: { try await connection.inspectEnvironment(action: $0, sessionID: $1) },
            openBrowser: { await diagnosticBrowser.open($0) }),
          history: history,
          readProgress: { try await connection.networkDiagnosticProgress(operationID: $0) },
          run: { try await connection.diagnoseNetwork(mode: $0, operationID: $1) }),
        load: { try await connection.trafficPathDiagnostics() },
        recheck: { try await connection.trafficPathDiagnostics(recheck: true) }),
      reconnectCommand: { try await connection.reconnectProxy() },
      currentPathCommand: { try await connection.recheckCurrentPath($0) })
    return store
  }
  private lazy var powerForwarder = makePowerForwarder()
  private func makePowerForwarder() -> PowerEventForwarder {
    PowerEventForwarder(
      status: { [weak self] failed in self?.store.setPowerForwardingFailed(failed) },
      send: { [weak self] sleeping, sequence in
        guard let self else { return }
        try await self.commandConnection.notifyPowerEvent(sleeping: sleeping, sequence: sequence)
      })
  }
  @objc private func willSleep(_ notification: Notification) {
    powerForwarder.notify(sleeping: true)
  }
  @objc private func didWake(_ notification: Notification) {
    powerForwarder.notify(sleeping: false)
  }
  lazy var launcher = PaxdLauncher(
    bundleURL: Bundle.main.bundleURL, ownerPID: ProcessInfo.processInfo.processIdentifier)
  private lazy var coreLauncher = CoreLauncher(
    bundleURL: Bundle.main.bundleURL, ownerPID: ProcessInfo.processInfo.processIdentifier)
  private lazy var coreAuthorization = AppSessionStore(
    start: { [weak self] password in
      guard let self, DaemonInstaller.validate() == nil else {
        throw AppSessionFailure.invalidBundle
      }
      try await self.coreLauncher.start(password: password)
    }, stop: {})
  var authorizationController: AppSessionAuthorizationController?
  var authorizationTask: Task<Void, Never>?
  var paxdSocket = ""
  var startupTask: Task<Void, Never>?
  var terminationTask: Task<Void, Never>?
  var debugSocket: String? {
    let socket = ProcessInfo.processInfo.environment["PAX_SOCKET"] ?? ""
    return socket.isEmpty ? nil : socket
  }
  private var menuBar: MenuBarController?
  var windowController: WorkbenchWindowController?
  private var workbenchWindow: WorkbenchWindowController?
  /// Demo and external sockets are explicit development entry points.
  private var usesWorkbench: Bool { ProcessInfo.processInfo.environment["PAX_MODE"] == "demo" }
  var connectionGeneration: UInt64 = 0
  var connectionTask: Task<Void, Never>?
  var stableBackendTask: Task<Void, Never>?
  var automaticBackendRecoveryAvailable = true

  func applicationDidFinishLaunching(_ notification: Notification) {
    menuBar = MenuBarController()
    if usesWorkbench {
      let window = WorkbenchWindowController()
      workbenchWindow = window
      if let demo = window.menuDemoStore {
        menuBar?.attach(
          MenuBarMenuController(
            demo: demo,
            onNavigate: { [weak window] in window?.openMenuDestination($0) },
            onQuit: { NSApp.terminate(nil) }))
        let preview = NSMenuItem(
          title: "菜单栏快捷菜单", action: #selector(previewStatusMenu), keyEquivalent: "")
        preview.target = self
        NSApp.mainMenu?.items.first { $0.title == "预览" }?.submenu?.addItem(preview)
      }
      window.showWindow(nil)
      NSApp.activate(ignoringOtherApps: true)
      return
    }
    launcher.onUnexpectedExit = { [weak self] code in
      self?.backendExited(code)
    }
    recoverBackend()
  }

  @objc private func previewStatusMenu() {
    guard let view = workbenchWindow?.window?.contentView else { return }
    menuBar?.showMenuPreview(in: view)
  }

  func showLiveWindow() {
    let notifications = NSWorkspace.shared.notificationCenter
    notifications.addObserver(
      self, selector: #selector(willSleep(_:)), name: NSWorkspace.willSleepNotification, object: nil
    )
    notifications.addObserver(
      self, selector: #selector(didWake(_:)), name: NSWorkspace.didWakeNotification, object: nil)
    store.requestBackendRecovery = { [weak self] in self?.recoverBackend() }
    if debugSocket == nil {
      store.requestAuthorization = { [weak self] in self?.authorizeCore() }
    }
    let window = WorkbenchWindowController(
      routes: routesStore, connection: store, telemetry: telemetry, aiProfiles: aiProfilesStore,
      aiUsage: aiUsageStore, dashboardSources: DashboardSources(connection: commandConnection),
      refresh: refresh)
    window.showWindow(nil)
    windowController = window
    menuBar?.attach(
      MenuBarMenuController(
        routes: routesStore, connection: store,
        onNavigate: { [weak self] destination in
          guard let self else { return }
          if case .toggleProxy = destination, self.store.proxyConfirmationRoute == nil {
            let enabled = !self.store.proxyEnabled
            Task { await self.store.setProxyFromWorkbench(enabled) }
          } else {
            self.windowController?.openMenuDestination(destination)
          }
        }, onQuit: { NSApp.terminate(nil) }))
    NSApp.activate(ignoringOtherApps: true)
  }

  private func authorizeCore() {
    windowController?.openMenuDestination(.dashboard)
    guard terminationTask == nil, authorizationTask == nil, authorizationController == nil,
      let window = windowController?.window
    else { return }
    let token = store.coreAuthorizationToken
    authorizationTask = Task { [weak self] in
      guard let self else { return }
      self.coreAuthorization.markUnavailable()
      await self.coreAuthorization.start()
      guard !Task.isCancelled, self.terminationTask == nil,
        token == self.store.coreAuthorizationToken, self.store.needsServiceAuthorization
      else {
        self.authorizationTask = nil
        return
      }
      if self.coreAuthorization.state == .running {
        await self.store.resumeProxyAfterAuthorization(token: token)
      } else {
        let controller = AppSessionAuthorizationController(
          store: self.coreAuthorization,
          onReady: { [weak self] in
            guard let self else { return }
            self.authorizationController?.dismiss(nil)
            self.authorizationController = nil
            Task { await self.store.resumeProxyAfterAuthorization(token: token) }
          },
          onCancel: { [weak self] in
            self?.store.cancelCoreAuthorization()
            self?.authorizationController = nil
          })
        self.authorizationController = controller
        window.contentViewController?.presentAsSheet(controller)
      }
      self.authorizationTask = nil
    }
  }

  func applicationDidBecomeActive(_ notification: Notification) {
    if windowController != nil && !usesWorkbench { telemetry.setAppActive(true) }
  }
  func applicationDidResignActive(_ notification: Notification) {
    if windowController != nil && !usesWorkbench { telemetry.setAppActive(false) }
  }
  func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool
  {
    if !flag { (windowController ?? workbenchWindow)?.showWindow(nil) }
    return true
  }

  func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
    guard !usesWorkbench, debugSocket == nil else { return .terminateNow }
    guard terminationTask == nil else { return .terminateLater }
    menuBar?.content?.isTerminating = true
    terminationTask = Task { [weak self] in
      guard let self else {
        sender.reply(toApplicationShouldTerminate: true)
        return
      }
      self.stableBackendTask?.cancel()
      self.startupTask?.cancel()
      self.connectionGeneration &+= 1
      self.connectionTask?.cancel()
      await self.startupTask?.value
      await self.connectionTask?.value
      self.store.cancelCoreAuthorization()
      self.authorizationTask?.cancel()
      await self.authorizationTask?.value
      self.authorizationController?.dismiss(nil)
      self.authorizationController = nil
      do {
        do { try await self.commandConnection.shutdownAppSession() } catch {
          try await self.coreLauncher.stop()
        }
        try await self.launcher.stop()
        self.powerForwarder.stop()
        NSWorkspace.shared.notificationCenter.removeObserver(self)
        sender.reply(toApplicationShouldTerminate: true)
      } catch {
        sender.reply(toApplicationShouldTerminate: false)
        let alert = NSAlert()
        alert.messageText = "后台尚未关闭"
        alert.informativeText = "请重试退出。"
        alert.runModal()
      }
      self.terminationTask = nil
      self.menuBar?.content?.isTerminating = false
    }

    return .terminateLater
  }

  /// 关掉主窗口不退出 App：代理与后台服务与窗口无关（§19.1 稳定性要求）。
  func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
    false
  }

  func applicationWillTerminate(_ notification: Notification) {
    diagnosticBrowser.stopPreservingFocus()
    workbenchWindow?.stopDemo()
    stableBackendTask?.cancel()
    startupTask?.cancel()
    connectionGeneration &+= 1
    connectionTask?.cancel()
  }

}
