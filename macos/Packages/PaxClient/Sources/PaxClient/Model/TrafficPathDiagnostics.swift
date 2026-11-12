import Foundation
import PaxProto

public struct TrafficPathFinding: Sendable, Equatable {
  public let kind: String
  public let subject: String
  public let state: String
  public let value: String
  public init(kind: String, subject: String, state: String, value: String = "") {
    self.kind = kind
    self.subject = subject
    self.state = state
    self.value = value
  }
}

public struct TrafficPathDiagnostics: Sendable, Equatable {
  public struct Step: Sendable, Equatable {
    public var kind: String
    public var state: String
    public var code: String
    public var durationMS: UInt64
    public var at: Date?
    public init(
      kind: String, state: String, code: String = "", durationMS: UInt64 = 0, at: Date? = nil
    ) {
      self.kind = kind
      self.state = state
      self.code = code
      self.durationMS = durationMS
      self.at = at
    }
    init(_ p: Pax_V1_ConnectionDiagnosticStep) {
      self.init(
        kind: p.kind, state: p.state, code: p.code, durationMS: p.durationMs,
        at: p.hasAt ? p.at.date : nil)
    }
  }
  public var targetRouteID = ""
  public var targetRouteName = ""
  public var runningRouteID = ""
  public var runningRouteName = ""
  public var targetRevision: UInt64 = 0
  public var operationID = ""
  public var attemptAt: Date?
  public var attemptRouteName = ""
  public var attemptMatchesTarget = false
  public var rechecked = false
  public var steps: [Step] = []
  public var attemptSteps: [Step] = []
  public let at: Date
  public let partial: Bool
  public let changedDuringCapture: Bool
  public let findings: [TrafficPathFinding]
  public let mixedConnections: UInt64
  public let lastMixedConnection: Date?
  public let tunConnections: UInt64
  public let proxyRoutes: UInt64
  public let directRoutes: UInt64
  public let lastTunConnection: Date?
  public let runtimeObservationAvailable: Bool
  public init(
    at: Date, partial: Bool = false, changedDuringCapture: Bool = false,
    findings: [TrafficPathFinding], tunConnections: UInt64 = 0, proxyRoutes: UInt64 = 0,
    directRoutes: UInt64 = 0, lastTunConnection: Date? = nil,
    runtimeObservationAvailable: Bool = false, mixedConnections: UInt64 = 0,
    lastMixedConnection: Date? = nil
  ) {
    self.mixedConnections = mixedConnections
    self.lastMixedConnection = lastMixedConnection
    self.at = at
    self.partial = partial
    self.changedDuringCapture = changedDuringCapture
    self.findings = findings
    self.tunConnections = tunConnections
    self.proxyRoutes = proxyRoutes
    self.directRoutes = directRoutes
    self.lastTunConnection = lastTunConnection
    self.runtimeObservationAvailable = runtimeObservationAvailable
  }
  init(proto: Pax_V1_TrafficPathDiagnostics) {
    self.init(
      at: proto.at.date, partial: proto.partial, changedDuringCapture: proto.changedDuringCapture,
      findings: proto.findings.map {
        TrafficPathFinding(kind: $0.kind, subject: $0.subject, state: $0.state, value: $0.value)
      }, tunConnections: proto.tunConnections, proxyRoutes: proto.proxyRoutes,
      directRoutes: proto.directRoutes,
      lastTunConnection: proto.hasLastTunConnection ? proto.lastTunConnection.date : nil,
      runtimeObservationAvailable: proto.runtimeObservationAvailable,
      mixedConnections: proto.mixedConnections,
      lastMixedConnection: proto.hasLastMixedConnection ? proto.lastMixedConnection.date : nil)
    targetRouteID = proto.targetRouteID
    targetRouteName = proto.targetRouteName
    runningRouteID = proto.runningRouteID
    runningRouteName = proto.runningRouteName
    targetRevision = proto.targetRevision
    operationID = proto.operationID
    attemptAt = proto.hasAttemptAt ? proto.attemptAt.date : nil
    attemptRouteName = proto.attemptRouteName
    attemptMatchesTarget = proto.attemptMatchesTarget
    rechecked = proto.rechecked
    steps = proto.steps.map(Step.init)
    attemptSteps = proto.attemptSteps.map(Step.init)

  }
}
