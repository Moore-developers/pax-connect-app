import AppKit
import PaxCore
import PaxUI

/// PaxRefresh 驱动的读取结果；刷新失败只让对应卡显示失败，不阻塞其它卡。
struct DashboardLiveState {
  var trendRouteID = ""
  var trendHistory: [RouteHistorySample] = []
  var trendConfigRevision: UInt64 = 0
  var problemsFailed = false
  var operationsFailed = false
  var problemsLoaded = false
  var operationsLoaded = false
  var trendFailed = false
  var usageBuckets: [AIUsageBucket] = []
  var usageState: AIUsageCollectionState?
  var usageUpdated: Date?
  var usageLoaded = false
  var usageFailed = false
  var usageGeneration: UInt64?
  var usageSession: UInt64?
  var selectedUsageFilter = PaxUsageFilter.all
  var problems: [ProductLog] = []
  var operations: [ProductLog] = []
  var activityInvalidation: UInt64?
  var trafficFailed = false
  var trafficTrendPage: TrafficPage?
  var trafficTrendFailed = false
}

extension WorkbenchDashboardViewController {
  func installOverviewActions() {
    trendCard.onSelectFilter = { [weak self] filter in
      guard let self else { return }
      self.live.selectedUsageFilter = filter
      if let store = self.store {
        self.applyDemoOverview(store)
      } else {
        self.applyUsage(trendOnly: true)
      }
    }
    rankCard.onOpen = { [weak self] in self?.onShowAIUsage?() }
    for card in [attentionCard, changesCard] {
      card.onSelect = { [weak self] _ in self?.onShowLogs?() }
    }
    actionCard.onPrimary = { [weak self] in self?.performAction(primary: true) }
    actionCard.onSecondary = { [weak self] in self?.performAction(primary: false) }
  }

  func performAction(primary: Bool) {
    guard let action = currentAction else { return }
    switch action {
    case .addRoute:
      if store != nil { showSubscription() } else { onShowRoutes?() }
    case .channelFailed:
      guard let connection = liveConnection else { return }
      if primary {
        if let diagnostics = connection.trafficPathStore {
          presentWorkbenchModal(TrafficPathController(store: diagnostics, connection: connection))
        } else {
          onShowRoutes?()
        }
      } else {
        Task { await connection.recheckCurrentPath() }
      }
    case .authorize:
      if let connection = liveConnection {
        ProxyTransitionPresenter.request(from: self, connection: connection, enabled: true)
      }
    }
  }

  func applyAction(_ requested: DashboardInsightProjection.Action?) {
    let action = onboardingActive && requested == .addRoute ? nil : requested
    guard currentAction != action else { return }
    onPresentRegion?("action")
    currentAction = action
    let wasVisible = document.showsAction
    document.showsAction = action != nil
    if wasVisible != document.showsAction { document.needsLayout = true }
    if let action { actionCard.apply(DashboardInsightProjection.actionModel(action)) }
  }

  /// 当前线路：代理运行中取实际线路，否则取选定线路。
  func overviewRoute(routes: RoutesStore, connection: ConnectionStore, running: Bool)
    -> NodeProfile?
  {
    let id = running ? connection.currentRoute?.id : routes.selectedRouteID
    return routes.routes.first { $0.id == id && !$0.hidden }
  }

  func applyRoute(routes: RoutesStore, connection: ConnectionStore, running: Bool) {
    onPresentRegion?("route")
    let route = overviewRoute(routes: routes, connection: connection, running: running)
    let badge =
      connection.connection != .connected ? "最后线路" : running ? "当前线路" : "选定线路"
    // 切换线路后旧线路的趋势立即撤下：历史只在 id 与配置版本都匹配时使用。
    let history =
      route.map {
        $0.id == live.trendRouteID && $0.configRevision == live.trendConfigRevision
          ? live.trendHistory : []
      } ?? []
    var routeModel = DashboardRouteProjection.card(
      route: route, badge: badge,
      exitIP: route.flatMap { savedExit($0)?.ip },
      exitRegion: route.flatMap { savedExit($0)?.region }, running: running, history: history)
    if live.trendFailed, route != nil, routeModel.series.points.isEmpty {
      routeModel.trendEmpty = "延迟趋势读取失败"
    } else if live.trendFailed {
      routeModel.note += " · 趋势更新失败"
    }
    routeCard.apply(routeModel)
  }

  func applyActivity(problems: Bool = true, operations: Bool = true) {
    guard let connection = liveConnection else { return }
    let fresh = telemetry == nil || live.activityInvalidation == telemetry?.invalidationGeneration
    let logsUnavailable = telemetry.map { !($0.available && $0.logViewsAvailable) } ?? false
    let activityReady = telemetry == nil || (fresh && live.activityInvalidation != nil)
    if problems {
      onPresentRegion?("problems")
      let attention = DashboardInsightProjection.attention(
        feedback: connection.feedback, problems: fresh ? live.problems : [])
      attentionCard.apply(
        PaxInsightModel(
          items: attention,
          emptyTitle: live.problemsFailed || logsUnavailable
            ? "问题摘要暂不可用" : activityReady && live.problemsLoaded ? "一切正常 · 今日暂无问题" : "正在读取问题摘要",
          updateFailed: live.problemsFailed)
      )
    }
    if operations {
      onPresentRegion?("operations")
      changesCard.apply(
        PaxInsightModel(
          items: DashboardInsightProjection.changes(operations: fresh ? live.operations : []),
          emptyTitle: live.operationsFailed || logsUnavailable
            ? "操作记录暂不可用" : activityReady && live.operationsLoaded ? "今日暂无 Pax 操作记录" : "正在读取操作记录",
          updateFailed: live.operationsFailed)
      )
    }
  }

  func applyLiveAction() {
    guard let connection = liveConnection, let routes = liveRoutes else { return }
    let channelFailed =
      connection.currentPathSupported && connection.proxyEnabled
      && connection.currentPathCheck.state == "failed"
    applyAction(
      DashboardInsightProjection.action(
        hasRoutes: routes.routes.contains { !$0.hidden }, channelFailed: channelFailed,
        needsAuthorization: connection.needsServiceAuthorization))
  }

  /// 推送提供占比 / 时间，以及查询缺统计时的兜底；仍保留首次今日成功读取门槛。
  var todaySnapshot: TrafficSnapshot? {
    guard todayLoaded, todayValid, liveConnection?.connection == .connected,
      let snapshot = liveConnection?.todayTraffic,
      snapshot.asOf.map({ Calendar.current.isDateInToday($0) }) != false
    else { return nil }
    return snapshot
  }

  func applyTraffic() {
    onPresentRegion?("today")
    let page = todayLoaded && todayValid ? todayPage : nil
    var model = DashboardTrafficProjection.card(
      snapshot: todaySnapshot, page: page, trendPage: page == nil ? nil : live.trafficTrendPage,
      failed: live.trafficFailed || (telemetry.map { !$0.trafficAvailable } ?? false), now: Date())
    if page != nil && live.trafficTrendFailed {
      model.hourly = []
      model.hourlyNote = "趋势暂不可用"
    }
    if live.trafficFailed && page != nil { model.asOf += " · 更新失败" }
    trafficCard.apply(model)
  }

  func syncOverviewValidity() {
    guard let telemetry else { return }
    if let generation = live.usageGeneration,
      generation != (telemetry.aiUsage?.generation ?? 0)
        || (telemetry.connected && live.usageSession != telemetry.sessionGeneration)
    {
      live.usageBuckets = []
      live.usageState = telemetry.aiUsage
      live.usageUpdated = nil
      live.usageLoaded = false
      live.usageFailed = false
      live.usageGeneration = nil
    }
    if let generation = live.activityInvalidation, generation != telemetry.invalidationGeneration {
      live.problems = []
      live.operations = []
      live.activityInvalidation = nil
      live.problemsLoaded = false
      live.operationsLoaded = false
      live.problemsFailed = false
      live.operationsFailed = false
    }
  }

  func applyUsage(trendOnly: Bool = false) {
    onPresentRegion?(trendOnly ? "usageTrend" : "usage")
    let now = Date()
    if live.usageLoaded || live.usageFailed {
      var trend = DashboardUsageProjection.trend(
        buckets: live.usageBuckets, state: live.usageState,
        failed: live.usageFailed && !live.usageLoaded,
        updatedAt: live.usageUpdated, now: now, filter: live.selectedUsageFilter)
      if live.usageFailed && live.usageLoaded { trend.updated += " · 更新失败" }
      trendCard.apply(trend)
      if !trendOnly {
        var rank = DashboardUsageProjection.rank(
          buckets: live.usageBuckets, state: live.usageState,
          failed: live.usageFailed && !live.usageLoaded, now: now)
        if live.usageFailed && live.usageLoaded { rank.caption += " · 更新失败" }
        rankCard.apply(rank)
      }
    } else {
      var trend = PaxModelTrendModel()
      trend.empty = sources == nil ? "用量尚未接入" : "正在读取用量"
      trendCard.apply(trend)
      var rank = PaxModelRankModel()
      rank.empty = trend.empty
      if !trendOnly { rankCard.apply(rank) }
    }
  }

  /// 已保存的出口测量（与当前配置版本一致才用）；随 Snapshot 推送的线路目录实时取值。
  private func savedExit(_ route: NodeProfile) -> (ip: String?, region: String?)? {
    guard
      let node = liveRoutes?.routes.first(where: {
        $0.id == route.id && $0.configRevision == route.configRevision
      })
    else { return nil }
    return DashboardRouteProjection.savedExit(node)
  }

}
