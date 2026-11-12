import Foundation
import Observation
import PaxClient

/// Owns the date-window summary and one retained cache trajectory.
/// Watch supplies invalidation; PaxRefresh supplies scheduling.
@MainActor @Observable public final class AISessionStore {
  public typealias Query =
    @Sendable (Date, Date, [AISessionKey], Range<Date>?) async throws -> AISessionAnalysis
  public private(set) var current: AISessionAnalysis?
  public private(set) var detail: AISessionAnalysis?
  public private(set) var selected: [AISessionKey] = []
  public private(set) var loading = false
  public private(set) var error: String?
  public private(set) var supported = true
  public private(set) var limitsSupported = true
  public private(set) var contextSupported = true
  public private(set) var catalogSupported = true
  public let catalog: AISessionCatalogStore
  private let query: Query
  private var active = false
  private var connected = true
  private var range: Range<Date>?
  private var comparisonRange: Range<Date>?
  private var task: Task<Void, Never>?
  private var token = 0
  private var observed = AIUsageCollectionState()
  public init(query: @escaping Query, catalog: AISessionCatalogStore) {
    self.query = query
    self.catalog = catalog
  }
  public func select(range: Range<Date>, comparison: Range<Date>? = nil) {
    guard self.range != range || comparisonRange != comparison else { return }
    self.range = range
    comparisonRange = comparison
    catalog.select(range: range)
    current = nil
    detail = nil
    if active { refresh() }
  }
  public func choose(_ keys: [AISessionKey]) {
    guard keys != selected, keys.count <= 1 else { return }
    selected = keys
    catalog.select(keys.first)
    detail = nil
    if active { refresh() }
  }
  public func setActive(_ value: Bool, fetchNow: Bool = true) {
    guard active != value else { return }
    active = value
    catalog.setActive(value && connected && catalogSupported)
    if value && fetchNow { refresh() }
    if !value {
      token += 1
      task?.cancel()
      loading = false
    }
  }
  public func consume(_ update: ConnectionUpdate) {
    switch update {
    case .ready(let hello, let snapshot):
      connected = true
      limitsSupported =
        hello.capabilities.contains("ai.usage.limits.v1")
        && hello.capabilities.contains("ai.usage.limits.summary.v1")
      supported = hello.capabilities.contains("ai.usage.sessions.v1")
      contextSupported =
        hello.capabilities.contains("ai.usage.context.v1")
        && hello.capabilities.contains("ai.usage.session_agents.v1")
      catalogSupported =
        hello.capabilities.contains("ai.sessions.catalog.v1")
        && hello.capabilities.contains("ai.sessions.catalog.range.v1")
        && hello.capabilities.contains("ai.sessions.catalog.activity.v1")
      clear()
      observed = snapshot.aiUsage ?? .init()
      catalog.observe(
        revision: observed.catalogRevision, usageRevision: observed.revision,
        error: observed.catalogError, reset: true)
      catalog.setActive(active && catalogSupported)
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
      catalog.setActive(false)
      token += 1
      task?.cancel()
      loading = false
    }
  }
  private func accept(_ state: AIUsageCollectionState) {
    guard state.generation >= observed.generation, state.revision >= observed.revision else {
      return
    }
    if state.generation != observed.generation { clear() }
    observed = state
    catalog.observe(
      revision: state.catalogRevision, usageRevision: state.revision, error: state.catalogError)
  }
  private func clear() {
    token += 1
    task?.cancel()
    current = nil
    detail = nil
    loading = false
  }
  public func refresh() {
    guard active, connected, let range else { return }
    guard limitsSupported else {
      error = "后台不支持会话上限分析，请更新后台"
      return
    }
    guard supported else {
      error = "后台不支持会话分析，请更新后台"
      return
    }
    if catalogSupported { catalog.refresh() }
    guard catalogSupported else {
      error = "后台不支持原生会话目录，请更新后台"
      return
    }
    token += 1
    task?.cancel()
    let ticket = token
    loading = current == nil
    error = nil
    let selected = self.selected
    let comparison = comparisonRange
    task = Task { [weak self] in
      guard let self else { return }
      defer { if self.token == ticket { self.loading = false } }
      do {
        let now = try await self.query(range.lowerBound, range.upperBound, [], comparison)
        let keys = selected
        let full =
          keys.isEmpty
          ? nil : try await self.query(range.lowerBound, range.upperBound, keys, comparison)
        guard !Task.isCancelled, self.active, self.token == ticket else { return }
        // Watch invalidates for the next scheduled read; it may lead this consistent
        // snapshot. Only a generation change makes the returned data unsafe to retain.
        guard full.map({ $0.state.generation == now.state.generation }) ?? true,
          now.state.generation >= self.observed.generation
        else {
          throw ClientError.unavailable
        }
        self.current = now
        self.detail = full
        self.selected = keys
        self.error = now.state.errorCode.isEmpty ? nil : "本地用量采集不可用"
      } catch {
        guard self.token == ticket, !Task.isCancelled else { return }
        self.error = "会话分析读取失败，请重试"
      }
    }
  }
  public var rounds: [AISessionRound] {
    guard let primary = selected.first else { return [] }
    return AISessionRound.group(detail?.rows.filter { $0.key == primary } ?? [])
  }
}

/// One user turn can have several model/effort contributions. Unknown turns are excluded
/// from round curves and denominators, while their amounts remain in session summaries.
public struct AISessionRound: Equatable, Sendable {
  public let id: String
  public let rows: [AISessionRow]
  public var totals: AIUsageTotals { rows.reduce(into: AIUsageTotals()) { $0.add($1.totals) } }
  public var at: Date { rows.map(\.at).min() ?? .distantPast }
  public var complete: Bool { rows.allSatisfy(\.complete) }
  public var compressed: Bool { rows.contains(where: \.compressed) }
  public static func group(_ rows: [AISessionRow]) -> [Self] {
    Dictionary(grouping: rows.filter { !$0.turn.isEmpty }, by: \.turn).map {
      Self(id: $0.key, rows: $0.value)
    }
    .sorted { $0.at == $1.at ? $0.id < $1.id : $0.at < $1.at }
  }
}
