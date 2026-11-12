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
    let model: GettingStartedView.Model
    switch path {
    case .welcome:
      model = .init(
        title: "开始使用 Pax Connect",
        detail: "连接网络，查看流量去向；也可以直接查看本机模型用量。网络需要自备节点或订阅，用量来自本机客户端日志。",
        primary: "配置网络", secondary: "查看模型用量")
    case .network:
      switch networkStep {
      case .addRoute:
        model = .init(
          title: "1 · 添加你的第一条线路", detail: "导入节点分享链接或 HTTPS 订阅，也可以在线路管理中手动添加。", primary: "导入线路",
          secondary: "没有节点或订阅？")
      case .selectRoute:
        model = .init(
          title: "1 · 选择用于连接的线路", detail: "线路已保存。在线路行中选择用于连接的线路，测速可以稍后进行。", primary: "选择线路",
          secondary: "查看模型用量")
      case .connect:
        model = .init(
          title: "2 · 开启代理", detail: "已选定线路。开启后才开始接管网络；需要管理员权限时会请求授权。", primary: "开启代理",
          secondary: "选择其他线路")
      case .waiting:
        model = .init(
          title: "3 · 等待网络活动", detail: "已连接。打开一个平时访问的网站，网络记录会自动出现在流量和日志中。", primary: "查看流量",
          secondary: "查看日志")
      case .complete:
        model = .init(
          title: "已经开始记录网络活动", detail: "流量页查看应用与目标，日志页查看访问结果和问题。关闭代理后仍可查看已保存历史。", primary: "完成引导",
          secondary: "查看模型用量")
      }
    case .models:
      if onboarding.modelsCompleted {
        model = .init(
          title: "已读取本机模型用量", detail: "记录可能包含安装 Pax 之前的历史；费用为本地估算。Profile 配置可以稍后管理。",
          primary: "完成引导", secondary: "管理配置")
      } else {
        model = .init(
          title: "查看本机模型用量",
          detail: "支持 Claude Code、Codex 和 DSH。有日志就直接展示；没有记录时，在客户端完成一次请求即可。无需先开启代理或创建 Profile。",
          primary: "查看用量", secondary: "管理配置")
      }
    }
    gettingStarted.apply(model)
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
        alert.messageText = "需要自备节点或订阅"
        alert.informativeText =
          "从你使用的网络服务商获取节点分享链接、HTTPS 订阅地址或服务器参数。Pax 不提供节点。可以在线路管理中导入或手动添加，也可以稍后继续。"
        alert.addButton(withTitle: "知道了")
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
