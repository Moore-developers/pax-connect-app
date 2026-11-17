import Foundation
import PaxProto

/// Typed readback of one completed intent, independent of the current catalog.
public enum DaemonNetworkOperation: Sendable, Equatable {
  case proxy(
    status: ProtectionStatus, selectedRouteID: String, runningRouteID: String,
    degradedReasons: [ProductError])
  case measurements([Measurement])
  case currentPath(CurrentPathCheck)
  case connection(TrafficPathDiagnostics)
  case network(NetworkDiagnosticReport)
  case environment(EnvironmentDiagnosticReport)
  case cfWorkspace(CFWorkspace)
  case cfValidation(DaemonCFValidation)

  public struct Measurement: Sendable, Equatable {
    public let routeID: String
    public let configRevision: UInt64
    public let success: Bool
    public let error: ProductError?
    public let latency: RouteTestSummary?
    public let exit: RouteExitSummary?
    public let download: DownloadTestSummary?

    init(_ value: Pax_V1_RouteMeasurementResult) {
      routeID = value.routeID
      configRevision = value.configRevision
      success = value.success
      error = value.hasError ? ProductError(proto: value.error) : nil
      if case .latency(let result) = value.measurement {
        latency = RouteTestSummary(proto: result)
      } else {
        latency = nil
      }
      if case .exit(let result) = value.measurement {
        exit = RouteExitSummary(proto: result)
      } else {
        exit = nil
      }
      if case .download(let result) = value.measurement {
        download = DownloadTestSummary(proto: result)
      } else {
        download = nil
      }
    }
  }

  init?(_ value: Pax_V1_OperationState) {
    switch value.result {
    case .proxy(let result):
      self = .proxy(
        status: ProtectionStatus(proto: result.status), selectedRouteID: result.selectedRouteID,
        runningRouteID: result.runningRouteID,
        degradedReasons: result.degradedReasons.map(ProductError.init(proto:)))
    case .measurements(let result): self = .measurements(result.items.map(Measurement.init))
    case .currentPath(let result): self = .currentPath(CurrentPathCheck(result))
    case .connection(let result): self = .connection(TrafficPathDiagnostics(proto: result))
    case .network(let result): self = .network(NetworkDiagnosticReport(result))
    case .environment(let result): self = .environment(EnvironmentDiagnosticReport(result))
    case .cfWorkspace(let result): self = .cfWorkspace(CFWorkspace(result))
    case .cfValidation(let result): self = .cfValidation(DaemonCFValidation(result))
    case .routeSave, .aiProfile, .settings, .none: return nil
    }
  }
}

public struct DaemonOperationProgress: Sendable, Equatable {
  public let phase: String
  public let completed: UInt64?
  public let total: UInt64?

  init(_ value: Pax_V1_OperationProgress) {
    phase = value.phase
    completed = value.hasCompleted ? value.completed : nil
    total = value.hasTotal ? value.total : nil
  }
}
