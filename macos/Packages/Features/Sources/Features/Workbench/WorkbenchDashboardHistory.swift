import Foundation
import PaxCore

/// The five readers share visibility, never an interval or an in-flight barrier.
enum DashboardReadGroup: String, CaseIterable {
  case today, analysis, route, usage, activity
  var domains: Set<RefreshDomain> {
    switch self {
    case .today: [.trafficData]
    case .analysis: [.traffic, .logs]
    case .route: [.routeHistory]
    case .usage: [.aiUsage]
    case .activity: [.logs]
    }
  }
}

struct DashboardCalendarIdentity: Equatable {
  var day = Calendar.current.startOfDay(for: Date())
  var timeZone = Calendar.current.timeZone.identifier
}

extension WorkbenchDashboardViewController {
  var canReadDashboard: Bool {
    pageVisible && telemetry?.connected == true
      && telemetry?.isShown(.dashboard, focus: pageRefresh == nil) == true
      && (pageRefresh == nil
        || (pageRefresh?.windowVisible == true && pageRefresh?.connected == true))
  }
  func bindRefresh() {
    for group in DashboardReadGroup.allCases {
      pageRefresh?.bind(.dashboard, consumer: group.rawValue, domains: group.domains) {
        [weak self] in
        await self?.read(group)
      }
    }
  }
  func requestRead(_ group: DashboardReadGroup, restart: Bool = false) {
    if let pageRefresh {
      pageRefresh.request(.dashboard, consumer: group.rawValue, restart: restart)
    } else if canReadDashboard {
      if restart {
        fallbackTasks[group]?.cancel()
        fallbackTasks[group] = nil
      }
      guard fallbackTasks[group] == nil else { return }
      fallbackTasks[group] = Task { [weak self] in await self?.read(group) }
    }
  }
  func fetchTelemetry() {
    for group in DashboardReadGroup.allCases { requestRead(group) }
  }
  func reloadTelemetry() async {
    async let today: Void = read(.today)
    async let analysis: Void = read(.analysis)
    async let route: Void = read(.route)
    async let usage: Void = read(.usage)
    async let activity: Void = read(.activity)
    _ = await (today, analysis, route, usage, activity)
  }
  func syncTelemetry() {
    guard pageRefresh == nil, let telemetry else { return }
    guard canReadDashboard else {
      for task in fallbackTasks.values { task.cancel() }
      fallbackTasks.removeAll()
      telemetryGate = ""
      return
    }
    let gate = "\(telemetry.sessionGeneration):\(telemetry.capabilities.sorted())"
    guard gate != telemetryGate else { return }
    telemetryGate = gate
    fetchTelemetry()
  }
  func read(_ group: DashboardReadGroup) async {
    guard canReadDashboard, let telemetry else { return }
    reconcileCalendar()
    guard !Task.isCancelled else { return }
    let session = telemetry.sessionGeneration
    let date = calendarIdentity
    switch group {
    case .today:
      guard telemetry.trafficAvailable else { return }
      let facts = telemetry.trafficDataInvalidationGeneration
      var query = TrafficFilter()
      query.until = Date()
      query.window = "today"
      query.dimension = "network"
      do {
        let page = try await telemetry.traffic(query, force: true)
        guard accepts(session, date: date), facts == telemetry.trafficDataInvalidationGeneration
        else { return }
        todayPage = page
        live.trafficFailed = false
        live.trafficTrendPage = nil
        live.trafficTrendFailed = false
        // 小时窗口跨午夜时补取昨天；大数仍来自今日查询，不混入昨天的累计。
        let hour = Calendar.current.dateInterval(of: .hour, for: query.until ?? Date())?.start
        if let hour,
          hour.addingTimeInterval(-Double(DashboardTrafficProjection.hourCount - 1) * 3600)
            < date.day
        {
          // yesterday 与 today 均按午夜对齐；day 的滚动起点可能让5分钟桶跨整点偏移。
          query.window = "yesterday"
          do {
            let yesterday = try await telemetry.traffic(query, force: true)
            guard accepts(session, date: date), facts == telemetry.trafficDataInvalidationGeneration
            else { return }
            var trend = page
            trend.trend = yesterday.trend + page.trend
            live.trafficTrendPage = trend
          } catch {
            guard accepts(session, date: date), facts == telemetry.trafficDataInvalidationGeneration
            else { return }
            live.trafficTrendFailed = true
          }
        }
      } catch {
        guard accepts(session, date: date), facts == telemetry.trafficDataInvalidationGeneration
        else { return }
        live.trafficFailed = true
      }
      applyTraffic()
    case .analysis:
      await analysisStore.reloadLive(force: true, automatic: true)
      guard accepts(session, date: date) else { return }
    case .route: await reloadTrend()
    case .usage: await reloadUsage()
    case .activity:
      guard telemetry.available, telemetry.logViewsAvailable else { return }
      let invalidation = telemetry.invalidationGeneration
      async let problems: Void = reloadActivityView(
        "problems", session: session, date: date, invalidation: invalidation)
      async let operations: Void = reloadActivityView(
        "operations", session: session, date: date, invalidation: invalidation)
      await problems
      await operations
    }
  }
  func accepts(_ session: UInt64, date: DashboardCalendarIdentity? = nil) -> Bool {
    !Task.isCancelled && canReadDashboard && session == telemetry?.sessionGeneration
      && (date == nil || date == DashboardCalendarIdentity())
  }
  func reloadTrend(refresh: Bool = true) async {
    guard canReadDashboard, let sources, let routes = liveRoutes, let connection = liveConnection
    else { return }
    let running = connection.status == .protected || connection.status == .degraded
    guard let route = overviewRoute(routes: routes, connection: connection, running: running) else {
      return
    }
    let session = telemetry?.sessionGeneration ?? 0
    let items = try? await sources.autoLatency(route.id)
    let currentRunning = connection.status == .protected || connection.status == .degraded
    guard accepts(session),
      let current = overviewRoute(routes: routes, connection: connection, running: currentRunning),
      current.id == route.id, current.configRevision == route.configRevision
    else { return }
    if let items { live.trendHistory = items }
    live.trendRouteID = route.id
    live.trendConfigRevision = route.configRevision
    live.trendFailed = items == nil
    if refresh { applyRoute(routes: routes, connection: connection, running: currentRunning) }
  }
  func reloadUsage(refresh: Bool = true) async {
    guard canReadDashboard, let sources, let telemetry else { return }
    let generation = telemetry.aiUsage?.generation ?? 0
    let revision = telemetry.aiUsage?.revision ?? 0
    let session = telemetry.sessionGeneration
    let date = calendarIdentity
    let now = Date()
    let start = Calendar.current.date(byAdding: .day, value: -6, to: date.day) ?? now
    do {
      let page = try await sources.aiUsage(start, now.addingTimeInterval(3600))
      guard accepts(session, date: date), generation == (telemetry.aiUsage?.generation ?? 0),
        page.state.generation == generation,
        page.state.revision >= (telemetry.aiUsage?.revision ?? revision)
      else { return }
      live.usageBuckets = page.buckets
      live.usageState = telemetry.aiUsage ?? page.state
      live.usageUpdated = Date()
      live.usageLoaded = true
      live.usageFailed = false
    } catch {
      guard accepts(session, date: date), generation == (telemetry.aiUsage?.generation ?? 0) else {
        return
      }
      live.usageFailed = true
    }
    live.usageGeneration = generation
    live.usageSession = session
    if refresh { applyUsage() }
  }
  private func reloadActivityView(
    _ view: String, session: UInt64, date: DashboardCalendarIdentity, invalidation: UInt64
  ) async {
    guard let telemetry else { return }
    var query = LogFilter()
    query.view = view
    query.from = date.day
    query.until = Date()
    query.limit = 20
    do {
      let page = try await telemetry.logs(query, force: true)
      guard accepts(session, date: date), invalidation == telemetry.invalidationGeneration else {
        return
      }
      live.activityInvalidation = invalidation
      if view == "problems" {
        live.problems = page.items
        live.problemsLoaded = true
        live.problemsFailed = false
      } else {
        live.operations = page.items
        live.operationsLoaded = true
        live.operationsFailed = false
      }
    } catch {
      guard accepts(session, date: date), invalidation == telemetry.invalidationGeneration else {
        return
      }
      if view == "problems" { live.problemsFailed = true } else { live.operationsFailed = true }
    }
    applyActivity(problems: view == "problems", operations: view == "operations")
  }
  func presentAnalysis() {
    onPresentRegion?("analysis")
    let oldHeight = analysis.preferredHeight
    renderAnalysis()
    if oldHeight != analysis.preferredHeight { document.needsLayout = true }
    let wasHidden = noticeHost.isHidden
    notice.stringValue = analysisStore.liveError
    notice.isHidden = analysisStore.liveError.isEmpty
    noticeHost.isHidden = notice.isHidden
    if wasHidden != noticeHost.isHidden { document.needsLayout = true }
  }
}
