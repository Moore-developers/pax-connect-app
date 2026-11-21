import AppKit
import PaxCore
import PaxUI

enum RouteMetricPresentation {
  static func make(
    _ title: String, _ symbol: String, _ result: RouteWorkbenchStore.Measurement,
    kind: RouteWorkbenchStore.Test,
    color: NSColor, context: PresentationContext
  ) -> NSView {
    let reading = result.reading(kind)
    let suffix = result.historical ? context.text("model_copy.c1eb2557f1") : ""
    return PaxRouteMetric(
      title: title, symbol: symbol,
      value: reading.map { $0.value + suffix } ?? result.text(context: context),
      unit: reading?.unit ?? "",
      caption: result.caption(context: context), samples: result.displaySamples(kind),
      color: result.historical ? PaxTheme.Color.textSecondary : color,
      failed: result.failure != nil,
      bars: kind == .speed,
      valueColor: kind == .speed
        ? (result.historical ? PaxTheme.Color.textSecondary : PaxTheme.Routes.text)
        : result.latencyColor)
  }
}
