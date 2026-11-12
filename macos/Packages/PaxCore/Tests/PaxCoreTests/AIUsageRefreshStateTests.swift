import Foundation
import PaxClient
import Synchronization
import Testing

@testable import PaxCore

@MainActor @Suite("AI刷新有效性与阅读", .serialized)
struct AIUsageRefreshStateTests {
  private func settle(_ predicate: () -> Bool) async throws {
    for _ in 0..<200 where !predicate() { try await Task.sleep(for: .milliseconds(2)) }
    #expect(predicate())
  }
  private func snapshot(_ state: AIUsageCollectionState?) -> DaemonSnapshot {
    var snapshot = DaemonSnapshot(
      snapshotVersion: 1, status: .off, routes: [], currentRoute: nil,
      todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: [])
    snapshot.aiUsage = state
    return snapshot
  }
  private func ready(_ state: AIUsageCollectionState?) -> ConnectionUpdate {
    .ready(
      DaemonHandshake(
        daemonVersion: "test", protocolVersion: 38, snapshotSeq: 1,
        capabilities: ["ai_usage.v1"]), snapshot(state))
  }
  @Test("普通读取失败保留有效桶，not_modified恢复清错；代数失效后失败不能恢复旧桶")
  func invalidationAndRecovery() async throws {
    let phase = Mutex(0)
    let store = AIUsageDataStore(
      buckets: { _, _, _, known in
        let phase = phase.withLock { $0 }
        if phase == 1 || phase == 3 { throw ClientError.unavailable }
        var totals = AIUsageTotals()
        totals.count = 1
        totals.cost = 12
        return AIUsageBucketPage(
          buckets: [.init(at: Date(), client: .claude, modelID: "m", totals: totals)],
          state: .init(), notModified: phase == 2 && known != nil)
      }, summary: { .init(buckets: [], state: .init(), notModified: false) },
      logs: { _, _, _ in .init(requests: [], all: 0, success: 0, failed: 0, totals: .init()) })
    store.consume(ready(.init()))
    store.setActive(.overview)
    try await settle { store.bucketsLoaded }
    phase.withLock { $0 = 1 }
    store.fetch(.overview)
    try await settle { store.error != nil }
    #expect(store.buckets.first?.totals.cost == 12)
    phase.withLock { $0 = 2 }
    store.fetch(.overview)
    try await settle { store.error == nil }
    #expect(store.buckets.first?.totals.cost == 12)
    phase.withLock { $0 = 3 }
    var state = AIUsageCollectionState()
    state.generation = 1
    state.revision = 2
    store.consume(.events([.aiUsage(state)]))
    #expect(store.buckets.isEmpty && store.lifetime.isEmpty && !store.bucketsLoaded)
    try await settle { store.error != nil }
    #expect(store.buckets.isEmpty)
    store.consume(ready(nil))
    #expect(!store.bucketsLoaded && store.buckets.isEmpty && store.error != nil)
    store.setActive(nil)
  }
  @Test("翻页立即读取整页，普通推送不混总量，代数失效绕过阅读暂停")
  func logReading() async throws {
    let reads = Mutex([Int]())
    let store = AIUsageDataStore(
      buckets: { _, _, _, _ in
        .init(buckets: [], state: .init(), notModified: false)
      }, summary: { .init(buckets: [], state: .init(), notModified: false) },
      logs: { _, _, filter in
        let count = reads.withLock { value in
          value.append(filter.page)
          return value.count
        }
        var totals = AIUsageTotals()
        totals.count = count
        return .init(requests: [], all: count, success: count, failed: 0, totals: totals)
      })
    store.consume(ready(.init()))
    store.setActive(.logs)
    try await settle { store.logs != nil }
    var filter = AIUsageFilter()
    filter.page = 1
    let from = Date(timeIntervalSince1970: 100)
    store.select(range: from..<from.addingTimeInterval(100), granularity: "hour", filter: filter)
    try await settle { reads.withLock { $0.count } == 2 && store.logs?.all == 2 }
    store.fetch(.logs)
    for _ in 0..<5 { await Task.yield() }
    #expect(reads.withLock { $0 } == [0, 1])
    #expect(store.logs?.all == 2)
    var state = AIUsageCollectionState()
    state.generation = 1
    state.revision = 3
    store.consume(.events([.aiUsage(state)]))
    #expect(store.logs == nil)
    try await settle { store.logs?.all == 3 }
    #expect(reads.withLock { $0 } == [0, 1, 1])
    store.setActive(nil)
  }
  @Test("RPC先于Watch到达：新代数接纳，迟到旧推送不能倒退或撤下新页")
  func rpcLeadsWatch() async throws {
    var next = AIUsageCollectionState()
    next.revision = 5
    next.generation = 2
    let value = next
    let store = AIUsageDataStore(
      buckets: { _, _, _, _ in
        .init(buckets: [], state: value, notModified: false)
      }, summary: { .init(buckets: [], state: value, notModified: false) },
      logs: { _, _, _ in
        .init(requests: [], all: 8, success: 8, failed: 0, totals: .init(), state: value)
      })
    store.consume(ready(.init()))
    store.setActive(.logs)
    try await settle { store.logs?.all == 8 }
    let invalidation = store.invalidationVersion
    var old = AIUsageCollectionState()
    old.revision = 4
    old.generation = 1
    store.consume(.events([.aiUsage(old)]))
    #expect(store.state.generation == 2 && store.state.revision == 5)
    #expect(store.logs?.all == 8 && store.invalidationVersion == invalidation)
    store.setActive(nil)
  }

  @Test("实际重算撤销旧费用与整页总量，试算保留阅读快照")
  func repriceInvalidates() async throws {
    let reads = Mutex(0)
    let store = AIUsageDataStore(
      buckets: { _, _, _, _ in .init(buckets: [], state: .init(), notModified: false) },
      summary: { .init(buckets: [], state: .init(), notModified: false) },
      logs: { _, _, _ in
        let count = reads.withLock {
          $0 += 1
          return $0
        }
        var totals = AIUsageTotals()
        totals.count = 2
        totals.cost = Double(count)
        return .init(requests: [], all: 2, success: 2, failed: 0, totals: totals)
      }, reprice: { _, _, _, dry in .init(requests: 2, oldCost: 1, newCost: 2, dryRun: dry) })
    store.setActive(.logs)
    try await settle { store.logs != nil }
    store.setLogsReading(true)
    let generation = store.invalidationVersion
    _ = try await store.reprice(profileID: "p", model: nil, range: .all, dryRun: true)
    #expect(store.invalidationVersion == generation && store.logs?.totals.cost == 1)
    _ = try await store.reprice(profileID: "p", model: nil, range: .all, dryRun: false)
    #expect(store.logs == nil && store.invalidationVersion > generation)
    try await settle { store.logs?.totals.cost == 2 }
    #expect(store.logs?.all == 2)
    store.setActive(nil)
  }

  @Test("比较期RPC跨清理代数时不能与当前桶一起提交")
  func comparisonGenerationMustMatch() async throws {
    let current = Date(timeIntervalSince1970: 1000)
    let store = AIUsageDataStore(
      buckets: { from, _, _, _ in
        var state = AIUsageCollectionState()
        state.generation = from < current ? 2 : 1
        state.revision = state.generation
        var totals = AIUsageTotals()
        totals.count = 1
        return .init(
          buckets: [.init(at: from, client: .claude, modelID: "m", totals: totals)], state: state,
          notModified: false)
      },
      summary: {
        var state = AIUsageCollectionState()
        state.generation = 1
        state.revision = 1
        return .init(buckets: [], state: state, notModified: false)
      }, logs: { _, _, _ in .init(requests: [], all: 0, success: 0, failed: 0, totals: .init()) })
    store.select(
      range: current..<current.addingTimeInterval(100), granularity: "hour", filter: .init(),
      previous: current.addingTimeInterval(-100)..<current)
    store.setActive(.overview)
    try await settle { store.error != nil }
    #expect(store.buckets.isEmpty && store.previous.isEmpty && !store.bucketsLoaded)
    store.setActive(nil)
  }

}
