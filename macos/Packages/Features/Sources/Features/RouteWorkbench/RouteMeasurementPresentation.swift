import AppKit
import PaxClient
import PaxCore
import PaxUI

extension RouteWorkbenchStore.Presentation {
  /// 出口与延迟可能来自相隔很久的两次测量；相差超过阈值时，在较旧一项的次要行标出测量时间。
  static let staleGap: TimeInterval = 600
  @MainActor var staleCaptions: (exit: String, latency: String) {
    guard let exitAt, let latencyAt, abs(exitAt.timeIntervalSince(latencyAt)) > Self.staleGap
    else { return ("", "") }
    let caption = "测于 " + MeasurementTimestamp.shortText(min(exitAt, latencyAt))
    return exitAt < latencyAt ? (caption, "") : ("", caption)
  }
}

extension RouteWorkbenchStore {
  static func measurementPresentation(
    _ node: NodeProfile, routes: RoutesStore, initial: Presentation
  ) -> Presentation {
    let id = node.id
    var value = initial
    switch routes.testState(node) {
    case .untested: break
    case .running:
      value.latency.running = true
      value.firstConnection.running = true
    case .stale:
      value.latency.failure = "结果已过期"
      value.firstConnection.failure = "结果已过期"
    case .finished(let summary):
      let currentMethod = summary.method == "https204-median-v3"
      let legacy = !currentMethod
      value.latency.historical = legacy && summary.success
      value.latency.time = MeasurementTimestamp.text(summary.at)
      value.latencyAt = summary.at
      value.firstConnection.time = value.latency.time
      if currentMethod {
        if summary.success {
          if summary.coldMS > 0 { value.firstConnection.value = Double(summary.coldMS) }
          if summary.stableSamples == 3 && summary.stableError.isEmpty {
            value.latency.value = Double(summary.stableMS)
          } else {
            value.latency.failure = "样本不足（\(summary.stableSamples)/3）"
          }
        } else {
          let error =
            summary.error?.code == "tls_certificate_invalid"
            ? "证书校验失败" : summary.error.map(ProductErrorPresentation.message) ?? "测试失败"
          value.firstConnection.failure = error
          value.latency.failure = error
        }
      } else {
        value.firstConnection.historical = true
        if summary.coldMS > 0 { value.firstConnection.value = Double(summary.coldMS) }
        if summary.success {
          value.latency.value = Double(summary.durationMS)
        } else {
          value.latency.failure = summary.error.map(ProductErrorPresentation.message) ?? "测试失败"
        }
      }
    }
    if let exit = node.exitSummary {
      value.lastExitLocation = exit.location ?? exit.lastLocation
      value.exitAt = exit.at
      if exit.testedConfigRevision != node.configRevision {
        value.exitMessage = "结果已过期"
      } else {
        value.exitIP = exit.ip.isEmpty ? nil : exit.ip
        value.exitLocation = exit.location
        value.exitMessage = exit.error.map {
          ProductErrorPresentation.message(
            ProductError(
              code: $0.code,
              messageParams: exit.errorParams.isEmpty ? $0.messageParams : exit.errorParams,
              affectedTarget: $0.affectedTarget))
        }
      }
    }
    if routes.exitOperationIDs[id] != nil { value.exitMessage = "正在查询 IP 地区…" }
    if routes.downloadingRouteIDs.contains(id) {
      value.speed.running = true
      value.speed.time = routes.downloadQueue?.queuedRouteIDs.contains(id) == true ? "排队等待" : "正在下载"
    } else if let summary = node.downloadSummary {
      value.speed.time = MeasurementTimestamp.text(summary.at)
      if summary.testedConfigRevision != node.configRevision {
        value.speed.failure = "结果已过期"
      } else if routes.capabilities.contains("routes.measure.v2"), summary.method != "window6s-v2" {
        value.speed.historical = true
        if summary.success { value.speed.value = summary.bytesPerSecond / 1_000_000 }
      } else if summary.success {
        value.speed.value = summary.bytesPerSecond / 1_000_000
      } else {
        value.speed.failure =
          summary.endReason == "body_interrupted"
          ? "下载已开始，但正文传输中途断开，本次不生成速度结果"
          : summary.error.map(ProductErrorPresentation.message) ?? "测速失败"
      }
    }
    return value
  }
}

extension RouteWorkbenchStore.Measurement {
  func reading(_ kind: RouteWorkbenchStore.Test) -> MeasurementFormatting.Reading? {
    guard !running, failure == nil, let value else { return nil }
    return kind == .speed
      ? MeasurementFormatting.speed(bytesPerSecond: value * 1_000_000)
      : MeasurementFormatting.latency(value)
  }
  func display(_ kind: RouteWorkbenchStore.Test) -> String {
    guard let reading = reading(kind) else { return text }
    return reading.text + (historical ? " · 历史" : "")
  }
  func displaySamples(_ kind: RouteWorkbenchStore.Test) -> [Double] {
    guard let unit = reading(kind)?.unit else { return [] }
    let scale: Double
    if kind == .speed {
      scale = unit == "B/s" ? 1_000_000 : unit == "KB/s" ? 1000 : unit == "GB/s" ? 0.001 : 1
    } else {
      scale = unit == "s" ? 0.001 : 1
    }
    return samples.map { $0 * scale }
  }
  var latencyColor: NSColor {
    if historical || running { return PaxTheme.Color.textSecondary }
    if failure != nil { return PaxTheme.Color.error }
    switch MeasurementFormatting.latencyGrade(value) {
    case .good: return PaxTheme.Color.healthy
    case .moderate: return PaxTheme.Color.warning
    case .slow: return PaxTheme.Color.error
    case .unknown: return PaxTheme.Color.textSecondary
    }
  }
}
