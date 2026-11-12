import Foundation
import PaxClient
import Synchronization
import Testing

@testable import PaxCore

@Suite("官方费用筛选输入")
struct AIUsageCostRangeTests {
  @Test("空白不限制，零与六位小数精确转换")
  func exactBounds() throws {
    let unbounded = try AIUsageFormatting.costBounds(minimum: "", maximum: " ")
    #expect(unbounded.minimum == nil && unbounded.maximum == nil)
    let range = try AIUsageFormatting.costBounds(minimum: "0", maximum: "0.123456")
    #expect(range.minimum == 0 && range.maximum == 123456)
    let maximum = try AIUsageFormatting.costBounds(minimum: ".5", maximum: "9223372036854.775807")
    #expect(maximum.minimum == 500000 && maximum.maximum == Int64.max)
    #expect(AIUsageFormatting.costBoundInput(123456) == "0.123456")
    #expect(AIUsageFormatting.costBoundInput(0) == "0")
    #expect(AIUsageFormatting.costBoundInput(nil) == "")
  }
  @Test("拒绝负数、非数值、过量精度、溢出和倒置区间")
  func invalidBounds() {
    for text in ["-1", "NaN", "inf", "1e3", ".", "1.2.3", "0.0000001", "9223372036854.775808"] {
      #expect(throws: AIUsageCostRangeError.self) {
        try AIUsageFormatting.costBounds(minimum: text, maximum: "")
      }
    }
    #expect(throws: AIUsageCostRangeError.self) {
      try AIUsageFormatting.costBounds(minimum: "2", maximum: "1")
    }
  }
}

@MainActor @Suite("费用筛选能力边界")
struct AIUsageCostCapabilityTests {
  @Test("旧后台不能静默忽略费用范围，支持时保留明确的零下限")
  func capabilityGatesReads() async throws {
    let captured = Synchronization.Mutex<AIUsageFilter?>(nil)
    let store = AIUsageDataStore(
      buckets: { _, _, _, _ in .init(buckets: [], state: .init(), notModified: false) },
      summary: { .init(buckets: [], state: .init(), notModified: false) },
      logs: { _, _, filter in
        captured.withLock { $0 = filter }
        return .init(requests: [], all: 0, success: 0, failed: 0, totals: .init())
      })
    var filter = AIUsageFilter()
    filter.minOfficialCostMicro = 0
    filter.maxOfficialCostMicro = 1
    let start = Date(timeIntervalSince1970: 1_792_843_200)
    store.select(
      range: start..<start.addingTimeInterval(86400), granularity: "hour", filter: filter)
    store.setActive(.logs)
    #expect(captured.withLock { $0 } == nil)
    #expect(store.error == "后台不支持费用筛选，请更新后台")
    var snapshot = DaemonSnapshot(
      snapshotVersion: 1, status: .off, routes: [], currentRoute: nil, todayTraffic: .zero,
      settings: DaemonSettings(autoConnect: false), recentLogs: [])
    snapshot.aiUsage = .init()
    store.consume(
      .ready(
        .init(
          daemonVersion: "test", protocolVersion: 40, snapshotSeq: 1,
          capabilities: ["ai.usage.cost_range.v1"]), snapshot))
    store.retry()
    for _ in 0..<100 where captured.withLock({ $0 == nil }) {
      try await Task.sleep(for: .milliseconds(2))
    }
    #expect(captured.withLock { $0?.minOfficialCostMicro } == 0)
    #expect(captured.withLock { $0?.maxOfficialCostMicro } == 1)
    store.setActive(nil)
  }
}
