import Foundation
import Observation
import PaxClient

/// Coordinates product-history queries. Network and durable facts remain daemon-owned.
@MainActor @Observable
public final class TelemetryStore {
  @ObservationIgnored private let queries = TelemetryQueries()
  public private(set) var visiblePage: TelemetryPage? = nil
  public private(set) var windowVisible = false
  public private(set) var appActive = false
  public private(set) var trafficTabActive = true
  private let updatesQuery: @Sendable (LogUpdateRequest) async throws -> LogUpdatePage
  public func setPresentation(page: TelemetryPage?, windowVisible: Bool, appActive: Bool) {
    visiblePage = page
    self.windowVisible = windowVisible
    self.appActive = appActive
  }
  public func selectPage(_ page: TelemetryPage?) { visiblePage = page }
  public func setAppActive(_ active: Bool) { appActive = active }
  public func setWindowVisible(_ visible: Bool) { windowVisible = visible }
  public func setTrafficTabActive(_ active: Bool) { trafficTabActive = active }
  public func isPageVisible(_ page: TelemetryPage) -> Bool {
    visiblePage == page && windowVisible && appActive
  }
  public func isVisible(_ page: TelemetryPage) -> Bool {
    isPageVisible(page) && (page != .traffic || trafficTabActive)
  }
  public var refreshV2: Bool { capabilities.contains("history.refresh.v2") }
  public func queryCounts() -> (requests: Int, cancellations: Int) {
    (queries.requests, queries.cancellations)
  }
  public private(set) var connected = false
  public private(set) var sessionGeneration: UInt64 = 0
  public private(set) var invalidationGeneration: UInt64 = 0
  public private(set) var trafficInvalidationGeneration: UInt64 = 0
  /// Only traffic facts; log invalidation separately revokes read tokens and response evidence.
  public private(set) var trafficDataInvalidationGeneration: UInt64 = 0
  public private(set) var capabilities: Set<String> = []
  public private(set) var history = HistoryState()
  public private(set) var aiUsage: AIUsageCollectionState?
  public private(set) var recentLogs: [ProductLog] = []
  public private(set) var settings = DaemonSettings(autoConnect: false)
  public private(set) var logPage = HistoryLogPage()
  public private(set) var message = ""
  public private(set) var busy = false
  private var generation: UInt64 = 0
  private let logsQuery: @Sendable (LogFilter) async throws -> HistoryLogPage
  private let objectsQuery: @Sendable (LogFilter) async throws -> HistoryObjectPage
  private let recordDetailQuery: @Sendable (LogDetailFilter) async throws -> HistoryLogDetail
  private let detailQuery: @Sendable (String) async throws -> HistoryLogDetail
  private let trafficQuery: @Sendable (TrafficFilter) async throws -> TrafficPage
  private let trafficDetailQuery: @Sendable (TrafficDetailQuery) async throws -> TrafficDetail
  private let geoQuery: @Sendable (String, UInt32) async throws -> GeoRulesPage
  private let libraryQuery: @Sendable () async throws -> RoutingLibrarySnapshot
  private let libraryMutation: @Sendable (RoutingMutation) async throws -> RoutingLibrarySnapshot
  private let rulesQuery: @Sendable () async throws -> RoutingRulesSnapshot
  private let iconQuery: @Sendable (String, Bool, UInt64) async throws -> ObjectIconResult
  private let clearCommand: @Sendable (String, Date) async throws -> String
  private let retentionCommand: @Sendable (UInt32, UInt64, String, Date) async throws -> String

  public init(
    geoRules: @escaping @Sendable (String, UInt32) async throws -> GeoRulesPage = { _, _ in
      throw ClientError.unavailable
    },
    routingLibrary: @escaping @Sendable () async throws -> RoutingLibrarySnapshot = {
      throw ClientError.unavailable
    },
    mutateRoutingLibrary:
      @escaping @Sendable (RoutingMutation) async throws -> RoutingLibrarySnapshot = { _ in
        throw ClientError.unavailable
      },
    routingRules: @escaping @Sendable () async throws -> RoutingRulesSnapshot = {
      throw ClientError.unavailable
    },
    icon: @escaping @Sendable (String, Bool, UInt64) async throws -> ObjectIconResult = { _, _, _ in
      throw ClientError.unavailable
    },
    recordDetail: @escaping @Sendable (LogDetailFilter) async throws -> HistoryLogDetail = { _ in
      throw ClientError.unavailable
    },
    updates: @escaping @Sendable (LogUpdateRequest) async throws -> LogUpdatePage = { _ in
      throw ClientError.unavailable
    },
    logs: @escaping @Sendable (LogFilter) async throws -> HistoryLogPage = { _ in
      throw ClientError.unavailable
    },
    objects: @escaping @Sendable (LogFilter) async throws -> HistoryObjectPage = { _ in
      throw ClientError.unavailable
    },
    detail: @escaping @Sendable (String) async throws -> HistoryLogDetail = { _ in
      throw ClientError.unavailable
    },
    traffic: @escaping @Sendable (TrafficFilter) async throws -> TrafficPage = { _ in
      throw ClientError.unavailable
    },
    trafficDetail: @escaping @Sendable (TrafficDetailQuery) async throws -> TrafficDetail = { _ in
      throw ClientError.unavailable
    },
    clear: @escaping @Sendable (String, Date) async throws -> String = { _, _ in
      throw ClientError.unavailable
    },
    retention: @escaping @Sendable (UInt32, UInt64, String, Date) async throws -> String = {
      _, _, _, _ in throw ClientError.unavailable
    }
  ) {
    geoQuery = geoRules
    libraryQuery = routingLibrary
    libraryMutation = mutateRoutingLibrary
    rulesQuery = routingRules
    iconQuery = icon
    recordDetailQuery = recordDetail
    updatesQuery = updates
    logsQuery = logs
    objectsQuery = objects
    detailQuery = detail
    trafficQuery = traffic
    trafficDetailQuery = trafficDetail
    clearCommand = clear
    retentionCommand = retention
  }
  public func setConnection(connected: Bool, capabilities: Set<String>) {
    if self.connected != connected {
      queries.invalidate(logs: true, traffic: true)
      sessionGeneration &+= 1
      if connected { message = "" }
    }
    self.connected = connected
    self.capabilities = capabilities
    if !connected {
      generation &+= 1
      message = "与后台断开，保留最后记录"
    }
  }
  public func consume(_ update: ConnectionUpdate) {
    switch update {
    case .ready(let hello, let snapshot):
      setConnection(connected: true, capabilities: hello.capabilities)
      accept(snapshot)
    case .events(let events):
      for event in events {
        switch event {
        case .snapshot(let snapshot): accept(snapshot)
        case .historyDelta(let delta):
          acceptHistory(delta.history)
          if delta.replaceRecentLogs { recentLogs = delta.recentLogs }
        case .aiUsage(let state): aiUsage = state
        default: break
        }
      }
    case .connecting, .retrying, .failed, .disconnected:
      setConnection(connected: false, capabilities: capabilities)
    }
  }
  private func accept(_ snapshot: DaemonSnapshot) {
    acceptHistory(snapshot.history)
    if let state = snapshot.aiUsage { aiUsage = state }
    if settings != snapshot.settings { settings = snapshot.settings }
    if recentLogs != snapshot.recentLogs { recentLogs = snapshot.recentLogs }
  }
  private func acceptHistory(_ next: HistoryState) {
    let logsInvalid =
      refreshV2
      ? history.logDeletionGeneration != next.logDeletionGeneration
        || history.journalGeneration != next.journalGeneration
        || history.summaryGeneration != next.summaryGeneration
      : history.clearGeneration != next.clearGeneration
    let trafficDataInvalid =
      refreshV2
      ? history.trafficDeletionGeneration != next.trafficDeletionGeneration
      : logsInvalid
    let trafficInvalid = trafficDataInvalid || logsInvalid
    if logsInvalid {
      generation &+= 1
      invalidationGeneration &+= 1
      logPage = HistoryLogPage()
      recentLogs = []
    }
    if trafficDataInvalid { trafficDataInvalidationGeneration &+= 1 }
    if trafficInvalid { trafficInvalidationGeneration &+= 1 }
    if logsInvalid || trafficInvalid {
      let scopedRemoval =
        refreshV2 && next.logRemovalBeforeMs > 0
        && next.summaryGeneration == history.summaryGeneration
        && next.journalGeneration == history.journalGeneration
      queries.invalidate(
        logs: logsInvalid, traffic: trafficInvalid, trafficFacts: trafficDataInvalid,
        logsRemovedBefore: scopedRemoval
          ? Date(timeIntervalSince1970: Double(next.logRemovalBeforeMs) / 1000) : nil)
    }
    if history != next { history = next }
  }
  /// Per-domain refresh signals for `PaxRefresh`.
  public var signals: [RefreshDomain: RefreshSignal] {
    [
      .logs: .init(version: history.logsRevision, generation: invalidationGeneration),
      .traffic: .init(
        version: history.trafficRevision, generation: trafficInvalidationGeneration),
      .trafficData: .init(
        version: history.trafficRevision, generation: trafficDataInvalidationGeneration),
      .routeHistory: .init(version: history.routeHistoryRevision),
      .aiUsage: .init(version: aiUsage?.revision ?? 0, generation: aiUsage?.generation ?? 0),
      .aiSessionCatalog: .init(version: aiUsage?.catalogRevision ?? 0),
    ]
  }
  public func logsInvalidated(since previous: HistoryState, from: Date?) -> Bool {
    refreshV2
      ? history.invalidatesLogs(since: previous, from: from)
      : history.clearGeneration != previous.clearGeneration
  }
  public var available: Bool { connected && capabilities.contains("history.logs.v1") }
  public var trafficAvailable: Bool { connected && capabilities.contains("history.traffic.v1") }
  public var qualityMessage: String {
    if !connected { return "与后台断开，保留最后记录" }
    if !history.errorCode.isEmpty {
      return ProductErrorPresentation.message(.init(code: history.errorCode))
    }
    if history.capacityEvicted { return "容量已满，较早记录已淘汰；保存范围不足" }
    if history.collectionState == "idle" { return "当前未采集，正在查看本地历史" }
    return ""
  }
  public func loadLogs(_ q: LogFilter) async {
    generation &+= 1
    let token = generation
    do {
      let page = try await logs(q)
      guard token == generation else { return }
      logPage = page
      message = ""
    } catch { if token == generation { message = ProductErrorPresentation.display(error) } }
  }
  public func logs(_ q: LogFilter, force: Bool = false) async throws -> HistoryLogPage {
    guard available else { throw ClientError.unavailable }
    let load = logsQuery
    let v = try await queries.load(.logs(q), ttl: .infinity, force: force) {
      .logs(try await load(q))
    }
    guard case .logs(let page) = v else { throw ClientError.invalidEvent }
    return page
  }
  public func objects(_ q: LogFilter, force: Bool = false) async throws -> HistoryObjectPage {
    guard available else { throw ClientError.unavailable }
    let load = objectsQuery
    let v = try await queries.load(.objects(q), ttl: .infinity, force: force) {
      .objects(try await load(q))
    }
    guard case .objects(let page) = v else { throw ClientError.invalidEvent }
    return page
  }
  public func logUpdates(_ q: LogUpdateRequest) async throws -> LogUpdatePage {
    guard available, refreshV2 else { throw ClientError.unavailable }
    return try await updatesQuery(q)
  }
  public var logSummaryAvailable: Bool { capabilities.contains("history.summary.v1") }
  public var logViewsAvailable: Bool { capabilities.contains("history.views.v1") }
  public func recordDetail(_ q: LogDetailFilter) async throws -> HistoryLogDetail {
    guard available, logViewsAvailable else { throw ClientError.unavailable }
    let session = sessionGeneration
    let previousHistory = history
    let value = try await recordDetailQuery(q)
    try Task.checkCancellation()
    guard connected, session == sessionGeneration,
      !logsInvalidated(since: previousHistory, from: q.query.from)
    else {
      throw CancellationError()
    }
    return value
  }
  public func detail(_ id: String) async throws -> HistoryLogDetail {
    guard available else { throw ClientError.unavailable }
    let session = sessionGeneration
    let invalidation = invalidationGeneration
    let value = try await detailQuery(id)
    try Task.checkCancellation()
    guard connected, session == sessionGeneration, invalidation == invalidationGeneration else {
      throw CancellationError()
    }
    return value
  }
  public func traffic(_ q: TrafficFilter, force: Bool = false) async throws -> TrafficPage {
    guard trafficAvailable else { throw ClientError.unavailable }
    let load = trafficQuery
    let ttl: TimeInterval =
      ["day", "hour"].contains(q.window) ? .infinity : q.window == "today" ? 30 : 5
    let v = try await queries.load(.traffic(q), ttl: ttl, force: force) {
      .traffic(try await load(q))
    }
    guard case .traffic(let page) = v else { throw ClientError.invalidEvent }
    return page
  }
  public func trafficDetail(_ q: TrafficDetailQuery) async throws -> TrafficDetail {
    guard trafficAvailable else { throw ClientError.unavailable }
    let session = sessionGeneration
    let value = try await trafficDetailQuery(q)
    try Task.checkCancellation()
    // The daemon gives opened details their own immutable read token. Database/list
    // invalidation cannot revoke it; a different daemon session still rejects late replies.
    guard connected, session == sessionGeneration else { throw CancellationError() }
    return value
  }
  public var routingRulesAvailable: Bool {
    connected && capabilities.contains("routing.rules.read.v1")
  }
  public func routingRules() async throws -> RoutingRulesSnapshot {
    guard routingRulesAvailable else { throw ClientError.unavailable }
    let session = sessionGeneration
    let value = try await rulesQuery()
    try Task.checkCancellation()
    guard connected, session == sessionGeneration else { throw CancellationError() }
    return value
  }
  public var iconsAvailable: Bool { connected && capabilities.contains("objects.icons.v1") }
  public func objectIcon(host: String, fetch: Bool) async throws -> ObjectIconResult {
    guard iconsAvailable else { throw ClientError.unavailable }
    let session = sessionGeneration
    let invalidation = invalidationGeneration
    let value = try await iconQuery(host, fetch, history.logDeletionGeneration)
    try Task.checkCancellation()
    guard connected, session == sessionGeneration, invalidation == invalidationGeneration,
      value.logDeletionGeneration == history.logDeletionGeneration
    else { throw CancellationError() }
    return value
  }
  public func clearLogs() async -> Bool {
    guard available, !busy else { return false }
    busy = true
    defer { busy = false }
    do {
      _ = try await clearCommand(UUID().uuidString, Date())
      queries.invalidate(logs: true, traffic: true)
      trafficInvalidationGeneration &+= 1
      generation &+= 1
      invalidationGeneration &+= 1
      logPage = HistoryLogPage()
      recentLogs = []
      message = "日志已清除，新事件继续记录"
      return true
    } catch {
      message = ProductErrorPresentation.display(error)
      return false
    }
  }
  public func saveRetention(_ hours: UInt32) async -> Bool {
    guard available, !busy else { return false }
    busy = true
    defer { busy = false }
    do {
      _ = try await retentionCommand(hours, settings.revision, UUID().uuidString, Date())
      message = "日志保留时间已保存"
      return true
    } catch {
      message = ProductErrorPresentation.display(error)
      return false
    }
  }
}

extension TelemetryStore {
  public func geoRules(search: String, offset: UInt32) async throws -> GeoRulesPage {
    guard routingLibraryAvailable else { throw ClientError.unavailable }
    return try await geoQuery(search, offset)
  }
  public var routingLibraryAvailable: Bool {
    connected && capabilities.contains("routing.library.v1")
  }
  public func routingLibrary() async throws -> RoutingLibrarySnapshot {
    guard routingLibraryAvailable else { throw ClientError.unavailable }
    return try await libraryQuery()
  }
  public func mutateRoutingLibrary(_ command: RoutingMutation) async throws
    -> RoutingLibrarySnapshot
  {
    guard routingLibraryAvailable else { throw ClientError.unavailable }
    return try await libraryMutation(command)
  }
}
