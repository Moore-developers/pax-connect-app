import AppKit
import Observation
import PaxCore

private struct DashboardRouteInput: Equatable {
  var route: NodeProfile?
  var source: String
  var running: Bool
  var connected: Bool
  var session: UInt64
}
private struct DashboardTodayInput: Equatable {
  var snapshot: TrafficSnapshot?
  var available: Bool
  var connected: Bool
  var session: UInt64
  var generation: UInt64
}
private struct DashboardAnalysisInput: Equatable {
  var page: TrafficPage
  var loading: Bool
  var message: String
  var available: Bool
}

private struct DashboardLifecycleInput: Equatable {
  var connected: Bool
  var visible: Bool
  var dashboard: Bool
  var session: UInt64
}
private struct DashboardPowerInput: Equatable {
  var status: ProtectionStatus
  var enabled: Bool
  var message: String
}
private struct DashboardUsageInput: Equatable {
  var session: UInt64
  var generation: UInt64
  var error: String
}

@MainActor private final class DashboardObservedValue<Value> { var value: Value? }

extension WorkbenchDashboardViewController {
  /// Read only the region's inputs under Observation. Project/apply outside tracking and
  /// only when the effective input differs, including R8 state-only Watch frames.
  private func observeRegion<Value: Equatable>(
    previous: Value? = nil, read: @escaping @MainActor () -> Value,
    apply: @escaping @MainActor (Value?, Value) -> Void
  ) {
    let observed = DashboardObservedValue<Value>()
    let next = withObservationTracking {
      read()
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in
        self?.observeRegion(previous: observed.value, read: read, apply: apply)
      }
    }
    observed.value = next
    if previous != next { apply(previous, next) }
  }

  func observeLiveRegions() {
    guard let telemetry, let connection = liveConnection, let routes = liveRoutes else { return }
    observeRegion {
      DashboardLifecycleInput(
        connected: telemetry.connected, visible: telemetry.windowVisible,
        dashboard: telemetry.visiblePage == .dashboard, session: telemetry.sessionGeneration)
    } apply: { [weak self] _, next in
      guard let self else { return }
      if !next.dashboard { self.refreshOnNextEntry = true }
      self.reconcileCalendar()
      self.syncTelemetry()
    }
    observeRegion {
      DashboardPowerInput(
        status: connection.status, enabled: connection.canToggleProxy,
        message: connection.proxyMessage)
    } apply: { [weak self] _, _ in
      guard let self else { return }
      self.modeForConnection()
    }
    observeRegion { [weak self] in
      let running = connection.status == .protected || connection.status == .degraded
      let route = self?.overviewRoute(routes: routes, connection: connection, running: running)
      return DashboardRouteInput(
        route: route, source: route.map { routes.sourceName($0.sourceID) } ?? "",
        running: running, connected: connection.connection == .connected,
        session: telemetry.sessionGeneration)
    } apply: { [weak self] old, next in
      guard let self else { return }
      let changed =
        old?.route?.id != next.route?.id
        || old?.route?.configRevision != next.route?.configRevision
      if changed || old?.session != next.session {
        self.live.trendHistory = []
        self.live.trendFailed = false
        self.live.trendRouteID = ""
        if changed { self.requestRead(.route, restart: true) }
      }
      self.applyRoute(routes: routes, connection: connection, running: next.running)
    }
    observeRegion {
      DashboardTodayInput(
        snapshot: connection.todayTraffic, available: telemetry.trafficAvailable,
        connected: telemetry.connected, session: telemetry.sessionGeneration,
        generation: telemetry.trafficDataInvalidationGeneration)
    } apply: { [weak self] old, next in
      guard let self else { return }
      if old?.session != next.session || old?.generation != next.generation {
        self.live.trafficFailed = false
        self.live.trafficTrendPage = nil
        self.live.trafficTrendFailed = false
      }
      self.reconcileCalendar()
      self.applyTraffic()
      if next.available && old?.available == false && !self.todayLoaded {
        self.requestRead(.today, restart: true)
      }
    }
    observeRegion {
      let state = telemetry.aiUsage
      return DashboardUsageInput(
        session: telemetry.sessionGeneration, generation: state?.generation ?? 0,
        error: state?.errorCode ?? "")
    } apply: { [weak self] old, next in
      guard let self else { return }
      if let old, old.session != next.session || old.generation != next.generation {
        self.syncOverviewValidity()
        if self.pageRefresh == nil { self.requestRead(.usage, restart: true) }
      }
      self.live.usageState = telemetry.aiUsage
      self.applyUsage()
    }
    observeRegion { [weak self] in
      DashboardAnalysisInput(
        page: self?.analysisStore.livePage ?? TrafficPage(),
        loading: self?.analysisStore.liveLoading ?? false,
        message: self?.analysisStore.stateMessage ?? "", available: telemetry.trafficAvailable)
    } apply: { [weak self] old, next in
      self?.presentAnalysis()
      if old?.available == false && next.available { self?.requestRead(.analysis, restart: true) }
    }
    observeRegion {
      [
        telemetry.sessionGeneration, telemetry.invalidationGeneration,
        telemetry.available && telemetry.logViewsAvailable ? 1 : 0,
      ]
    } apply: { [weak self] old, next in
      guard let self else { return }
      if let old, old.prefix(2) != next.prefix(2) {
        self.syncOverviewValidity()
      }
      self.applyActivity()
      if old?.last == 0 && next.last == 1 { self.requestRead(.activity, restart: true) }
    }
    observeRegion {
      connection.feedback
    } apply: { [weak self] _, _ in
      self?.applyActivity(problems: true, operations: false)
    }
    observeRegion {
      DashboardInsightProjection.action(
        hasRoutes: routes.routes.contains { !$0.hidden },
        channelFailed: connection.currentPathSupported && connection.proxyEnabled
          && connection.currentPathCheck.state == "failed",
        needsAuthorization: connection.needsServiceAuthorization)
    } apply: { [weak self] _, next in
      self?.applyAction(next)
    }
  }

  func installCalendarNotifications() {
    for name in [
      Notification.Name.NSCalendarDayChanged, .NSSystemTimeZoneDidChange,
      .NSSystemClockDidChange,
    ] {
      calendarObservers.append(
        NotificationCenter.default.addObserver(
          forName: name, object: nil, queue: .main
        ) { [weak self] _ in Task { @MainActor [weak self] in self?.reconcileCalendar() } })
    }
    workspaceObserver = NSWorkspace.shared.notificationCenter.addObserver(
      forName: NSWorkspace.didWakeNotification, object: nil, queue: .main
    ) { [weak self] _ in Task { @MainActor [weak self] in self?.reconcileCalendar() } }
  }

  func reconcileCalendar() {
    let next = DashboardCalendarIdentity()
    guard next != calendarIdentity else { return }
    calendarIdentity = next
    todayLoaded = false
    live.trafficFailed = false
    live.trafficTrendPage = nil
    live.trafficTrendFailed = false
    analysisStore.stopLive()
    analysisStore.livePage = TrafficPage()
    live.usageBuckets = []
    live.usageLoaded = false
    live.usageFailed = false
    live.usageUpdated = nil
    live.problems = []
    live.operations = []
    live.activityInvalidation = nil
    live.problemsLoaded = false
    live.operationsLoaded = false
    live.problemsFailed = false
    live.operationsFailed = false
    applyTraffic()
    applyUsage()
    applyActivity()
    presentAnalysis()
    for group in [DashboardReadGroup.today, .analysis, .usage, .activity] {
      requestRead(group, restart: true)
    }
  }
}
