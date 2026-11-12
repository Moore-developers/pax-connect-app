import Foundation

/// Pure geometry for the shared time-series chart: no AppKit, no drawing.
/// Everything here is a function of (points, domain), so A/B/C place the same data identically.
struct PaxTimeSeriesLayout {
  enum Segment: Equatable {
    /// Two or more adjacent observed buckets joined by a solid line with area.
    case run([Int])
    /// An observed bucket with no adjacent neighbour: a filled dot.
    case dot(Int)
    /// Thin dashed link across one or more unobserved buckets: no area, no readout.
    case bridge(Int, Int)
  }

  /// `index` is nil for an unobserved ("未采集") bucket; `date` is the bucket start.
  struct Hit: Equatable {
    let date: Date
    let index: Int?
    var sampleID: String? = nil
  }

  let domain: PaxTimeDomain
  /// Cleaned points: sorted, de-duplicated, inside the domain. Raw series drop nil values.
  let points: [PaxTimeSeriesPoint]
  let axis: PaxTimeSeries.Axis
  let floor: Double
  let ceiling: Double
  let segments: [Segment]

  init(
    points: [PaxTimeSeriesPoint], domain: PaxTimeDomain, minimumCeiling: Double,
    axis: PaxTimeSeries.Axis = .time
  ) {
    self.axis = axis
    self.domain = domain
    let cleaned =
      axis == .samples
      ? points.filter { $0.value != nil && $0.sampleID != nil } : Self.clean(points, in: domain)
    self.points = cleaned
    let maximum = cleaned.compactMap(\.total).max() ?? 0
    let range = PaxChartPlot.range(cleaned.compactMap(\.total))
    floor = axis == .samples ? range.lowerBound : 0
    ceiling =
      axis == .samples ? range.upperBound : Self.niceCeiling(maximum, minimum: minimumCeiling)
    segments = Self.segments(cleaned, bucket: axis == .samples ? nil : domain.bucket)
  }

  // MARK: Scale

  /// Zero-based ceiling from 1/2/2.5/5 x 10^n, never below `minimum`.
  static func niceCeiling(_ maximum: Double, minimum: Double) -> Double {
    let floor = minimum.isFinite && minimum > 0 ? minimum : 1
    let target = maximum.isFinite ? max(maximum, floor) : floor
    let magnitude = pow(10, Foundation.floor(log10(target)))
    for multiplier in [1.0, 2, 2.5, 5, 10] {
      let candidate = multiplier * magnitude
      if candidate >= target * (1 - 1e-9) { return candidate }
    }
    return 10 * magnitude
  }

  func yFraction(_ value: Double) -> Double { min(1, max(0, (value - floor) / (ceiling - floor))) }

  var gridValues: [Double] {
    let count = max(2, PaxTheme.TimeSeries.gridLineCount)
    return (0..<count).map { floor + (ceiling - floor) * Double($0) / Double(count - 1) }
  }

  // MARK: Time mapping

  /// Bucket centre (clipped to the domain), or the sample time itself for raw series.
  func center(of point: PaxTimeSeriesPoint) -> Date {
    guard let bucket = domain.bucket else { return point.at }
    if domain.endsAtLatestBucket {
      let middle = point.at.addingTimeInterval(bucket / 2)
      return min(domain.end, max(domain.start, middle))
    }
    let low = max(point.at, domain.start)
    let high = min(point.at.addingTimeInterval(bucket), domain.end)
    let middle = (low.timeIntervalSinceReferenceDate + high.timeIntervalSinceReferenceDate) / 2
    return Date(timeIntervalSinceReferenceDate: middle)
  }

  func fraction(of date: Date) -> Double {
    guard domain.span > 0 else { return domain.endsAtLatestBucket ? 1 : 0.5 }
    return min(1, max(0, date.timeIntervalSince(domain.start) / domain.span))
  }

  func fraction(at index: Int) -> Double {
    if axis == .samples { return points.count > 1 ? Double(index) / Double(points.count - 1) : 0.5 }
    return fraction(of: center(of: points[index]))
  }

  func date(atFraction fraction: Double) -> Date {
    domain.start.addingTimeInterval(min(1, max(0, fraction)) * domain.span)
  }

  // MARK: Summary

  var knownIndices: [Int] { points.indices.filter { points[$0].value != nil } }
  var latest: PaxTimeSeriesPoint? { knownIndices.last.map { points[$0] } }
  var maximum: Double? { points.compactMap(\.total).max() }

  /// Unobserved buckets in the whole domain. Raw series have no bucket grid, so 0.
  var gapCount: Int {
    guard axis == .time, let bucket = domain.bucket, !points.isEmpty else { return 0 }
    let total = max(1, Int((domain.span / bucket - 1e-9).rounded(.up)))
    var covered = Set<Int>()
    for index in knownIndices {
      covered.insert(slot(of: points[index].at, bucket: bucket, total: total))
    }
    return total - covered.count
  }

  private func slot(of date: Date, bucket: TimeInterval, total: Int) -> Int {
    let raw = Int((date.timeIntervalSince(domain.start) / bucket + 1e-9).rounded(.down))
    return min(total - 1, max(0, raw))
  }

  // MARK: Hit testing

  func hit(atFraction fraction: Double) -> Hit? {
    guard !points.isEmpty else { return nil }
    guard axis == .time, domain.bucket != nil else {
      let known = knownIndices
      guard
        let nearest = known.min(by: {
          abs(self.fraction(at: $0) - fraction) < abs(self.fraction(at: $1) - fraction)
        })
      else { return nil }
      return Hit(date: points[nearest].at, index: nearest, sampleID: points[nearest].sampleID)
    }
    return bucketHit(at: date(atFraction: fraction))
  }

  /// Re-resolve a remembered bucket date after the data was refreshed.
  func resolve(_ date: Date, sampleID: String? = nil) -> Hit? {
    if axis == .samples {
      guard let sampleID, let index = points.firstIndex(where: { $0.sampleID == sampleID }) else {
        return nil
      }
      return Hit(date: points[index].at, index: index, sampleID: sampleID)
    }
    guard !points.isEmpty, date >= domain.start, date <= domain.end else { return nil }
    guard domain.bucket != nil else {
      return knownIndices.first { abs(points[$0].at.timeIntervalSince(date)) < 0.5 }
        .map { Hit(date: points[$0].at, index: $0) }
    }
    return bucketHit(at: date)
  }

  private func bucketHit(at time: Date) -> Hit? {
    guard let bucket = domain.bucket else { return nil }
    if let index = points.lastIndex(where: {
      $0.at <= time && time < $0.at.addingTimeInterval(bucket)
    }) {
      return Hit(date: points[index].at, index: points[index].value == nil ? nil : index)
    }
    let total = max(1, Int((domain.span / bucket - 1e-9).rounded(.up)))
    let start = domain.start.addingTimeInterval(
      Double(slot(of: time, bucket: bucket, total: total)) * bucket)
    return Hit(date: start, index: nil)
  }

  /// Keyboard stepping moves between observed buckets only and clamps at the ends.
  func neighbor(of hit: Hit?, step: Int) -> Hit? {
    let known = knownIndices
    guard let first = known.first, let last = known.last else { return nil }
    guard let hit else {
      let index = step < 0 ? last : first
      return Hit(date: points[index].at, index: index, sampleID: points[index].sampleID)
    }
    if axis == .samples, let index = hit.index {
      let target = min(last, max(first, index + step))
      return Hit(date: points[target].at, index: target, sampleID: points[target].sampleID)
    }
    let target =
      step < 0
      ? known.last { points[$0].at < hit.date } ?? first
      : known.first { points[$0].at > hit.date } ?? last
    return Hit(date: points[target].at, index: target)
  }

  // MARK: Construction helpers

  private static func clean(
    _ input: [PaxTimeSeriesPoint], in domain: PaxTimeDomain
  ) -> [PaxTimeSeriesPoint] {
    var byTime: [Date: PaxTimeSeriesPoint] = [:]
    for point in input { byTime[point.at] = point }
    return byTime.values.sorted { $0.at < $1.at }.filter { point in
      if let bucket = domain.bucket {
        let overlaps = point.at.addingTimeInterval(bucket) > domain.start
        return overlaps && (point.at < domain.end || (domain.span == 0 && point.at == domain.end))
      }
      return point.value != nil && point.at >= domain.start && point.at <= domain.end
    }
  }

  private static func segments(_ points: [PaxTimeSeriesPoint], bucket: TimeInterval?) -> [Segment] {
    var runs: [[Int]] = []
    for index in points.indices where points[index].value != nil {
      if let previous = runs.last?.last, adjacent(previous, index, points, bucket) {
        runs[runs.count - 1].append(index)
      } else {
        runs.append([index])
      }
    }
    var output: [Segment] = []
    for (number, run) in runs.enumerated() {
      if number > 0, let from = runs[number - 1].last, let to = run.first {
        output.append(.bridge(from, to))
      }
      output.append(run.count > 1 ? .run(run) : .dot(run[0]))
    }
    return output
  }

  /// Raw series connect by real time. Bucketed series connect only neighbouring buckets;
  /// a gap point or a missing bucket in between breaks the solid line.
  private static func adjacent(
    _ left: Int, _ right: Int, _ points: [PaxTimeSeriesPoint], _ bucket: TimeInterval?
  ) -> Bool {
    guard let bucket else { return true }
    return right == left + 1
      && points[right].at.timeIntervalSince(points[left].at) <= bucket * 1.5
  }
}
