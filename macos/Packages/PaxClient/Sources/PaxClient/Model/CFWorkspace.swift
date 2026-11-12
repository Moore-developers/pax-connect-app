import Foundation
import PaxProto

public struct CFSample: Codable, Sendable, Equatable {
  public var value: Double = 0
  public var measuredAt: Date?
  public var attemptedAt: Date?
  public var errorCode = ""
  public var rounds: UInt32 = 0
  public init() {}
  init(_ p: Pax_V1_CFSample) {
    value = p.value
    measuredAt = p.hasMeasuredAt ? p.measuredAt.date : nil
    attemptedAt = p.hasAttemptedAt ? p.attemptedAt.date : nil
    errorCode = p.errorCode
    rounds = p.successfulRounds
  }
}
public struct CFIPLocation: Codable, Sendable, Equatable {
  public var countryCode = ""
  public var country = ""
  public var region = ""
  public var city = ""
  public var source = ""
  public var at: Date?
  public var errorCode = ""
  public init() {}
  init(_ p: Pax_V1_CFIPLocation) {
    countryCode = p.countryCode
    country = p.country
    region = p.region
    city = p.city
    source = p.source
    at = p.hasAt ? p.at.date : nil
    errorCode = p.errorCode
  }
}
public struct CFRecord: Codable, Sendable, Equatable, Identifiable {
  public var id: String { ip }
  public var ip = ""
  public var location: CFIPLocation?
  public var prefix = ""
  public var network = ""
  public var tcp = CFSample()
  public var download = CFSample()
  public var validation = CFSample()
  public var reachable = false
  public var proofCurrent = false
  public var validationMatches = false
  public var issue = ""
  public init() {}
  init(_ p: Pax_V1_CFRecord) {
    ip = p.ip
    location = p.hasLocation ? CFIPLocation(p.location) : nil
    prefix = p.prefix
    network = p.network
    tcp = CFSample(p.tcp)
    download = CFSample(p.download)
    validation = CFSample(p.validation)
    reachable = p.reachable
    proofCurrent = p.proofCurrent
    validationMatches = p.validationMatches
    issue = p.issue
  }
}
public struct CFNetwork: Sendable, Equatable {
  public let id: String
  public let count: UInt32
  public let at: Date?
}
public struct CFWorkspace: Sendable, Equatable {
  public var network = ""
  public var currentNetwork = ""
  public var records: [CFRecord] = []
  public var networks: [CFNetwork] = []
  public var operationID = ""
  public var phase = ""
  public var completed: UInt32 = 0
  public var total: UInt32 = 0
  public var monitoring = false
  public var pendingChecks: UInt32 = 0
  public var oldestCheck: Date?
  public var error: ProductError?
  public init() {}
  init(_ p: Pax_V1_CFWorkspace) {
    network = p.network
    currentNetwork = p.currentNetwork
    records = p.records.map(CFRecord.init)
    networks = p.networks.map {
      CFNetwork(id: $0.id, count: $0.count, at: $0.hasAt ? $0.at.date : nil)
    }
    operationID = p.operationID
    phase = p.phase
    completed = p.completed
    total = p.total
    monitoring = p.monitoring
    pendingChecks = p.pendingChecks
    oldestCheck = p.hasOldestCheck ? p.oldestCheck.date : nil
    error = p.hasError ? ProductError(proto: p.error) : nil
  }
}
public struct CFWorkspaceQuery: Sendable {
  public var network = ""
  public var routeID: String
  public var revision: UInt64
  public var document: String
  public init(routeID: String, revision: UInt64, document: String) {
    self.routeID = routeID
    self.revision = revision
    self.document = document
  }
}
