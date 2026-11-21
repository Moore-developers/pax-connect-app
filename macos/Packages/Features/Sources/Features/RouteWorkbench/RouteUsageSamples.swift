import Foundation
import PaxCore
import PaxUI

/// Route usage on the query window's time axis (SCT-03/04). Gap and coverage decide
/// "未采集"; partial buckets stay drawn without a readout note.
@MainActor func routeUsageSeries(_ usage: RouteUsagePage) -> PaxTimeSeries? {
  let buckets = usage.items.sorted { $0.at < $1.at }
  guard let first = buckets.first, let last = buckets.last else { return nil }
  let width = buckets.map { TimeInterval($0.seconds) }.max().flatMap { $0 > 0 ? $0 : nil } ?? 300
  let domain = PaxTimeDomain(
    windowStart: usage.from ?? first.at, coverageStart: usage.coverageStart,
    end: usage.until ?? last.at.addingTimeInterval(width), bucket: width)
  let points = buckets.map { bucket -> PaxTimeSeriesPoint in
    let seconds = bucket.seconds > 0 ? TimeInterval(bucket.seconds) : width
    let beforeCoverage = usage.coverageStart.map { bucket.at.addingTimeInterval(seconds) <= $0 }
    guard !bucket.gap, beforeCoverage != true else {
      return PaxTimeSeriesPoint(at: bucket.at, value: nil)
    }
    return PaxTimeSeriesPoint(
      at: bucket.at, value: Double(bucket.upload) / 1_000_000,
      stacked: Double(bucket.download) / 1_000_000, partial: bucket.partial)
  }
  return PaxTimeSeries(domain: domain, points: points)
}

/// Axis unit that names the bucket width, so per-bucket volume is never read as a rate.
func routeUsageUnit(_ domain: PaxTimeDomain, context: PresentationContext) -> String {
  guard let bucket = domain.bucket else { return "MB" }
  let minutes = Int((bucket / 60).rounded())
  if minutes >= 60 && minutes % 60 == 0 {
    return context.text("routes.usage_per_hours", ["hours": String(minutes / 60)])
  }
  return context.text("routes.usage_per_minutes", ["minutes": String(minutes)])
}

/// Fixture series for the isolated demo (no daemon): half-hourly buckets since 08:00.
@MainActor func demoRouteTransferSeries(now: Date = Date()) -> PaxTimeSeries {
  let start = Calendar.current.startOfDay(for: now).addingTimeInterval(8 * 3600)
  var points: [PaxTimeSeriesPoint] = []
  for index in 0..<12 {
    let at: Date = start.addingTimeInterval(TimeInterval(index) * 1800)
    let upload = Double(8 + index * 2)
    let download = Double(60 + (index % 5) * 35)
    points.append(PaxTimeSeriesPoint(at: at, value: upload, stacked: download))
  }
  return PaxTimeSeries(
    domain: PaxTimeDomain(start: start, end: start.addingTimeInterval(6 * 3600), bucket: 1800),
    points: points)
}

@MainActor extension RouteWorkbenchStore {
  func usageCoverageCaption(_ id: String, context: PresentationContext) -> String {
    guard let usage = routesUsage(id) else { return context.text("route_readouts.f948d939b6") }
    let copy = timeSeriesCopy(context)
    let late = routeUsageSeries(usage)?.domain.coverageNote(format: copy.coverage)
    let unknown = context.text("common.unknown")
    return copy.range + (usage.from.map(TrafficFormatting.dateMinute) ?? unknown) + " — "
      + (usage.until.map(TrafficFormatting.dateMinute) ?? unknown) + (late.map { " · " + $0 } ?? "")
  }
}
