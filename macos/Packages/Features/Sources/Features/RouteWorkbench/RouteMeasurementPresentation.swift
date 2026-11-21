import AppKit
import PaxClient
import PaxCore
import PaxUI

extension RouteWorkbenchStore.Presentation {
  /// 出口与延迟可能来自相隔很久的两次测量；相差超过阈值时，在较旧一项的次要行标出测量时间。
  static let staleGap: TimeInterval = 600
  @MainActor var staleCaptions: (exit: String, latency: String) { staleCaptions(context: .chinese) }
  @MainActor func staleCaptions(context: PresentationContext) -> (exit: String, latency: String) {
    guard let exitAt, let latencyAt, abs(exitAt.timeIntervalSince(latencyAt)) > Self.staleGap
    else { return ("", "") }
    let caption =
      context.text("model_copy.0ac2645171") + MeasurementTimestamp.shortText(min(exitAt, latencyAt))
    return exitAt < latencyAt ? (caption, "") : ("", caption)
  }
}

extension RouteWorkbenchStore {
  func physicalCaption(context: PresentationContext) -> String {
    let value = routes?.physicalEgress
    let status: String
    switch value?.phase {
    case "ready": status = MeasurementTimestamp.text(value?.at)
    case "loading": status = context.text("route_readouts.3a1abf3422")
    default: status = context.text("route_readouts.c9c37a1eba")
    }
    return context.text("route_readouts.545aab0d2a") + " · " + status
  }

  static func measurementPresentation(
    _ node: NodeProfile, routes: RoutesStore, initial: Presentation,
    context: PresentationContext = .chinese
  ) -> Presentation {
    let id = node.id
    var value = initial
    switch routes.testState(node) {
    case .untested: break
    case .running:
      value.latency.running = true
      value.firstConnection.running = true
    case .stale:
      value.latency.failure = context.text("model_copy.675fdb706e")
      value.firstConnection.failure = context.text("model_copy.675fdb706e")
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
            value.latency.failure = context.text(
              "model_copy.d59b5691ac", ["arg0": String(describing: summary.stableSamples)])
          }
        } else {
          let error =
            summary.error?.code == "tls_certificate_invalid"
            ? context.text("model_copy.70693b08f4")
            : summary.error.map { ProductErrorPresentation.message($0, context: context) }
              ?? context.text("model_copy.77c9e582e8")
          value.firstConnection.failure = error
          value.latency.failure = error
        }
      } else {
        value.firstConnection.historical = true
        if summary.coldMS > 0 { value.firstConnection.value = Double(summary.coldMS) }
        if summary.success {
          value.latency.value = Double(summary.durationMS)
        } else {
          value.latency.failure =
            summary.error.map { ProductErrorPresentation.message($0, context: context) }
            ?? context.text("model_copy.77c9e582e8")
        }
      }
    }
    if let exit = node.exitSummary {
      value.lastExitLocation = exit.location ?? exit.lastLocation
      value.exitAt = exit.at
      if exit.testedConfigRevision != node.configRevision {
        value.exitMessage = context.text("model_copy.675fdb706e")
      } else {
        value.exitIP = exit.ip.isEmpty ? nil : exit.ip
        value.exitLocation = exit.location
        value.exitMessage = exit.error.map {
          ProductErrorPresentation.message(
            ProductError(
              code: $0.code,
              messageParams: exit.errorParams.isEmpty ? $0.messageParams : exit.errorParams,
              affectedTarget: $0.affectedTarget), context: context)
        }
      }
    }
    if routes.exitOperationIDs[id] != nil {
      value.exitMessage = context.text("model_copy.f651684e94")
    }
    if routes.downloadingRouteIDs.contains(id) {
      value.speed.running = true
      value.speed.queued = routes.downloadQueue?.queuedRouteIDs.contains(id) == true
    } else if let summary = node.downloadSummary {
      value.speed.time = MeasurementTimestamp.text(summary.at)
      if summary.testedConfigRevision != node.configRevision {
        value.speed.failure = context.text("model_copy.675fdb706e")
      } else if routes.capabilities.contains("routes.measure.v2"), summary.method != "window6s-v2" {
        value.speed.historical = true
        if summary.success { value.speed.value = summary.bytesPerSecond / 1_000_000 }
      } else if summary.success {
        value.speed.value = summary.bytesPerSecond / 1_000_000
      } else {
        value.speed.failure =
          summary.endReason == "body_interrupted"
          ? context.text("model_copy.49d1c8afbb")
          : summary.error.map { ProductErrorPresentation.message($0, context: context) }
            ?? context.text("model_copy.e2c3a337e8")
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
  func display(_ kind: RouteWorkbenchStore.Test, context: PresentationContext = .chinese) -> String
  {
    guard let reading = reading(kind) else { return text(context: context) }
    return reading.text + (historical ? context.text("model_copy.c1eb2557f1") : "")
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
