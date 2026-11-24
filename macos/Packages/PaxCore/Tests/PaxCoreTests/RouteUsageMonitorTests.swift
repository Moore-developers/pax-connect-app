import Foundation
import Synchronization
import Testing

@testable import PaxClient
@testable import PaxCore

@MainActor struct RouteUsageMonitorTests {
  @Test("线路统计只更新数据，失败不冒充零，重连可恢复")
  func dataAndReconnect() async throws {
    let failed = Mutex(false)
    var page = RouteUsagePage(items: [])
    page.routes = [.init(routeID: "a", hourUpload: 100, hourDownload: 2_000_000_000)]
    let result = page
    let monitor = RouteUsageMonitor(query: { _, _ in
      if failed.withLock({ $0 }) { throw RoutesStore.DocumentSaveError.unavailable }
      return result
    })
    monitor.setConnected(true)
    defer { monitor.setConnected(false) }
    for _ in 0..<100 where monitor.overview == nil { try await Task.sleep(for: .milliseconds(2)) }
    #expect(monitor.summary("a")?.hourDownload == 2_000_000_000)
    failed.withLock { $0 = true }
    await monitor.refresh()
    #expect(monitor.error != nil)
    #expect(monitor.summary("a") == nil)
    failed.withLock { $0 = false }
    monitor.setConnected(false)
    monitor.setConnected(true)
    for _ in 0..<100 where monitor.error != nil { try await Task.sleep(for: .milliseconds(2)) }
    #expect(monitor.summary("a")?.hourUpload == 100)
  }
  @Test("断线期间迟到的批量响应不能提交")
  func staleRead() async throws {
    let pending = Mutex<CheckedContinuation<RouteUsagePage, Never>?>(nil)
    let monitor = RouteUsageMonitor(query: { _, _ in
      await withCheckedContinuation { continuation in pending.withLock { $0 = continuation } }
    })
    monitor.setConnected(true)
    for _ in 0..<100 where pending.withLock({ $0 == nil }) {
      try await Task.sleep(for: .milliseconds(2))
    }
    monitor.setConnected(false)
    pending.withLock {
      $0?.resume(returning: .init(items: []))
      $0 = nil
    }
    try await Task.sleep(for: .milliseconds(5))
    #expect(monitor.overview == nil)
  }
  @Test("今日hover排行沿用今日窗口")
  func breakdownMatchesToday() async throws {
    let requested = Mutex<[String]>([])
    let monitor = RouteUsageMonitor(query: { route, window in
      if !route.isEmpty { requested.withLock { $0.append(window) } }
      return .init(items: [])
    })
    monitor.setConnected(true)
    defer { monitor.setConnected(false) }
    _ = try await monitor.breakdown("a")
    #expect(requested.withLock { $0 } == ["today"])
  }
}
