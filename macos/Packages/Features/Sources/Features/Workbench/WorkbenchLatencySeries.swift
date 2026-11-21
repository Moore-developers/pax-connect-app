import Foundation
import PaxCore
import PaxUI

/// Recent actual observations; the query filters the source, not the chart's spacing.
func workbenchResponseSeries(
  _ responses: some Collection<TrafficResponsePoint>, from: Date, asOf: Date, limit: Int,
  dns: Bool = false, context: PresentationContext = .chinese
) -> PaxTimeSeries {
  let points = responses.suffix(limit).map {
    PaxTimeSeriesPoint(
      at: $0.startedAt, value: Double($0.milliseconds), sampleID: $0.connectionKey,
      note: dns
        ? context.text("traffic_series.6f5b222e4a") : context.text("traffic_series.e338cc5ec0"))
  }
  return PaxTimeSeries(domain: .init(start: from, end: asOf), points: points, axis: .samples)
}

func workbenchResponseContext(
  state: String, count: Int, dns: Bool, context: PresentationContext = .chinese
) -> String {
  let noun =
    dns ? context.text("traffic_series.d824870c1d") : context.text("traffic_series.1e5a9cdaa7")
  switch state {
  case "loading":
    return context.text("traffic_series.cd55966db0", ["arg0": String(describing: noun)])
  case "failed":
    return context.text("traffic_series.4620f3143c", ["arg0": String(describing: noun)])
  case "partial":
    return context.text(
      "traffic_series.1e25f84df0",
      ["arg0": String(describing: count), "arg1": String(describing: noun)])
  case "ready":
    return count == 0
      ? context.text("traffic_series.be5e37abf3", ["arg0": String(describing: noun)])
      : context.text(
        "traffic_series.9af9a941b7",
        ["arg0": String(describing: count), "arg1": String(describing: noun)])
  default: return context.text("traffic_series.39b4c35a48", ["arg0": String(describing: noun)])
  }
}
