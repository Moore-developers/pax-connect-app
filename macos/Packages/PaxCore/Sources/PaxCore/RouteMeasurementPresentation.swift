import PaxClient

extension RouteMeasurementPhase {
  public func caption(context: PresentationContext) -> String {
    switch self {
    case .latency: context.text("measurement.phase.latency")
    case .exitQueued: context.text("measurement.phase.exit_queued")
    case .exitRunning: context.text("measurement.phase.exit_running")
    case .exitRetrying: context.text("measurement.phase.exit_retrying")
    case .unknown: context.text("measurement.phase.unknown")
    }
  }
}
