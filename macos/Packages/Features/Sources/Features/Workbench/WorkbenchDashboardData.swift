import Foundation
import PaxCore
import PaxUI

/// 演示入口的确定样本（对齐参考稿 dashboard-overview-v2.html），只生成视图输入；
/// 所有卡片走与真实数据相同的投影，缺测 / 空态口径一致。
@MainActor
extension WorkbenchDemoStore {
  var statisticsUnavailable: Bool {
    [.empty, .loading, .trafficMissing, .trafficFailed].contains(scenario)
  }
  var statisticsMessage: String {
    switch scenario {
    case .loading: "正在加载统计"
    case .trafficFailed: "统计暂时不可用"
    case .trafficMissing: "暂无统计"
    case .empty: "暂无请求记录 · 产生网络请求后将在这里显示"
    default: "暂无匹配记录 · 切换筛选条件查看其他请求"
    }
  }

  var dashboardRouteModel: PaxCurrentRouteModel {
    let badge = scenario == .disconnected ? "最后线路" : isRunning ? "当前线路" : "选定线路"
    guard let route else {
      return DashboardRouteProjection.card(
        route: nil, badge: badge, exitIP: nil, running: false, history: [])
    }
    let base = Date(timeIntervalSince1970: 1_791_006_000)
    let recorded =
      qualityItems.first { $0.id == route.id }?.samples.compactMap(\.milliseconds) ?? []
    let values = scenario == .few ? Array(recorded.prefix(1)) : recorded
    let history =
      isRunning
      ? values.enumerated().map { index, value in
        RouteHistorySample(
          metric: DashboardRouteProjection.metric,
          at: base.addingTimeInterval(index < 2 ? 0 : Double(index) * 30 + 86_400),
          configRevision: route.configRevision, value: value, success: true)
      } : []
    return DashboardRouteProjection.card(
      route: route, badge: badge, exitIP: isRunning ? "203.0.113.8" : nil,
      running: isRunning, history: history)
  }

  var dashboardTrafficModel: PaxTodayTrafficModel {
    let now = trafficCapturedAt
    guard !statisticsUnavailable else {
      var snapshot = TrafficSnapshot.zero
      snapshot.asOf = now
      var model = DashboardTrafficProjection.card(
        snapshot: snapshot, page: scenario == .loading ? nil : TrafficPage(), now: now)
      model.upload = "—"
      model.download = "—"
      model.hourlyNote = statisticsMessage
      return model
    }
    var snapshot = TrafficSnapshot(
      uploadBytes: 220_000_000, downloadBytes: 2_180_000_000, proxiedBytes: 1_872_000_000,
      directBytes: 456_000_000, unclassifiedBytes: 72_000_000, activeConnections: 128)
    snapshot.asOf = now
    snapshot.collectionState = "collecting"
    let down: [UInt64] = [60, 80, 120, 100, 160, 140, 200, 180, 240, 220, 180, 140, 120, 160, 80]
    var page = TrafficPage()
    let start = Calendar.current.startOfDay(for: now)
    page.trend = down.enumerated().map { hour, megabytes in
      var point = TrafficPoint()
      point.at = start.addingTimeInterval(Double(hour) * 3600)
      point.seconds = 300
      point.download = megabytes * 1_000_000
      point.upload = megabytes * 100_000
      return point
    }
    return DashboardTrafficProjection.card(snapshot: snapshot, page: page, now: now)
  }

  /// 近 7 日 + 今日各模型的确定桶：今日官方 $18.60，6 个模型，全部只在内存。
  var dashboardUsageBuckets: [AIUsageBucket] {
    let calendar = Calendar.current
    let today = calendar.startOfDay(for: trafficCapturedAt)
    func totals(_ cost: Double, _ tokens: Int, _ count: Int) -> AIUsageTotals {
      var value = AIUsageTotals()
      value.count = count
      value.success = count
      value.input = tokens * 4 / 5
      value.read = value.input * 3 / 5
      value.output = tokens / 5
      value.cost = cost
      value.costs = [cost * 0.35, cost * 0.11, 0, cost * 0.54]
      value.billedCosts = value.costs
      return value
    }
    let earlier: [Double] = [12.4, 15.1, 9.8, 13.6, 14.9, 15.4]
    var buckets = earlier.enumerated().compactMap { index, cost -> AIUsageBucket? in
      calendar.date(byAdding: .day, value: index - 6, to: today).map {
        AIUsageBucket(
          at: $0.addingTimeInterval(36_000), client: .claude, modelID: "claude-opus-5-5",
          totals: totals(cost, 900_000, 800))
      }
    }
    let now: [(AIClient, String, Double, Int, Int)] = [
      (.claude, "claude-opus-5-5", 9.10, 580_000, 530),
      (.claude, "claude-haiku-5-5", 0.60, 40_000, 30),
      (.codex, "gpt-5.1-codex", 3.50, 230_000, 310),
      (.harness, "deepseek-v4", 1.68, 105_000, 180), (.claude, "deepseek-v4", 0.72, 45_000, 60),
      (.claude, "claude-sonnet-5-5", 1.90, 120_000, 120),
      (.codex, "gemini-3-pro", 1.10, 80_000, 54),
    ]
    buckets += now.map {
      AIUsageBucket(
        at: today.addingTimeInterval(32_400), client: $0.0, modelID: $0.1,
        totals: totals($0.2, $0.3, $0.4))
    }
    return buckets
  }

  var dashboardAction: DashboardInsightProjection.Action? {
    switch scenario {
    case .empty: .addRoute
    case .failed, .degraded: .channelFailed
    default: nil
    }
  }

  var dashboardAttention: [PaxInsightItem] {
    guard scenario != .empty, scenario != .off else { return [] }
    return [
      .init(
        id: "subscription", symbol: "exclamationmark.triangle", title: "订阅 A 更新失败",
        detail: "08:00 自动更新 · 无法连接订阅地址", badge: "错误", badgeTone: .error),
      .init(
        id: "measure", symbol: "clock", title: "线路测量未完成", detail: "洛杉矶-备用 延迟测试超时", badge: "待处理",
        badgeTone: .warning),
    ]
  }

  var dashboardChanges: [PaxInsightItem] {
    guard scenario != .empty else { return [] }
    return [
      .init(
        id: "switch", time: "14:02", title: "切换到 洛杉矶-CF中转-防封-首选", detail: "成功", detailTone: .success
      ),
      .init(id: "proxy", time: "09:15", title: "开启代理"),
      .init(id: "refresh", time: "08:00", title: "订阅 B 自动更新", detail: "新增 3 条线路"),
    ]
  }
}

extension WorkbenchDashboardViewController {
  func applyDemoOverview(_ store: WorkbenchDemoStore) {
    routeCard.apply(store.dashboardRouteModel)
    trafficCard.apply(store.dashboardTrafficModel)
    let now = store.trafficCapturedAt
    if store.scenario == .empty {
      var trend = PaxModelTrendModel()
      trend.empty = "近 7 日暂无模型请求"
      trendCard.apply(trend)
      var rank = PaxModelRankModel()
      rank.empty = "今日暂无模型请求"
      rankCard.apply(rank)
    } else {
      let buckets = store.dashboardUsageBuckets
      trendCard.apply(
        DashboardUsageProjection.trend(
          buckets: buckets, state: nil, updatedAt: now.addingTimeInterval(-1800), now: now,
          filter: live.selectedUsageFilter))
      rankCard.apply(DashboardUsageProjection.rank(buckets: buckets, state: nil, now: now))
    }
    attentionCard.apply(
      PaxInsightModel(items: store.dashboardAttention, emptyTitle: "一切正常 · 今日暂无问题"))
    changesCard.apply(
      PaxInsightModel(items: store.dashboardChanges, emptyTitle: "今日暂无 Pax 操作记录"))
    applyAction(store.dashboardAction)
  }
}
