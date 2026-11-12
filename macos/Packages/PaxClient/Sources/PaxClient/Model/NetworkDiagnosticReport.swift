import Foundation
import PaxProto

/// Bounded evidence from one frozen route; never contains raw upstream responses.
public struct NetworkDiagnosticReport: Sendable, Equatable, Codable {
  public struct Source: Sendable, Equatable, Codable {
    public let name, country, city, asn, org, providerDate: String
    public let providerID: String?
  }
  public struct Fact: Sendable, Equatable, Codable {
    public let group, label, value, source, state: String
    public let providerID: String?
  }
  public struct Provider: Sendable, Equatable, Codable {
    public let id, queryIP, family, state, errorCode, fallbackReason: String
    public let capabilities, fallbackFor: [String]
    public let httpStatus: UInt32
    public let durationMS: UInt64
    public let at: Date?
  }
  public struct Probe: Sendable, Equatable, Codable {
    public let target: String
    public let state, stage, redirectHost: String?
    public let attempt, httpStatus: UInt32
    public let durationMS: UInt64
    public let errorCode: String
    public let expectedResponse: Bool
    public let at: Date?
  }
  public let targetRouteID, targetRouteName, runningRouteID, runningRouteName: String
  public let configRevision: UInt64
  public let network: String
  public let at: Date
  public let changedDuringCapture: Bool
  public let mode: String
  public let exit: ExitLocation?
  public let exitError, providerError: String
  public let score: Int?
  public let scoreModel: String
  public let scoreCeiling: Int
  public let ipType, timezone, country: String
  public let intelAnswered, intelTotal: Int
  public let sources: [Source]
  public let facts: [Fact]
  public let providers: [Provider]?
  public private(set) var probes: [Probe]

  public func preservingProbes(_ probes: [Probe]) -> Self {
    var copy = self
    copy.probes = probes
    return copy
  }

  init(_ p: Pax_V1_NetworkDiagnosticReport) {
    targetRouteID = p.targetRouteID
    targetRouteName = p.targetRouteName
    runningRouteID = p.runningRouteID
    runningRouteName = p.runningRouteName
    configRevision = p.configRevision
    network = p.network
    at = p.at.date
    changedDuringCapture = p.changedDuringCapture
    mode = p.mode
    exit = p.hasExit ? ExitLocation(proto: p.exit) : nil
    exitError = p.exitError
    providerError = p.providerError
    score = p.hasScore ? Int(p.score) : nil
    scoreModel = p.scoreModel
    scoreCeiling = Int(p.scoreCeiling)
    ipType = p.ipType
    timezone = p.timezone
    country = p.country
    intelAnswered = Int(p.intelAnswered)
    intelTotal = Int(p.intelTotal)
    sources = p.sources.map {
      Source(
        name: $0.name, country: $0.country, city: $0.city, asn: $0.asn,
        org: $0.org, providerDate: $0.providerDate,
        providerID: $0.providerID.isEmpty ? nil : $0.providerID)
    }
    facts = p.facts.map {
      Fact(
        group: $0.group, label: $0.label, value: $0.value, source: $0.source, state: $0.state,
        providerID: $0.providerID.isEmpty ? nil : $0.providerID)
    }
    providers =
      p.providers.isEmpty
      ? nil
      : p.providers.map {
        Provider(
          id: $0.id, queryIP: $0.queryIp, family: $0.family, state: $0.state,
          errorCode: $0.errorCode, fallbackReason: $0.fallbackReason,
          capabilities: $0.capabilities, fallbackFor: $0.fallbackFor,
          httpStatus: $0.httpStatus, durationMS: $0.durationMs,
          at: $0.hasAt ? $0.at.date : nil)
      }
    probes = p.probes.map {
      Probe(
        target: $0.target, state: $0.state, stage: $0.stage, redirectHost: $0.redirectHost,
        attempt: $0.attempt, httpStatus: $0.httpStatus,
        durationMS: $0.durationMs, errorCode: $0.errorCode,
        expectedResponse: $0.expectedResponse, at: $0.hasAt ? $0.at.date : nil)
    }
  }
}
