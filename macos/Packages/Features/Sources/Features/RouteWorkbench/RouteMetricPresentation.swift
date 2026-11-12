import AppKit
import PaxCore
import PaxUI

enum RouteMetricPresentation {
  static func make(
    _ title: String, _ symbol: String, _ result: RouteWorkbenchStore.Measurement,
    kind: RouteWorkbenchStore.Test,
    color: NSColor
  ) -> NSView {
    let reading = result.reading(kind)
    return PaxRouteMetric(
      title: title, symbol: symbol,
      value: reading.map { $0.value + (result.historical ? "（历史）" : "") } ?? result.text,
      unit: reading?.unit ?? "",
      caption: result.caption, samples: result.displaySamples(kind),
      color: result.historical ? PaxTheme.Color.textSecondary : color,
      failed: result.failure != nil,
      bars: kind == .speed,
      valueColor: kind == .speed
        ? (result.historical ? PaxTheme.Color.textSecondary : PaxTheme.Routes.text)
        : result.latencyColor)
  }
}
