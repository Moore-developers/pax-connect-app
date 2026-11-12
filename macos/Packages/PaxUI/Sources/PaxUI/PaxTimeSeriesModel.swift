import Foundation

/// The query window a chart covers, never the first/last sample. `end` is the real cutoff
/// (for "today" the capture time, not 24:00). `bucket` is set only for fixed-width buckets.
public struct PaxTimeDomain: Equatable, Sendable {
  public let start: Date
  public let end: Date
  public let bucket: TimeInterval?
  /// The query window's own start. `start` is later when recorded evidence began later (SCT-08).
  public let windowStart: Date
  /// Compact list charts only (SCT-10): `end` is the centre of the last observed bucket and
  /// that bucket is drawn exactly there, so the line ends on the plot's right edge.
  let endsAtLatestBucket: Bool
  public init(start: Date, end: Date, bucket: TimeInterval? = nil) {
    self.init(windowStart: start, coverageStart: nil, end: end, bucket: bucket)
  }
  /// Axis start is max(window start, coverage start); time before coverage is neither
  /// drawn nor counted as unobserved. A coverage start at or after `end` is ignored.
  public init(windowStart: Date, coverageStart: Date?, end: Date, bucket: TimeInterval? = nil) {
    let late = coverageStart.flatMap { $0 > windowStart && $0 < end ? $0 : nil }
    self.windowStart = windowStart
    start = late ?? windowStart
    self.end = max(start, end)
    self.bucket = bucket.flatMap { $0.isFinite && $0 > 0 ? $0 : nil }
    endsAtLatestBucket = false
  }
  /// Exact axis for a compact series: `start` is already max(window, coverage) and is kept
  /// even when it equals or follows `end` (a single early observation).
  init(
    windowStart: Date, start: Date, end: Date, bucket: TimeInterval?, endsAtLatestBucket: Bool
  ) {
    self.windowStart = windowStart
    self.start = start
    self.end = max(start, end)
    self.bucket = bucket.flatMap { $0.isFinite && $0 > 0 ? $0 : nil }
    self.endsAtLatestBucket = endsAtLatestBucket && self.bucket != nil
  }
  var span: TimeInterval { end.timeIntervalSince(start) }

  /// "自 MM-DD HH:mm 起有记录" when the axis starts later than the query window.
  public func coverageNote(calendar: Calendar = .current) -> String? {
    guard start > windowStart else { return nil }
    let parts = calendar.dateComponents([.month, .day, .hour, .minute], from: start)
    return String(
      format: "自 %02d-%02d %02d:%02d 起有记录", parts.month ?? 0, parts.day ?? 0, parts.hour ?? 0,
      parts.minute ?? 0)
  }
}

/// One observation. `value == nil` means "not collected"; 0 is a real zero.
/// `stacked` is a second series piled on top of `value` (download over upload).
public struct PaxTimeSeriesPoint: Equatable, Sendable {
  public let sampleID: String?
  public let at: Date
  public let value: Double?
  public let stacked: Double?
  public let partial: Bool
  public let note: String
  public init(
    at: Date, value: Double?, sampleID: String? = nil, stacked: Double? = nil,
    partial: Bool = false, note: String = ""
  ) {
    self.sampleID = sampleID
    self.at = at
    let clean = value.flatMap { $0.isFinite && $0 >= 0 ? $0 : nil }
    self.value = clean
    self.stacked = clean == nil ? nil : stacked.flatMap { $0.isFinite && $0 >= 0 ? $0 : nil }
    self.partial = partial
    self.note = note
  }
  var total: Double? { value.map { $0 + (stacked ?? 0) } }
}

/// A domain with its points: the single input a host hands to the chart.
public struct PaxTimeSeries: Equatable, Sendable {
  public enum Axis: Equatable, Sendable { case time, samples }
  public let axis: Axis
  public let domain: PaxTimeDomain
  public let points: [PaxTimeSeriesPoint]
  public init(domain: PaxTimeDomain, points: [PaxTimeSeriesPoint], axis: Axis = .time) {
    self.axis = axis
    self.domain = domain
    self.points = points
  }
}
