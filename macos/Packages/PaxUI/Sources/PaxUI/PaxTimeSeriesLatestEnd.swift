import Foundation

extension PaxTimeSeries {
  /// The traffic list's mini-chart (SCT-10): the axis ends at the latest observed point so the
  /// line stops exactly at the plot's right edge, next to the latest value. Buckets after it
  /// are dropped (neither drawn nor counted as unobserved); the start is unchanged. Detail
  /// charts do not call this and keep the query cutoff.
  ///
  /// Bucketed points sit at bucket centres, so the end is the last observed bucket's centre and
  /// the domain flags that this bucket is placed there unclipped. Raw series end at the point.
  public func endingAtLatestObservation() -> PaxTimeSeries {
    guard let last = points.filter({ $0.value != nil }).map(\.at).max() else { return self }
    let kept = points.filter { $0.at <= last }
    let end = domain.bucket.map { last.addingTimeInterval($0 / 2) } ?? last
    return PaxTimeSeries(
      domain: PaxTimeDomain(
        windowStart: domain.windowStart, start: domain.start, end: end, bucket: domain.bucket,
        endsAtLatestBucket: domain.bucket != nil),
      points: kept)
  }
}
