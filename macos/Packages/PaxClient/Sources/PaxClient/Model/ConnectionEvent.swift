import Foundation
import PaxProto

public struct ConnectionEvent: Sendable, Equatable {
  public var inbound: String
  public var session: String
  public var generation: UInt64
  public var routeID: String
  public var configRevision: UInt64
  public var connectionID: String
  public var target: String
  public var stage: String
  public var code: String
  public var durationMS: UInt64
  public init(
    session: String, generation: UInt64, routeID: String, configRevision: UInt64 = 0,
    connectionID: String, target: String, stage: String, code: String = "", durationMS: UInt64 = 0,
    inbound: String = ""
  ) {
    self.inbound = inbound
    self.session = session
    self.generation = generation
    self.routeID = routeID
    self.configRevision = configRevision
    self.connectionID = connectionID
    self.target = target
    self.stage = stage
    self.code = code
    self.durationMS = durationMS
  }
  init(_ p: Pax_V1_ConnectionEvent) {
    self.init(
      session: p.session, generation: p.generation, routeID: p.routeID,
      configRevision: p.configRevision, connectionID: p.connectionID, target: p.target,
      stage: p.stage, code: p.code, durationMS: p.durationMs, inbound: p.inbound)
  }
}
