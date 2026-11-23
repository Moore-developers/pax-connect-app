import AppKit
import PaxCore
import PaxUI

extension WorkbenchTrafficDetail {
  /// The query window the detail inherited: real start and the capture cutoff.
  var latencyDomain: PaxTimeDomain {
    PaxTimeDomain(
      windowStart: lowerBound, coverageStart: aggregate?.history.aggregateCoverageStart,
      end: capturedAt)
  }

  /// Latest valid observations, in sample order; the query window only scopes the source.
  var latencySeries: PaxTimeSeries {
    if let aggregate {
      return workbenchResponseSeries(
        aggregate.recentResponses, from: lowerBound, asOf: capturedAt,
        limit: 60, dns: aggregate.summary.kind == "dns", context: context)
    }
    let points = successful.sorted { $0.started < $1.started }.suffix(60).map {
      PaxTimeSeriesPoint(
        at: $0.started, value: $0.establishmentMS, sampleID: $0.id,
        note: context.text("traffic_series.e338cc5ec0"))
    }
    return PaxTimeSeries(domain: latencyDomain, points: points, axis: .samples)
  }

  private var responseContext: String {
    guard let aggregate else { return context.text("traffic_series.569af1a47d") }
    return workbenchResponseContext(
      state: aggregate.recentResponsesState,
      count: min(60, aggregate.recentResponses.count), dns: aggregate.summary.kind == "dns",
      context: context)
  }

  /// Header detail: inherited range title plus its real start-end, e.g. "今日 00:00–14:32".
  /// When records began after the window start, the header says so.
  var latencyRangeText: String {
    let domain = latencyDomain
    return periodTitle + " " + domain.rangeText()
      + (domain.coverageNote(format: timeSeriesCopy(context).coverage).map { " · " + $0 } ?? "")
  }

  /// Frozen recent observations from the same read token as the opened detail.
  func latencyView(tooltip: PaxChartTooltipCoordinator) -> NSView {
    let series = latencySeries
    let view = PaxTimeLatencyView(
      series: series,
      compact: false, tooltip: tooltip, context: responseContext,
      title: aggregate?.summary.kind == "dns"
        ? context.text("traffic_series.d824870c1d") : context.text("traffic_series.abe05d4485"),
      pinKey: "traffic-detail-" + id + "-" + (aggregate?.dnsGroupID ?? "") + "-" + periodTitle
        + "-" + String(lowerBound.timeIntervalSince1970),
      copy: timeSeriesCopy(context))
    view.heightAnchor.constraint(equalToConstant: view.intrinsicContentSize.height).isActive = true
    return view
  }
}
