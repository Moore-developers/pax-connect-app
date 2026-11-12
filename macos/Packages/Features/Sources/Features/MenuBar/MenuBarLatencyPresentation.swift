import Foundation
import PaxCore

/// Compact menu reading; the route page remains the detailed measurement view.
@MainActor
struct MenuBarLatencyPresentation {
  let text: String
  let detail: String

  init(route: NodeProfile) {
    guard let summary = route.testSummary else {
      text = "未测试"
      detail = "尚无已完成的线路测试"
      return
    }
    let stale =
      summary.testedConfigRevision == 0
      ? summary.testedRevision != route.revision
      : summary.testedConfigRevision != route.configRevision
    let current = summary.method == "https204-median-v3"
    let reading: String
    if !summary.success {
      reading = summary.error.map(ProductErrorPresentation.message) ?? "测试失败"
    } else if current && (summary.stableSamples != 3 || !summary.stableError.isEmpty) {
      reading = "样本不足（\(summary.stableSamples)/3）"
    } else {
      reading =
        MeasurementFormatting.latency(
          Double(current ? summary.stableMS : summary.durationMS))?.text ?? "未测试"
    }
    text = reading + (stale || !current ? " · 历史" : "")
    detail =
      "\(reading)；\(MeasurementTimestamp.text(summary.at))；\(summary.method)"
      + (stale ? "；配置已变更" : "")
  }
}
