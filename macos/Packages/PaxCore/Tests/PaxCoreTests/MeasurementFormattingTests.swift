import Foundation
import Testing

@testable import PaxCore

@Suite("测量统一单位")
struct MeasurementFormattingTests {
  @Test("延迟按1000切换单位，最多两位小数，临界舍入不显示1000ms")
  func latencyUnits() {
    #expect(MeasurementFormatting.latency(100)?.text == "100 ms")
    #expect(MeasurementFormatting.latency(123.456)?.text == "123.46 ms")
    #expect(MeasurementFormatting.latency(1000)?.text == "1 s")
    #expect(MeasurementFormatting.latency(1234)?.text == "1.23 s")
    #expect(MeasurementFormatting.latency(999.999)?.text == "1 s")
    #expect(MeasurementFormatting.latency(nil) == nil)
    #expect(MeasurementFormatting.latency(-1) == nil)
    #expect(MeasurementFormatting.latency(.infinity) == nil)
  }
  @Test("速度使用十进制字节率，不混成bit，最多两位小数")
  func speedUnits() {
    #expect(MeasurementFormatting.speed(bytesPerSecond: 450_125)?.text == "450.12 KB/s")
    #expect(MeasurementFormatting.speed(bytesPerSecond: 1_000_000)?.text == "1 MB/s")
    #expect(MeasurementFormatting.speed(bytesPerSecond: 12_345_678)?.text == "12.35 MB/s")
    #expect(MeasurementFormatting.speed(bytesPerSecond: 999_999)?.text == "1 MB/s")
    #expect(MeasurementFormatting.speed(bytesPerSecond: 300)?.text == "300 B/s")
    #expect(MeasurementFormatting.speed(bytesPerSecond: .nan) == nil)
  }
  @Test("延迟按原始值分级，空值和0不冒充绿色")
  func latencyGrades() {
    #expect(MeasurementFormatting.latencyGrade(100) == .good)
    #expect(MeasurementFormatting.latencyGrade(200) == .good)
    #expect(MeasurementFormatting.latencyGrade(200.01) == .moderate)
    #expect(MeasurementFormatting.latencyGrade(500) == .moderate)
    #expect(MeasurementFormatting.latencyGrade(500.01) == .slow)
    #expect(MeasurementFormatting.latencyGrade(0) == .unknown)
    #expect(MeasurementFormatting.latencyGrade(nil) == .unknown)
  }
}
