import Foundation
import PaxCore

/// Compact menu reading; the route page remains the detailed measurement view.
@MainActor
struct MenuBarLatencyPresentation {
  let text: String
  let detail: String

  init(route: NodeProfile, context: PresentationContext = .chinese) {
    guard let summary = route.testSummary else {
      text = context.text("detail_controls.092b520558")
      detail = context.text("detail_controls.cc66d1a9f7")
      return
    }
    let stale =
      summary.testedConfigRevision == 0
      ? summary.testedRevision != route.revision
      : summary.testedConfigRevision != route.configRevision
    let current = summary.method == "https204-median-v3"
    let reading: String
    if !summary.success {
      reading =
        summary.error.map(ProductErrorPresentation.message)
        ?? context.text("detail_controls.77c9e582e8")
    } else if current && (summary.stableSamples != 3 || !summary.stableError.isEmpty) {
      reading = context.text(
        "detail_controls.d59b5691ac", ["arg0": String(describing: summary.stableSamples)])
    } else {
      reading =
        MeasurementFormatting.latency(
          Double(current ? summary.stableMS : summary.durationMS))?.text
        ?? context.text("detail_controls.092b520558")
    }
    text = reading + (stale || !current ? context.text("detail_controls.c1eb2557f1") : "")
    detail =
      "\(reading)；\(MeasurementTimestamp.text(summary.at))；\(summary.method)"
      + (stale ? context.text("detail_controls.4ea42c524c") : "")
  }
}
