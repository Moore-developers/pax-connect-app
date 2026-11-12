import Foundation
import PaxClient
import Testing

@testable import PaxCore

@Suite("会话分析口径") struct AISessionTests {
  func row(
    _ cli: String = "codex", session: String = "s", turn: String = "t", complete: Bool = true,
    model: String = "m", priced: Int = 1, input: Int = 100, read: Int = 60, cost: Double = 0.02
  ) -> AISessionRow {
    var t = AIUsageTotals()
    t.count = 1
    t.success = 1
    t.input = input
    t.read = read
    t.output = 20
    t.costs = [cost, 0, 0, 0]
    t.cost = cost * 0.5
    t.billedCosts = [cost * 0.5, 0, 0, 0]
    return .init(
      key: .init(cli: cli, session: session), turn: turn, model: model,
      at: Date(timeIntervalSince1970: 1), end: Date(timeIntervalSince1970: 2), complete: complete,
      totals: t, maxInput: input, turns: complete ? 1 : 0, knownRequests: turn.isEmpty ? 0 : 1,
      pricedRequests: priced, completedTurnIDs: complete && !turn.isEmpty ? [turn] : [])
  }
  @Test func mixedModelsDeduplicateTurnsAndKeepContributions() {
    let m = AISessionMetrics([row(), row(model: "other", cost: 0.03)])
    #expect(m.completedTurns == 1)
    #expect(m.value("costPerTurn", channel: false) == 0.05)
    #expect(m.value("cost", channel: true) == 0.025)
    #expect(m.value("tokens", channel: false) == 240)
    #expect(m.value("cache", channel: false) == 0.6)
    #expect(AISessionRound.group(m.rows).count == 1)
  }
  @Test func unknownAndOpenRoundsCannotBecomeCheapCompletedRounds() {
    let open = row(turn: "open", complete: false, cost: 0.2)
    let unknown = row(turn: "", complete: false, cost: 0.3)
    let m = AISessionMetrics([row(), open, unknown])
    #expect(m.value("turns", channel: false) == nil)
    #expect(m.value("costPerTurn", channel: false) == 0.02)
    #expect(AISessionRound.group(m.rows).count == 2)
    #expect(AISessionMetrics([row(priced: 0)]).value("cost", channel: false) == nil)
    #expect(AISessionMetrics([row(priced: 0)]).value("tokens", channel: false) == 120)
  }
  @Test func cliIdentityAndWeightedCacheArePreserved() {
    let m = AISessionMetrics([row(), row("claude", input: 900, read: 900)])
    #expect(m.completedTurns == 2)
    #expect(m.value("cache", channel: false) == 0.96)
  }
}

/// Delivers Watch from inside the awaited RPC, before its consistent snapshot returns.
@MainActor private final class SessionRefreshRace {
  let clock = FakeRefreshClock()
  var response = AIUsageCollectionState()
  var afterRead: AIUsageCollectionState?
  var fails = false
  var beforeReturn: AIUsageCollectionState?
  var observed = AIUsageCollectionState()
  var calls = 0
  lazy var store = AISessionStore(
    query: { [weak self] _, _, _, _ in
      guard let self else { throw ClientError.unavailable }
      return try await self.read()
    }, catalog: AISessionCatalogStore(query: { _, _, _, _, _, _ in .init() }))
  lazy var refresh = PaxRefresh(
    clock: clock.clock,
    signals: { [weak self] in
      guard let self else { return [:] }
      return [
        .aiUsage: .init(version: observed.revision, generation: observed.generation),
        .aiSessionCatalog: .init(version: observed.catalogRevision),
      ]
    }, report: { _ in })
  func push(_ state: AIUsageCollectionState) {
    observed = state
    let update = ConnectionUpdate.events([.aiUsage(state)])
    store.consume(update)
    refresh.consume(update)
  }
  func start(single: Bool) {
    if observed == AIUsageCollectionState() { push(response) }
    store.select(range: Date(timeIntervalSince1970: 1000)..<Date(timeIntervalSince1970: 2000))
    if single { store.choose([.init(cli: "codex", session: "selected")]) }
    store.setActive(true, fetchNow: false)
    refresh.bind(.aiSessions) { [weak self] in self?.store.refresh() }
    refresh.consume(
      .ready(
        .init(daemonVersion: "test", protocolVersion: 1, snapshotSeq: 1),
        .init(
          snapshotVersion: 1, status: .off, routes: [], currentRoute: nil,
          todayTraffic: .zero, settings: .init(autoConnect: false), recentLogs: [])))
    refresh.pageDidAppear(.aiSessions)
  }
  private func read() throws -> AISessionAnalysis {
    calls += 1
    if fails { throw ClientError.unavailable }
    let snapshot = response
    if let state = afterRead {
      afterRead = nil
      response = state
    }
    if let state = beforeReturn {
      beforeReturn = nil
      push(state)
    }
    return .init(rows: [], state: snapshot)
  }
}

@MainActor @Suite("会话分析Watch与响应竞争") struct AISessionRefreshRaceTests {
  private func state(_ revision: UInt64, catalog: UInt64 = 100) -> AIUsageCollectionState {
    var state = AIUsageCollectionState()
    state.generation = 7
    state.revision = revision
    state.catalogRevision = catalog
    return state
  }
  private func settle(_ predicate: () -> Bool) async throws {
    for _ in 0..<200 where !predicate() { try await Task.sleep(for: .milliseconds(2)) }
    #expect(predicate())
  }
  @Test(
    arguments: [false, true],
    [(cached: false, catalogOnly: false), (true, false), (false, true), (true, true)])
  func sameGenerationSnapshotSurvivesWatchAndAutomaticallyCatchesUp(
    single: Bool, scenario: (cached: Bool, catalogOnly: Bool)
  ) async throws {
    let h = SessionRefreshRace()
    let response = state(18420)
    let next = state(
      scenario.catalogOnly ? 18420 : 18421, catalog: scenario.catalogOnly ? 101 : 100)
    if scenario.cached {
      h.response = state(18419)
      h.start(single: single)
      try await settle { h.store.current != nil && !h.store.loading }
      h.response = response
      h.beforeReturn = next
      h.push(response)
      h.clock.advance(3)
    } else {
      h.response = response
      h.beforeReturn = next
      h.start(single: single)
    }
    try await settle { h.store.current?.state.revision == 18420 || h.store.error != nil }
    #expect(h.store.error == nil)
    #expect(h.store.current?.state == response)
    #expect(h.store.detail?.state == (single ? response : nil))
    let calls = h.calls
    h.response = next
    h.clock.advance(3)
    try await settle { h.store.current?.state == next }
    #expect(h.store.error == nil)
    #expect(h.store.detail?.state == (single ? next : nil))
    #expect(h.calls == calls + (single ? 2 : 1))
    h.refresh.pageDidDisappear(.aiSessions)
    h.store.setActive(false)
  }

  @Test(arguments: [false, true])
  func generationMismatchStillRejectsTheRead(mixedScopes: Bool) async throws {
    let h = SessionRefreshRace()
    h.response = state(18420)
    var newer = state(18421)
    newer.generation = 8
    if mixedScopes { h.afterRead = newer } else { h.push(newer) }
    h.start(single: mixedScopes)
    try await settle { h.store.error != nil }
    #expect(h.store.current == nil && h.store.detail == nil)
    h.store.setActive(false)
    h.refresh.pageDidDisappear(.aiSessions)
  }

  @Test func realFailureKeepsCacheAndSuccessClearsError() async throws {
    let h = SessionRefreshRace()
    h.response = state(18420)
    h.start(single: true)
    try await settle { h.store.detail != nil }
    h.fails = true
    h.store.refresh()
    try await settle { h.store.error != nil }
    #expect(h.store.current?.state == h.response && h.store.detail?.state == h.response)
    h.fails = false
    h.response = state(18421)
    h.store.refresh()
    try await settle { h.store.detail?.state == h.response }
    #expect(h.store.error == nil)
    #expect(h.store.current?.state == h.response && h.store.detail?.state == h.response)
    h.store.setActive(false)
    h.refresh.pageDidDisappear(.aiSessions)
  }

  @Test(arguments: ["hidden", "disconnected", "date", "selection", "generation"])
  func cancelledResponseCannotRestoreOldData(change: String) async throws {
    let old = state(18420)
    var fresh = state(18421)
    if change == "generation" { fresh.generation = 8 }
    let query = SuspendedSessionRead(old: old, fresh: fresh)
    let store = AISessionStore(
      query: { _, _, _, _ in await query.read() },
      catalog: AISessionCatalogStore(query: { _, _, _, _, _, _ in .init() }))
    store.select(range: Date(timeIntervalSince1970: 1000)..<Date(timeIntervalSince1970: 2000))
    store.setActive(true)
    for _ in 0..<200 where !(await query.waiting) {
      try await Task.sleep(for: .milliseconds(2))
    }
    #expect(await query.waiting)
    switch change {
    case "hidden": store.setActive(false)
    case "disconnected": store.consume(.disconnected)
    case "date":
      store.select(range: Date(timeIntervalSince1970: 2000)..<Date(timeIntervalSince1970: 3000))
    case "selection": store.choose([.init(cli: "codex", session: "new")])
    default:
      store.consume(.events([.aiUsage(fresh)]))
      store.refresh()
    }
    let inactive = change == "hidden" || change == "disconnected"
    if !inactive { try await settle { store.current?.state == fresh && !store.loading } }
    // The transport deliberately ignores cancellation and returns after the new request.
    await query.release()
    try await Task.sleep(for: .milliseconds(10))
    #expect(store.current?.state == (inactive ? nil : fresh))
    #expect(store.error == nil && !store.loading)
    if change == "selection" {
      #expect(store.selected == [.init(cli: "codex", session: "new")])
      #expect(store.detail?.state == fresh)
    }
    store.setActive(false)
  }
}

private actor SuspendedSessionRead {
  let old: AIUsageCollectionState
  let fresh: AIUsageCollectionState
  var waiting: Bool { pending != nil }
  private var calls = 0
  private var pending: CheckedContinuation<AISessionAnalysis, Never>?
  init(old: AIUsageCollectionState, fresh: AIUsageCollectionState) {
    self.old = old
    self.fresh = fresh
  }
  func read() async -> AISessionAnalysis {
    calls += 1
    if calls == 1 { return await withCheckedContinuation { pending = $0 } }
    return .init(rows: [], state: fresh)
  }
  func release() {
    pending?.resume(returning: .init(rows: [], state: old))
    pending = nil
  }
}

private actor SessionCatalogRanges {
  var ranges: [Range<Date>] = []
  func read(_ from: Date, _ to: Date) -> AISessionCatalogPage {
    ranges.append(from..<to)
    return .init()
  }
  func latest() -> Range<Date>? { ranges.last }
}

private actor SessionQueryCalls {
  var count = 0
  var comparisons: [Range<Date>?] = []
  func read(_ keys: [AISessionKey], comparison: Range<Date>? = nil) -> AISessionAnalysis {
    count += 1
    comparisons.append(comparison)
    return .init(rows: [])
  }
  func total() -> Int { count }
  func comparisonRanges() -> [Range<Date>?] { comparisons }
}
@MainActor @Suite("会话分析读取生命周期") struct AISessionStoreTests {
  @Test func comparisonRangeChangeInvalidatesBothScopesAndPassesCalendarBounds() async throws {
    let calls = SessionQueryCalls()
    let store = AISessionStore(
      query: { _, _, keys, comparison in await calls.read(keys, comparison: comparison) },
      catalog: AISessionCatalogStore(query: { _, _, _, _, _, _ in .init() }))
    let range = Date(timeIntervalSince1970: 1000)..<Date(timeIntervalSince1970: 2000)
    let first = Date(timeIntervalSince1970: 100)..<Date(timeIntervalSince1970: 500)
    let second = Date(timeIntervalSince1970: 500)..<Date(timeIntervalSince1970: 1000)
    store.select(range: range, comparison: first)
    store.choose([.init(cli: "codex", session: "selected")])
    store.setActive(true)
    for _ in 0..<100 where store.detail == nil { try await Task.sleep(for: .milliseconds(5)) }
    #expect(await calls.comparisonRanges() == [first, first])
    store.select(range: range, comparison: second)
    #expect(store.current == nil && store.detail == nil)
    for _ in 0..<100 where store.detail == nil { try await Task.sleep(for: .milliseconds(5)) }
    #expect(await calls.comparisonRanges() == [first, first, second, second])
    store.select(range: range, comparison: second)
    #expect(store.current != nil && store.detail != nil)
    #expect(await calls.total() == 4)
    store.select(range: range)
    #expect(store.current == nil && store.detail == nil)
    for _ in 0..<100 where store.detail == nil { try await Task.sleep(for: .milliseconds(5)) }
    #expect(await calls.comparisonRanges() == [first, first, second, second, nil, nil])
    store.setActive(false)
  }

  @Test(arguments: [false, true])
  func oldDaemonCannotMasqueradeAsEmptyLimits(existingLimits: Bool) async throws {
    let calls = SessionQueryCalls()
    let store = AISessionStore(
      query: { _, _, keys, _ in await calls.read(keys) },
      catalog: AISessionCatalogStore(query: { _, _, _, _, _, _ in .init() }))
    store.consume(
      .ready(
        .init(
          daemonVersion: "old", protocolVersion: 45, snapshotSeq: 1,
          capabilities: Set(
            [
              "ai.usage.sessions.v1", "ai.sessions.catalog.v1",
              "ai.sessions.catalog.range.v1", "ai.sessions.catalog.activity.v1",
            ] + (existingLimits ? ["ai.usage.limits.v1"] : []))),
        .init(
          snapshotVersion: 1, status: .off, routes: [], currentRoute: nil,
          todayTraffic: .zero, settings: .init(autoConnect: false), recentLogs: [])))
    store.select(range: Date(timeIntervalSince1970: 1000)..<Date(timeIntervalSince1970: 2000))
    store.setActive(true)
    #expect(!store.limitsSupported)
    #expect(store.error == "后台不支持会话上限分析，请更新后台")
    #expect(store.current == nil && store.detail == nil)
    #expect(await calls.total() == 0)
    store.setActive(false)
  }

  @Test func hiddenPageDoesNotReadAndReactivationReadsAgain() async throws {
    let calls = SessionQueryCalls()
    let store = AISessionStore(
      query: { _, _, keys, _ in await calls.read(keys) },
      catalog: AISessionCatalogStore(query: { _, _, _, _, _, _ in .init() }))
    let start = Date(timeIntervalSince1970: 1000)
    let end = Date(timeIntervalSince1970: 2000)
    store.select(range: start..<end)
    store.refresh()
    #expect(await calls.total() == 0)
    store.setActive(true)
    for _ in 0..<100 where store.loading { try await Task.sleep(for: .milliseconds(5)) }
    #expect(await calls.total() == 1)
    store.setActive(false)
    store.refresh()
    #expect(await calls.total() == 1)
    store.setActive(true)
    for _ in 0..<100 where await calls.total() < 2 { try await Task.sleep(for: .milliseconds(5)) }
    #expect(await calls.total() == 2)
  }

  @Test func dateChangePreservesTheNativeSessionWithoutUsage() async throws {
    let first = AISessionTests().row(session: "first")
    let second = AISessionTests().row(session: "second")
    let start = Date(timeIntervalSince1970: 1000)
    let next = Date(timeIntervalSince1970: 2000)
    let ranges = SessionCatalogRanges()
    let catalog = AISessionCatalogStore(query: { from, to, _, _, _, _ in
      await ranges.read(from, to)
    })
    let store = AISessionStore(
      query: { from, _, _, _ in
        .init(rows: [from == start ? first : second])
      }, catalog: catalog)
    store.select(range: start..<next)
    store.choose([first.key])
    store.setActive(true)
    for _ in 0..<100 where store.loading { try await Task.sleep(for: .milliseconds(5)) }
    #expect(store.selected == [first.key])
    #expect(await ranges.latest() == start..<next)
    store.select(range: next..<Date(timeIntervalSince1970: 3000))
    for _ in 0..<100 where store.loading { try await Task.sleep(for: .milliseconds(5)) }
    #expect(store.selected == [first.key])
    #expect(store.detail != nil)
    #expect(store.current?.rows.first?.key == second.key)
    #expect(await ranges.latest() == next..<Date(timeIntervalSince1970: 3000))
    store.setActive(false)
  }

  @Test func allSessionsStayUnselectedAndSingleSelectionDrivesTheTrajectory() async throws {
    let first = AISessionTests().row(session: "first")
    let second = AISessionTests().row(session: "second")
    let store = AISessionStore(
      query: { _, _, keys, _ in
        keys.isEmpty
          ? .init(rows: [first, second])
          : .init(
            rows: [first, second].filter { keys.contains($0.key) },
            requests: [first, second].filter { keys.contains($0.key) })
      }, catalog: AISessionCatalogStore(query: { _, _, _, _, _, _ in .init() }))
    store.select(range: Date(timeIntervalSince1970: 1000)..<Date(timeIntervalSince1970: 2000))
    store.setActive(true)
    for _ in 0..<100 where store.loading { try await Task.sleep(for: .milliseconds(5)) }
    #expect(store.current?.rows.count == 2)
    #expect(store.selected.isEmpty && store.detail == nil)
    store.choose([second.key])
    for _ in 0..<100 where store.detail == nil { try await Task.sleep(for: .milliseconds(5)) }
    #expect(store.selected == [second.key])
    #expect(store.detail?.requests.map(\.key) == [second.key])
    store.choose([first.key])
    for _ in 0..<100 where store.detail == nil { try await Task.sleep(for: .milliseconds(5)) }
    #expect(store.selected == [first.key])
    #expect(store.detail?.requests.map(\.key) == [first.key])
    store.choose([])
    #expect(store.selected.isEmpty && store.detail == nil)
    store.setActive(false)
  }
}
