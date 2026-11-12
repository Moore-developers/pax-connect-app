import Foundation
import Synchronization
import Testing

@testable import PaxClient
@testable import PaxCore

@MainActor struct RoutesUsagePeriodTests {
  private func store(
    periodUsage: @escaping @Sendable (String, String) async throws -> RouteUsagePage
  )
    -> RoutesStore
  {
    let store = RoutesStore(
      preview: { _, _, _ in .init(token: "", revision: 0, items: []) }, cancel: { _ in },
      cancelOperation: { _ in }, editor: { _ in .init() }, command: { _ in "" },
      periodUsage: periodUsage)
    store.consume(
      .ready(
        .init(
          daemonVersion: "test", protocolVersion: 23, snapshotSeq: 1, capabilities: ["routes.usage"]
        ),
        .init(
          snapshotVersion: 1, status: .off, routes: [], currentRoute: nil, todayTraffic: .zero,
          settings: .init(autoConnect: false), recentLogs: [])))
    return store
  }

  @Test("周期模式加载更多不会把同一页追加两次")
  func periodMoreDoesNotRepeatPage() async {
    let calls = Mutex(0)
    let store = store { _, _ in
      calls.withLock { $0 += 1 }
      return .init(
        items: [.init(at: Date(timeIntervalSince1970: 1_000), upload: 1, download: 2)],
        nextCursor: "next")
    }
    await store.setUsagePeriod("fixture", period: "week")
    #expect(store.usage["fixture"]?.items.count == 1)
    await store.refreshRouteUsage("fixture", more: true)
    let starts = store.usage["fixture"]?.items.map(\.at) ?? []
    #expect(Set(starts).count == starts.count)
  }
  @Test("详情取消读取后迟到用量不能提交")
  func cancelledUsageDoesNotCommit() async throws {
    let continuation = Mutex<CheckedContinuation<Void, Never>?>(nil)
    let store = store { _, _ in
      await withCheckedContinuation { pending in continuation.withLock { $0 = pending } }
      return .init(items: [], todayUpload: 99, todayDownload: 100)
    }
    let read = Task { await store.setUsagePeriod("fixture", period: "week") }
    for _ in 0..<100 where continuation.withLock({ $0 == nil }) {
      try await Task.sleep(for: .milliseconds(2))
    }
    read.cancel()
    continuation.withLock {
      $0?.resume()
      $0 = nil
    }
    await read.value
    #expect(store.usage["fixture"] == nil)
  }

}
