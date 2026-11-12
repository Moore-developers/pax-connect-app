import Foundation
import Testing

@testable import PaxCore

@Suite("真实历史查询") @MainActor
struct TelemetryStoreTests {
  @Test("断线保留最后数据且旧请求不能覆盖新筛选")
  func queryGeneration() async {
    let store = TelemetryStore(
      logs: { q in
        if q.objectID == "slow" { try? await Task.sleep(for: .milliseconds(50)) }
        var page = HistoryLogPage()
        page.total = q.objectID == "slow" ? 1 : 2
        return page
      })
    store.setConnection(connected: true, capabilities: ["history.logs.v1"])
    var slow = LogFilter()
    slow.objectID = "slow"
    let old = Task { await store.loadLogs(slow) }
    await Task.yield()
    var fresh = LogFilter()
    fresh.objectID = "fresh"
    await store.loadLogs(fresh)
    await old.value
    #expect(store.logPage.total == 2)
    store.setConnection(connected: false, capabilities: [])
    #expect(store.logPage.total == 2)
    #expect(!store.connected)
  }
}
