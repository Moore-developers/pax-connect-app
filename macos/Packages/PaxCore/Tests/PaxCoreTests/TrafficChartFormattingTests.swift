import Foundation
import Testing

@testable import PaxCore

@Suite("图表流量读数") struct TrafficChartFormattingTests {
  @Test("固定日期与秒补零，跨日不丢失年月日")
  func connectionTime() throws {
    let calendar = Calendar(identifier: .gregorian)
    let date = try #require(
      calendar.date(
        from:
          DateComponents(year: 2026, month: 10, day: 20, hour: 23, minute: 59, second: 59)))
    #expect(TrafficFormatting.dateAndClock(date).date == "2026-10-20")
    #expect(TrafficFormatting.dateAndClock(date).clock == "23:59:59")
    #expect(TrafficFormatting.dateAndClock(date.addingTimeInterval(2)).date == "2026-10-21")
    #expect(TrafficFormatting.dateAndClock(date.addingTimeInterval(2)).clock == "00:00:01")
  }
  @Test("大小流量分别选单位，不能把1KB上传显示成0MB")
  func differentMagnitudes() {
    #expect(TrafficFormatting.megabytes(0.001) == "1 KB")
    #expect(TrafficFormatting.megabytes(20) == "20 MB")
    #expect(TrafficFormatting.megabytes(0.000001) == "1 B")
    #expect(TrafficFormatting.megabytes(0) == "0 B")
    #expect(TrafficFormatting.megabytes(.nan) == "—")
  }
}
