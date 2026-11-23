import AppKit
import PaxCore
import PaxUI

extension WorkbenchDashboardViewController {
  func applyDashboardLanguage() {
    let c = interfaceCopy.context
    analysisList.applyLanguage(c)
    analysis.applyCopy(
      title: c.text("dashboard.analysis_today"),
      dimensions: [
        RoutingWorkbenchStore.Dimension.application.title(context: c),
        RoutingWorkbenchStore.Dimension.domain.title(context: c),
        c.text("dashboard.system"),
      ],
      directions: [c.text("dashboard.all_exits"), c.text("common.proxy"), c.text("common.direct")],
      hint: c.text("dashboard.hint"))
    renderAnalysis()
    let chartSummary = stackedChartSummary(c)
    mode.apply(labels: [c.text("common.proxy"), c.text("common.direct")])
    var routeCopy = PaxCurrentRouteCard.Copy()
    routeCopy.latencyTitle = c.text("traffic_details.abe05d4485")
    routeCopy.latencyCopy = timeSeriesCopy(c)
    routeCopy.noSelection = c.text("dashboard.route.none")
    routeCopy.comparison = c.text("dashboard.route.comparison")
    routeCopy.protocolLabel = c.text("dashboard.route.protocol")
    routeCopy.exitLabel = c.text("dashboard.route.exit")
    routeCopy.measuredPrefix = c.text("dashboard.route.measured_prefix")
    routeCopy.region = c.text("dashboard.region")
    routeCopy.switchRoute = c.text("dashboard.switch")
    routeCard.applyCopy(routeCopy)
    trafficCard.applyCopy(
      title: c.text("dashboard.today_traffic"), upload: c.text("dashboard.upload"),
      download: c.text("dashboard.download"), shareTitle: c.text("dashboard.traffic_direction"),
      directions: [
        c.text("common.proxy"), c.text("common.direct"), c.text("dashboard.unclassified"),
      ], chartSummary: chartSummary)
    var trendCopy = PaxModelTrendCard.Copy()
    trendCopy.all = c.text("ai_usage.all_profiles")
    trendCopy.filter = c.text("ai_usage.trend_filters")
    trendCopy.chartSummary = chartSummary
    trendCopy.title = c.text("dashboard.trend")
    trendCopy.metrics = [c.text("ai_usage.cost"), "Token"]
    trendCopy.today = [c.text("dashboard.today_cost"), c.text("dashboard.today_tokens")]
    trendCopy.chart = [c.text("dashboard.chart_cost"), c.text("dashboard.chart_tokens")]
    trendCopy.yesterday = Array(repeating: c.text("ai_period.yesterday"), count: 2)
    trendCopy.average = c.text("dashboard.average")
    trendCopy.series = [
      c.text("ai_usage.input"), c.text("ai_usage.output"), c.text("ai_usage.cache"),
    ]

    var rankCopy = PaxModelRankCard.Copy()
    rankCopy.empty = c.text("dashboard.no_models")
    rankCopy.title = c.text("dashboard.rank")
    rankCopy.open = c.text("dashboard.open_all")
    rankCopy.today = c.text("dashboard.today_model_cost")
    rankCopy.metrics = trendCopy.metrics
    rankCopy.metricAccessibility = [
      c.text("dashboard.rank_cost_ax"), c.text("dashboard.rank_tokens_ax"),
    ]
    rankCopy.model = c.text("ai_usage.model")
    rankCopy.cost = c.text("ai_usage.cost")
    rankCopy.shares = Array(repeating: c.text("session_limits.cc87b27b64"), count: 2)
    usageCard.applyCopy(
      trendCopy, rankCopy, period: c.text("dashboard.last_ten_days"),
      leader: c.text("dashboard.leader"))
    changesCard.applyTitle(
      c.text("dashboard.changes"), failedSuffix: c.text("dashboard.update_failed"))
    modeForConnection()
    if let routes = liveRoutes, let connection = liveConnection {
      applyRoute(routes: routes, connection: connection, running: connection.proxyEnabled)
      applyTraffic()
      applyUsage()
      applyActivity()
    }
    if let action = currentAction {
      actionCard.apply(DashboardInsightProjection.actionModel(action, context: c))
    }

  }
}
