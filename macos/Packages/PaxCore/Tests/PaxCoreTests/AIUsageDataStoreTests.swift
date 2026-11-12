import Foundation
import PaxClient
import Testing

@testable import PaxCore

private actor UsageGate {
  var calls = 0
  var continuation: CheckedContinuation<Void, Never>?
  func bucket() async -> AIUsageBucketPage {
    calls += 1
    await withCheckedContinuation { continuation = $0 }
    var state = AIUsageCollectionState()
    state.revision = 99
    return AIUsageBucketPage(buckets: [], state: state, notModified: false)
  }
  func release() {
    continuation?.resume()
    continuation = nil
  }
}
@MainActor @Suite("AI采集可见性") struct AIUsageDataStoreTests {
  @Test("最近使用时间随页面读取，不受所选时段限制")
  func lastUsedFollowsThePage() async throws {
    let last = Date(timeIntervalSince1970: 1_790_000_000)
    let store = AIUsageDataStore(
      buckets: { _, _, _, _ in
        AIUsageBucketPage(
          buckets: [], state: .init(), notModified: false, lastUsed: [.claude: last])
      },
      summary: { AIUsageBucketPage(buckets: [], state: .init(), notModified: false) },
      logs: { _, _, _ in
        AIUsageLogPage(requests: [], all: 0, success: 0, failed: 0, totals: .init())
      })
    let start = Date(timeIntervalSince1970: 1_792_843_200)
    store.select(
      range: start..<start.addingTimeInterval(86400), granularity: "hour", filter: .init())
    store.setActive(.overview)
    for _ in 0..<100 where store.lastUsed.isEmpty { try await Task.sleep(for: .milliseconds(2)) }
    #expect(store.lastUsed == [.claude: last])
    store.setActive(nil)
  }
  @Test("隐藏撤销在途响应，不能提交迟到数据或继续查询")
  func hiddenPageRejectsLateResult() async throws {
    let gate = UsageGate()
    let store = AIUsageDataStore(
      buckets: { _, _, _, _ in await gate.bucket() },
      summary: {
        Issue.record("隐藏后的迟到响应不得继续查询汇总")
        return AIUsageBucketPage(buckets: [], state: .init(), notModified: false)
      },
      logs: { _, _, _ in
        Issue.record("隐藏后的迟到响应不得继续查询日志")
        return AIUsageLogPage(requests: [], all: 0, success: 0, failed: 0, totals: .init())
      })
    let start = Date(timeIntervalSince1970: 1_792_843_200)
    store.select(
      range: start..<start.addingTimeInterval(86400), granularity: "hour", filter: .init())
    store.setActive(.overview)
    for _ in 0..<100 {
      if await gate.calls > 0 { break }
      try await Task.sleep(for: .milliseconds(2))
    }
    #expect(await gate.calls == 1)
    store.setActive(nil)
    await gate.release()
    for _ in 0..<10 { await Task.yield() }
    #expect(store.state.revision == 0)
    #expect(!store.loading)
    #expect(await gate.calls == 1)
  }

  private func countingStore(_ counter: Counter) -> AIUsageDataStore {
    AIUsageDataStore(
      buckets: { _, _, _, _ in
        counter.bump()
        var state = AIUsageCollectionState()
        state.revision = counter.revision
        return AIUsageBucketPage(buckets: [], state: state, notModified: false)
      },
      summary: { AIUsageBucketPage(buckets: [], state: .init(), notModified: false) },
      logs: { _, _, _ in
        counter.bumpLogs()
        return AIUsageLogPage(requests: [], all: 0, success: 0, failed: 0, totals: .init())
      })
  }
  private func settle() async { for _ in 0..<30 { await Task.yield() } }
  @Test("没有轮询：进页读一次，空闲后不再查询；只有 fetch 才读")
  func noPolling() async {
    let counter = Counter()
    let store = countingStore(counter)
    store.setActive(.overview)
    await settle()
    #expect(counter.calls == 1)
    try? await Task.sleep(for: .milliseconds(50))
    await settle()
    #expect(counter.calls == 1)
    store.fetch(.overview)
    await settle()
    #expect(counter.calls == 2)
  }
  @Test("概览与日志各读各的：日志页只读日志，概览页只读用量，其他页的 fetch 被忽略")
  func partsReadOnlyTheirOwn() async {
    let counter = Counter()
    let store = countingStore(counter)
    store.setActive(.logs, fetchNow: false)
    store.fetch(.overview)
    await settle()
    #expect(counter.calls == 0 && counter.logCalls == 0)
    store.fetch(.logs)
    await settle()
    #expect(counter.calls == 0 && counter.logCalls == 1)
    store.setActive(.overview, fetchNow: false)
    store.fetch(.logs)
    store.fetch(.overview)
    await settle()
    #expect(counter.calls == 1 && counter.logCalls == 1)
    store.setActive(nil)
    store.fetch(.overview)
    await settle()
    #expect(counter.calls == 1)
  }
}

final class Counter: @unchecked Sendable {
  private let lock = NSLock()
  private var _calls = 0
  private var _revision: UInt64 = 1
  private var _generation: UInt64 = 0
  private var _logCalls = 0
  var calls: Int { lock.withLock { _calls } }
  var logCalls: Int { lock.withLock { _logCalls } }
  func bump() { lock.withLock { _calls += 1 } }
  func bumpLogs() { lock.withLock { _logCalls += 1 } }
  var revision: UInt64 {
    get { lock.withLock { _revision } }
    set { lock.withLock { _revision = newValue } }
  }
  var generation: UInt64 {
    get { lock.withLock { _generation } }
    set { lock.withLock { _generation = newValue } }
  }
}
