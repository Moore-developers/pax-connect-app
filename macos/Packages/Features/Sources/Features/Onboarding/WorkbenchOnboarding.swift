import AppKit
import Observation
import PaxCore

extension WorkbenchShellViewController {
  func installOnboarding() {
    gettingStarted.onPrimary = { [weak self] in self?.advanceOnboarding() }
    gettingStarted.onSecondary = { [weak self] in self?.secondaryOnboarding() }
    gettingStarted.onSkip = { [weak self] in self?.onboarding.dismiss() }
    observeOnboarding()
  }
  private func observeOnboarding() {
    withObservationTracking {
      _ = (
        menuConnection?.initialInstall, menuConnection?.connection, menuConnection?.status,
        menuConnection?.proxyEnabled, menuConnection?.todayTraffic, menuConnection?.requesting,
        menuConnection?.needsServiceAuthorization, menuConnection?.feedback,
        onboardingRoutes?.routes, onboardingRoutes?.selectedRouteID,
        onboardingData?.lastUsed, onboardingData?.bucketsLoaded, onboardingData?.state,
        onboarding.path, onboarding.networkCompleted, onboarding.modelsCompleted
      )
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observeOnboarding() }
    }
    if let initial = menuConnection?.initialInstall { onboarding.identify(initialInstall: initial) }
    if let connection = menuConnection, connection.connection == .connected {
      onboarding.recordNetwork(
        connected: connection.proxyEnabled && connection.status != .starting,
        hasRecords: connection.todayTraffic.totalBytes > 0
          || connection.todayTraffic.activeConnections > 0)
    }
    onboarding.recordModels(hasRecords: onboardingData?.lastUsed.isEmpty == false)
    updateOnboarding()
  }
  public func showGettingStarted() {
    onboarding.reopen()
    showDashboardPage()
    updateOnboarding()
  }
  func updateOnboarding() {
    guard let connection = menuConnection else { return }
    let blocked =
      connection.connection != .connected || connection.requesting
      || connection.needsServiceAuthorization
      || connection.feedback.contains {
        ["proxy", "connection", "selection", "current-path", "degraded"].contains($0.id)
      }
    let visible = onboarding.path != nil && !blocked
    dashboard.onboardingActive = visible
    if dashboard.isViewLoaded { dashboard.applyLiveAction() }
    guard visible, let path = onboarding.path else {
      gettingStarted.apply(nil)
      return
    }
    let c = language?.context ?? .chinese
    let model: GettingStartedView.Model
    switch path {
    case .welcome:
      model = .init(
        title: c.text("onboarding.welcome_title"),
        detail: c.text("onboarding.welcome_detail"),
        primary: c.text("onboarding.configure_network"), secondary: c.text("onboarding.view_usage"))
    case .network:
      switch networkStep {
      case .addRoute:
        model = .init(
          title: c.text("onboarding.first_route_title"),
          detail: c.text("onboarding.first_route_detail"),
          primary: c.text("onboarding.import_routes"),
          secondary: c.text("onboarding.no_route_help"))
      case .selectRoute:
        model = .init(
          title: c.text("onboarding.select_title"), detail: c.text("onboarding.select_detail"),
          primary: c.text("onboarding.select_route"),
          secondary: c.text("onboarding.view_usage"))
      case .connect:
        model = .init(
          title: c.text("onboarding.connect_title"), detail: c.text("onboarding.connect_detail"),
          primary: c.text("onboarding.connect"),
          secondary: c.text("onboarding.other_route"))
      case .waiting:
        model = .init(
          title: c.text("onboarding.waiting_title"), detail: c.text("onboarding.waiting_detail"),
          primary: c.text("onboarding.view_traffic"),
          secondary: c.text("onboarding.view_logs"))
      case .complete:
        model = .init(
          title: c.text("onboarding.network_recording"),
          detail: c.text("onboarding.network_complete_detail"),
          primary: c.text("onboarding.complete"),
          secondary: c.text("onboarding.view_usage"))
      }
    case .models:
      if onboarding.modelsCompleted {
        model = .init(
          title: c.text("onboarding.usage_loaded"),
          detail: c.text("onboarding.usage_loaded_detail"),
          primary: c.text("onboarding.complete"), secondary: c.text("onboarding.manage_profiles"))
      } else {
        model = .init(
          title: c.text("onboarding.usage_title"),
          detail: c.text("onboarding.usage_detail"),
          primary: c.text("onboarding.usage_action"),
          secondary: c.text("onboarding.manage_profiles"))
      }
    }
    gettingStarted.apply(model, skipTitle: c.text("onboarding.later"))
  }
  private var networkStep: OnboardingStore.NetworkStep {
    onboarding.networkStep(
      hasRoutes: onboardingRoutes?.routes.contains { !$0.hidden } == true,
      hasSelection: onboardingRoutes?.routes.contains {
        !$0.hidden && $0.id == onboardingRoutes?.selectedRouteID
      } == true,
      connected: menuConnection?.proxyEnabled == true)
  }
  private func advanceOnboarding() {
    switch onboarding.path {
    case .welcome:
      onboarding.choose(.network)
      showRoutes()
    case .models:
      if onboarding.modelsCompleted { onboarding.dismiss() } else { showAIUsage() }
    case .network:
      switch networkStep {
      case .addRoute: showRouteImport()
      case .selectRoute: showRoutes()
      case .connect:
        guard let connection = menuConnection else { return }
        ProxyTransitionPresenter.request(from: dashboard, connection: connection, enabled: true)
      case .waiting: showTrafficAnalysis()
      case .complete: onboarding.dismiss()
      }
    case nil: break
    }
  }
  private func secondaryOnboarding() {
    let c = language?.context ?? .chinese
    switch onboarding.path {
    case .welcome:
      onboarding.choose(.models)
      showAIUsage()
    case .models:
      onboardingUsage?.showConfiguration()
      showAIUsage()
    case .network:
      switch networkStep {
      case .addRoute:
        guard let window = view.window else { return }
        let alert = NSAlert()
        alert.messageText = c.text("onboarding.bring_routes_title")
        alert.informativeText =
          c.text("onboarding.bring_routes_detail")
        alert.addButton(withTitle: c.text("onboarding.understood"))
        alert.beginSheetModal(for: window)
      case .connect: showRoutes()
      case .waiting: showLogs()
      case .selectRoute, .complete:
        onboarding.choose(.models)
        showAIUsage()
      }
    case nil: break
    }
  }
}
