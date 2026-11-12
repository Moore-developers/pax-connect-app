import Foundation
import Testing

@testable import PaxCore

@MainActor @Suite("结构化日志缓存预算")
struct TelemetryPayloadBudgetTests {
  @Test("系统详情正文计入缓存，越预算淘汰旧页而保留最新页")
  func nestedPayloadEviction() async throws {
    var event = SystemEvent()
    event.type = "fixture.payload"
    event.name = "系统事件"
    event.category.key = "fixture"
    var section = EventSection()
    section.kind = "facts"
    section.title = "内容"
    section.fields = (0..<15).map { index in
      var field = EventField()
      field.label = "字段\(index)"
      field.value = String(repeating: "v", count: 2048)
      return field
    }
    event.sections = [section]
    var record = LogRecord()
    record.systemEvent = event
    record.kind = "operation"
    var page = HistoryLogPage()
    page.records = (0..<100).map { index in
      var row = record
      row.recordID = String(index)
      return row
    }
    let payload = page
    let store = TelemetryStore(logs: { _ in payload })
    store.setConnection(connected: true, capabilities: ["history.logs.v1"])
    var q = LogFilter()
    for index in 0..<8 {
      q.cursor = String(index)
      _ = try await store.logs(q)
    }
    let before = store.queryCounts().requests
    _ = try await store.logs(q)
    #expect(store.queryCounts().requests == before)
    q.cursor = "0"
    _ = try await store.logs(q)
    #expect(store.queryCounts().requests == before + 1, "超过16MiB后最旧页仍在缓存")
  }
}
