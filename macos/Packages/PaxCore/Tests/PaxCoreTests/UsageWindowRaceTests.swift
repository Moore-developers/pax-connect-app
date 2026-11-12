import Foundation
import PaxClient
import Testing

@testable import PaxCore

private actor UsageWindowGate {
  private var continuation: CheckedContinuation<RouteUsagePage, Error>?
  func wait() async throws -> RouteUsagePage {
    try await withCheckedThrowingContinuation { continuation = $0 }
  }
  var waiting: Bool { continuation != nil }
  func release(failure: Bool) {
    if failure {
      continuation?.resume(throwing: ClientError.unavailable)
    } else {
      continuation?.resume(returning: .init(items: []))
    }
    continuation = nil
  }
}

@MainActor struct UsageWindowRaceTests {
  @Test("旧范围迟到成功或失败不能改写新范围错误", arguments: [false, true])
  func staleRequest(failedOld: Bool) async {
    let gate = UsageWindowGate()
    let now = Date()
    let store = RoutesStore(
      preview: { _, _, _ in .init(token: "", revision: 0, items: []) }, cancel: { _ in },
      cancelOperation: { _ in }, editor: { _ in .init() }, command: { _ in "" },
      usage: { _, _, from in
        if let from, now.timeIntervalSince(from) < 86400 { return try await gate.wait() }
        throw ClientError.unavailable
      })
    store.consume(
      .ready(
        .init(
          daemonVersion: "fixture", protocolVersion: 23, snapshotSeq: 1,
          capabilities: ["routes.usage"]),
        .init(
          snapshotVersion: 1, status: .off, routes: [], currentRoute: nil, todayTraffic: .zero,
          settings: .init(autoConnect: false), recentLogs: [])))
    let old = Task { await store.setUsageWindow("fixture", from: now.addingTimeInterval(-21600)) }
    for _ in 0..<1000 {
      if await gate.waiting { break }
      await Task.yield()
    }
    #expect(await gate.waiting)
    await store.setUsageWindow("fixture", from: now.addingTimeInterval(-259200))
    let current = store.usageError("fixture")
    #expect(current != nil)
    await gate.release(failure: failedOld)
    await old.value
    #expect(store.usageError("fixture") == current)
  }
}
