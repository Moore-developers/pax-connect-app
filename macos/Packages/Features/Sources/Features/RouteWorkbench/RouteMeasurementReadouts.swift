import Foundation
import PaxCore

extension RouteWorkbenchStore {
  func downloadCaption(_ id: String, context: PresentationContext = .chinese) -> String {
    guard let value = node(id)?.downloadSummary else { return "" }
    guard value.method == "window6s-v2" else { return context.text("route_readouts.2e45ea812b") }
    return
      context.text(
        "route_readouts.8ecdd92397",
        [
          "arg0": String(describing: value.parallel),
          "arg1": String(describing: TrafficFormatting.bytes(value.bytes)),
          "arg2": String(
            describing: MeasurementFormatting.latency(Double(value.durationMS))?.text ?? "—"),
          "arg3": String(
            describing: MeasurementFormatting.latency(Double(value.setupMS))?.text ?? "—"),
        ])
  }
  func measurementCaption(_ id: String, context: PresentationContext = .chinese) -> String {
    guard let summary = node(id)?.testSummary else {
      return context.text("route_readouts.2b90e75153")
    }
    guard summary.method == "https204-median-v3" else {
      return context.text("route_readouts.9ef6d58242")
    }
    let target = summary.testTarget == "gstatic" ? "Google gstatic" : "Google"
    let samples =
      summary.stableSamples == 3 && summary.stableError.isEmpty
      ? context.text("route_readouts.64007151aa")
      : context.text(
        "route_readouts.18e54c6704", ["arg0": String(describing: summary.stableSamples)])
    return context.text(
      "route_readouts.257a0b71f8",
      ["arg0": String(describing: target), "arg1": String(describing: samples)])
  }
  func subscriptionStatus(_ groupID: String, context: PresentationContext = .chinese) -> String {
    guard let source = subscription(groupID) else { return "" }
    if routes?.isRefreshing(source.id) == true { return context.text("route_readouts.1f3a59df6f") }
    var text =
      source.lastError.map { ProductErrorPresentation.message($0, context: context) }
      ?? context.text("route_readouts.26f42768ef")
    if let attempt = source.lastAttempt {
      let phase =
        [
          "dns": "DNS", "connect": context.text("route_readouts.a5574109f0"), "tls": "TLS",
          "headers": context.text("route_readouts.29e0f14357"),
          "body": context.text("route_readouts.1e33170032"),
          "response": context.text("route_readouts.e326629984"),
          "parse": context.text("route_readouts.fbff7b5046"),
          "complete": context.text("route_readouts.c0b3fbff51"),
        ][attempt.phase] ?? context.text("route_readouts.393e124155")
      text +=
        " · " + MeasurementTimestamp.text(attempt.at) + " · "
        + (attempt.channel == "route"
          ? context.text("route_readouts.bd170d0c23") : context.text("route_readouts.923d3879b7"))
        + " · " + phase
      if attempt.status > 0 { text += " HTTP " + String(attempt.status) }
      text += " · " + (MeasurementFormatting.latency(Double(attempt.totalMS))?.text ?? "—")
    }
    return text
  }

}
