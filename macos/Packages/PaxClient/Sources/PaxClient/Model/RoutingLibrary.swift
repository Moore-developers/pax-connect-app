import Foundation
import PaxProto

public struct RoutingEntry: Sendable, Equatable {
  public var id = ""
  public var method = "domain"
  public var target = ""
  public var destination = "direct"
  public var enabled = true
  public init() {}
  init(_ p: Pax_V1_RoutingEntry) {
    id = p.id
    method = p.method
    target = p.target
    destination = p.destination
    enabled = p.enabled
  }
  var proto: Pax_V1_RoutingEntry {
    .with {
      $0.id = id
      $0.method = method
      $0.target = target
      $0.destination = destination
      $0.enabled = enabled
    }
  }
}
public struct RoutingPackage: Sendable, Equatable {
  public var id = ""
  public var name = ""
  public var kind = "local"
  public var destination = "direct"
  public var enabled = true
  public var rules: [RoutingEntry] = []
  public var source = ""
  public var version = ""
  public var previousVersion = ""
  public var automatic = false
  public var updateMessage = ""
  public var updatedAt: Date? = nil
  public init() {}
  init(_ p: Pax_V1_RoutingPackage) {
    id = p.id
    name = p.name
    kind = p.kind
    destination = p.destination
    enabled = p.enabled
    rules = p.rules.map(RoutingEntry.init)
    source = p.source
    version = p.version
    previousVersion = p.previousVersion
    automatic = p.automatic
    updateMessage = p.updateMessage
    updatedAt = p.updatedUnix > 0 ? Date(timeIntervalSince1970: Double(p.updatedUnix)) : nil
  }
  var proto: Pax_V1_RoutingPackage {
    .with {
      $0.id = id
      $0.name = name
      $0.kind = kind
      $0.destination = destination
      $0.enabled = enabled
      $0.rules = rules.map(\.proto)
      $0.source = source
      $0.version = version
      $0.previousVersion = previousVersion
      $0.automatic = automatic
    }
  }
}
public struct RoutingApplication: Sendable, Equatable {
  public var id = ""
  public var name = ""
  public var domains: [String] = []
  public init() {}
}
public struct RoutingLibrarySnapshot: Sendable, Equatable {
  public var revision: UInt64 = 0
  public var packages: [RoutingPackage] = []
  public var applications: [RoutingApplication] = []
  public var routes: [NodeProfile] = []
  public var state = ""
  public var message = ""
  public init() {}
  init(_ p: Pax_V1_RoutingLibrarySnapshot) {
    revision = p.revision
    packages = p.packages.map(RoutingPackage.init)
    applications = p.applications.map { p in
      var a = RoutingApplication()
      a.id = p.id
      a.name = p.name
      a.domains = p.domains
      return a
    }
    routes = p.routes.map { NodeProfile(proto: $0) }
    state = p.state
    message = p.message
  }
}
public struct RoutingMutation: Sendable {
  public var expectedRevision: UInt64 = 0
  public var action = ""
  public var packageID = ""
  public var ruleID = ""
  public var package = RoutingPackage()
  public var rule = RoutingEntry()
  public var rules: [RoutingEntry] = []
  public var delta: Int32 = 0
  public var enabled = true
  public var destination = ""
  public init() {}
  var proto: Pax_V1_RoutingMutationRequest {
    .with {
      $0.expectedRevision = expectedRevision
      $0.action = action
      $0.packageID = packageID
      $0.ruleID = ruleID
      $0.package = package.proto
      $0.rule = rule.proto
      $0.delta = delta
      $0.enabled = enabled
      $0.destination = destination
      $0.rules = rules.map(\.proto)
    }
  }
}

public struct GeoRulesPage: Sendable, Equatable {
  public var rules: [RoutingEntry] = []
  public var total: UInt32 = 0
  public var nextOffset: UInt32 = 0
  public var version = ""
  public var updatedAt: Date? = nil
  public init() {}
  init(_ p: Pax_V1_GeoRulesPage) {
    rules = p.rules.map(RoutingEntry.init)
    total = p.total
    nextOffset = p.nextOffset
    version = p.version
    updatedAt = p.updatedUnix > 0 ? Date(timeIntervalSince1970: Double(p.updatedUnix)) : nil
  }
}
