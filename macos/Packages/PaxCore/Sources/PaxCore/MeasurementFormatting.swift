import Foundation

/// Decimal display units for route measurements; sorting continues to use raw values.
public enum MeasurementFormatting {
  public struct Reading: Equatable, Sendable {
    public let value: String
    public let unit: String
    public var text: String { value + " " + unit }
  }
  public enum LatencyGrade: Equatable, Sendable { case good, moderate, slow, unknown }

  public static func number(_ value: Double) -> String {
    guard value.isFinite else { return "—" }
    return value.formatted(.number.grouping(.never).precision(.fractionLength(0...2)))
  }
  public static func latency(_ milliseconds: Double?) -> Reading? {
    guard let milliseconds, milliseconds.isFinite, milliseconds >= 0 else { return nil }
    let seconds = (milliseconds * 100).rounded() >= 100_000
    return Reading(
      value: number(seconds ? milliseconds / 1000 : milliseconds), unit: seconds ? "s" : "ms")
  }
  public static func speed(bytesPerSecond: Double?) -> Reading? {
    guard let bytesPerSecond, bytesPerSecond.isFinite, bytesPerSecond >= 0 else { return nil }
    var value = bytesPerSecond
    let units = ["B/s", "KB/s", "MB/s", "GB/s"]
    var index = 0
    while (value * 100).rounded() >= 100_000 && index < units.count - 1 {
      value /= 1000
      index += 1
    }
    return Reading(value: number(value), unit: units[index])
  }
  public static func latencyGrade(_ milliseconds: Double?) -> LatencyGrade {
    guard let milliseconds, milliseconds.isFinite, milliseconds > 0 else { return .unknown }
    if milliseconds <= 200 { return .good }
    return milliseconds <= 500 ? .moderate : .slow
  }
}
