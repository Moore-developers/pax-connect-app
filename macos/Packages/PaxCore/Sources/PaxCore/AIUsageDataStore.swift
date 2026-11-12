import Foundation
import Observation
import PaxClient

/// Reads AI usage for the sub-page on screen only (driven by PaxRefresh, no polling); owns revision probes and rejects late responses after filter changes or hiding.
@MainActor @Observable public final class AIUsageDataStore {
  public private(set) var buckets: [AIUsageBucket] = []
  /// Buckets of the comparison period (see `select(previous:)`); empty means no data.
  public private(set) var previous: [AIUsageBucket] = []
  public private(set) var lifetime: [AIUsageBucket] = []
  /// Latest request per client over all retained history, not just the selected range.
  public private(set) var lastUsed: [AIClient: Date] = [:]
  public private(set) var logs: AIUsageLogPage?
  public private(set) var state = AIUsageCollectionState()
  public private(set) var error: String?
  public private(set) var loading = false
  public private(set) var bucketsLoaded = false
  public private(set) var invalidationVersion: UInt64 = 0
  public private(set) var logsReading = false
  public private(set) var costRangeSupported = false
  private var observedGeneration: UInt64?
  private var observedRevision: UInt64?
  private var connected = true
  private let queryBuckets:
    @Sendable (Date, Date, String, UInt64?) async throws -> AIUsageBucketPage
  private let querySummary: @Sendable () async throws -> AIUsageBucketPage
  private let queryLogs: @Sendable (Date, Date, AIUsageFilter) async throws -> AIUsageLogPage
  public let sessions: AISessionStore?
  private let runReprice: Reprice?
  public typealias Reprice =
    @Sendable (String, String?, AIRepriceRange, Bool) async throws -> AIRepriceResult
  /// The AI usage sub-page on screen; decides which read runs and which late responses are kept.
  public enum Part: Sendable { case overview, logs, profiles }
  private var bucketsTask: Task<Void, Never>?
  private var logsTask: Task<Void, Never>?
  private var bucketsGeneration: UInt64 = 0
  private var logsGeneration: UInt64 = 0
  private var active: Part?
  private var range: Range<Date> = Date()..<Date()
  private var previousRange: Range<Date>?
  private var granularity = "hour"
  private var filter = AIUsageFilter()
  private var revision: UInt64?
  public convenience init(connection: DaemonConnection) {
    self.init(
      buckets: {
        try await connection.aiUsageBuckets(from: $0, to: $1, granularity: $2, revision: $3)
      }, summary: { try await connection.aiUsageSummary() },
      logs: { try await connection.aiUsageLogs(from: $0, to: $1, filter: $2) },
      sessions: AISessionStore(
        query: {
          try await connection.aiSessionAnalysis(
            from: $0, to: $1, sessions: $2, limitsOnly: true, comparison: $3)
        },
        catalog: AISessionCatalogStore(query: {
          try await connection.aiSessionCatalog(
            from: $0, to: $1, cli: $2, query: $3, pageToken: $4, selected: $5)
        })),
      reprice: {
        try await connection.repriceAIUsage(profileID: $0, model: $1, range: $2, dryRun: $3)
      })
  }
  public init(
    buckets: @escaping @Sendable (Date, Date, String, UInt64?) async throws -> AIUsageBucketPage,
    summary: @escaping @Sendable () async throws -> AIUsageBucketPage,
    logs: @escaping @Sendable (Date, Date, AIUsageFilter) async throws -> AIUsageLogPage,
    sessions: AISessionStore? = nil, reprice: Reprice? = nil
  ) {
    self.sessions = sessions
    runReprice = reprice
    queryBuckets = buckets
    querySummary = summary
    queryLogs = logs
  }
  /// A changed range / filter is a user operation: the page on screen reads at once.
  public func select(
    range: Range<Date>, granularity: String, filter: AIUsageFilter,
    previous: Range<Date>? = nil
  ) {
    let bucketsMoved =
      self.range != range || self.granularity != granularity || previousRange != previous
    let logsMoved = bucketsMoved || self.filter != filter
    guard logsMoved else { return }
    self.range = range
    previousRange = previous
    self.granularity = granularity
    self.filter = filter
    logs = nil
    logsReading = filter.page > 0
    if bucketsMoved {
      buckets = []
      self.previous = []
      bucketsLoaded = false
    }
    revision = nil
    switch active {
    case .overview, .profiles: if bucketsMoved { fetch(active!) }
    case .logs: fetchLogs()
    case nil: break
    }
  }
  /// Sets the sub-page on screen (nil = none); in-flight reads of other pages are dropped.
  /// With `fetchNow` false the caller (PaxRefresh) triggers the entry read.
  public func setActive(_ part: Part?, fetchNow: Bool = true) {
    guard active != part else { return }
    active = part
    logsReading = part == .logs && filter.page > 0
    bucketsGeneration += 1
    logsGeneration += 1
    bucketsTask?.cancel()
    logsTask?.cancel()
    bucketsTask = nil
    logsTask = nil
    loading = false
    guard let part else { return }
    revision = nil
    if fetchNow {
      if part == .logs { fetchLogs() } else { fetch(part) }
    }
  }
  /// One read for the sub-page on screen; ignored for any other page.
  public func fetch(_ part: Part) {
    guard active == part, connected else { return }
    if part == .logs && logsReading { return }
    if part == .logs { fetchLogs() } else { fetchBuckets() }
  }
  public var canReprice: Bool { runReprice != nil }
  /// Recharges a profile's stored requests at its current multipliers; a real run reloads the
  /// page data afterwards. `dryRun` only computes the numbers.
  public func reprice(profileID: String, model: String?, range: AIRepriceRange, dryRun: Bool)
    async throws -> AIRepriceResult
  {
    guard let runReprice else { throw ProductError(code: "ai_usage_unavailable") }
    let result = try await runReprice(profileID, model, range, dryRun)
    if !dryRun {
      invalidateData()
      retry()
    }
    return result
  }
  public func retry() {
    guard connected else { return }
    revision = nil
    if active == .logs { fetchLogs() } else if let active { fetch(active) }
  }
  /// Reading a request freezes the complete page, including its totals.
  public func setLogsReading(_ value: Bool) { logsReading = value || filter.page > 0 }
  /// Watch invalidation must reach even an inactive subpage before any cached values are shown.
  public func consume(_ update: ConnectionUpdate) {
    sessions?.consume(update)
    switch update {
    case .ready(let handshake, let snapshot):
      costRangeSupported = handshake.capabilities.contains("ai.usage.cost_range.v1")
      connected = true
      invalidateData()
      observedGeneration = nil
      observedRevision = nil
      logsReading = false
      if let state = snapshot.aiUsage {
        accept(state)
      } else {
        invalidateData()
        error = "后台未提供本地用量状态"
      }
    case .events(let events):
      for event in events {
        switch event {
        case .aiUsage(let state): accept(state)
        case .snapshot(let snapshot): if let state = snapshot.aiUsage { accept(state) }
        default: break
        }
      }
    case .connecting, .retrying, .failed, .disconnected:
      connected = false
      costRangeSupported = false
      cancelReads()
    }
  }
  private func isCurrent(_ next: AIUsageCollectionState) -> Bool {
    next.generation >= (observedGeneration ?? 0) && next.revision >= (observedRevision ?? 0)
  }
  private func accept(_ next: AIUsageCollectionState) {
    guard isCurrent(next) else { return }
    let invalid = observedGeneration.map { $0 != next.generation } ?? false
    observedGeneration = next.generation
    observedRevision = next.revision
    state = next
    if invalid {
      invalidateData()
      // The ordinary reading pause cannot block removal of invalid content.
      if connected, let active {
        if active == .logs { fetchLogs() } else { fetchBuckets() }
      }
    }
  }
  private func cancelReads() {
    bucketsGeneration += 1
    logsGeneration += 1
    bucketsTask?.cancel()
    logsTask?.cancel()
    loading = false
  }
  /// A response may lead Watch. Adopt its watermark without cancelling this current read;
  /// invalidate every other value before its replacement is committed.
  private func acceptResponse(_ next: AIUsageCollectionState) -> Bool {
    guard isCurrent(next) else { return false }
    let invalid = observedGeneration.map { $0 != next.generation } ?? false
    observedGeneration = next.generation
    observedRevision = next.revision
    state = next
    if invalid {
      invalidationVersion &+= 1
      clearValues()
      revision = nil
    }
    return true
  }
  private func invalidateData() {
    invalidationVersion &+= 1
    cancelReads()
    clearValues()
    revision = nil
    error = nil
  }
  private func clearValues() {
    buckets = []
    previous = []
    lifetime = []
    lastUsed = [:]
    logs = nil
    bucketsLoaded = false
  }
  private var showsBuckets: Bool { active == .overview || active == .profiles }
  private func fetchBuckets() {
    bucketsGeneration += 1
    bucketsTask?.cancel()
    let token = bucketsGeneration
    bucketsTask = Task { [weak self] in
      guard let self, self.showsBuckets, token == self.bucketsGeneration else { return }
      await self.refreshBuckets(token)
    }
  }
  private func fetchLogs() {
    if !costRangeSupported, filter.minOfficialCostMicro != nil || filter.maxOfficialCostMicro != nil
    {
      logsTask?.cancel()
      logsGeneration += 1
      loading = false
      error = "后台不支持费用筛选，请更新后台"
      return
    }
    logsGeneration += 1
    logsTask?.cancel()
    let token = logsGeneration
    logsTask = Task { [weak self] in
      guard let self, self.active == .logs, token == self.logsGeneration else { return }
      await self.refreshLogs(token)
    }
  }
  private func refreshBuckets(_ token: UInt64) async {
    loading = !bucketsLoaded
    defer { if token == bucketsGeneration { loading = false } }
    do {
      let page = try await queryBuckets(range.lowerBound, range.upperBound, granularity, revision)
      guard showsBuckets, token == bucketsGeneration, !Task.isCancelled else { return }
      guard acceptResponse(page.state) else { throw ClientError.unavailable }
      guard !page.notModified else {
        error = nil
        return
      }
      let summary = try await querySummary()
      guard showsBuckets, token == bucketsGeneration, !Task.isCancelled else { return }
      guard summary.state.generation == page.state.generation else { throw ClientError.unavailable }
      var before: [AIUsageBucket] = []
      if let previousRange {
        // A failed optional comparison can be absent. A different generation cannot
        // be combined with this page, even before Watch catches up.
        if let comparison = try? await queryBuckets(
          previousRange.lowerBound, previousRange.upperBound, "day", nil)
        {
          guard comparison.state.generation == page.state.generation else {
            throw ClientError.unavailable
          }
          before = comparison.buckets
        }
        guard showsBuckets, token == bucketsGeneration, !Task.isCancelled else { return }
      }
      previous = before
      buckets = page.buckets
      bucketsLoaded = true
      lifetime = summary.buckets
      lastUsed = page.lastUsed
      revision = page.state.revision
      error = nil
    } catch {
      guard showsBuckets, token == bucketsGeneration, !Task.isCancelled else { return }
      self.error = "本地用量读取失败，请重试"
    }
  }
  private func refreshLogs(_ token: UInt64) async {
    loading = logs == nil
    defer { if token == logsGeneration { loading = false } }
    do {
      let rows = try await queryLogs(range.lowerBound, range.upperBound, filter)
      guard active == .logs, token == logsGeneration, !Task.isCancelled else { return }
      if let state = rows.state, !acceptResponse(state) { throw ClientError.unavailable }
      logs = rows
      error = nil
    } catch {
      guard active == .logs, token == logsGeneration, !Task.isCancelled else { return }
      self.error = "本地用量读取失败，请重试"
    }
  }
}
