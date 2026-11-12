import PaxProto

/// Foreground request progress. Counts include failed and cancelled probes.
public struct NetworkDiagnosticProgress: Sendable, Equatable {
  public let operationID: String
  public let probesCompleted, probesTotal: UInt32
  public let exitState, sourcesState: String
  public let finished: Bool

  public init(_ value: Pax_V1_NetworkDiagnosticProgress) {
    operationID = value.operationID
    probesCompleted = value.probesCompleted
    probesTotal = value.probesTotal
    exitState = value.exitState
    sourcesState = value.sourcesState
    finished = value.finished
  }
}
