import Foundation
import PaxProto

public struct RoutingRule: Sendable, Equatable, Identifiable {
  public var id = ""
  public var order: UInt32 = 0
  public var match = ""
  public var action = ""
  public var source = ""
  public init() {}
}
public struct RoutingRulesSnapshot: Sendable, Equatable {
  public var state = "unavailable"
  public var rules: [RoutingRule] = []
  public var at: Date?
  public init() {}
  init(_ p: Pax_V1_RoutingRulesSnapshot) {
    state = p.state
    at = p.hasAt ? p.at.date : nil
    rules = p.rules.map { row in
      var value = RoutingRule()
      value.id = row.id
      value.order = row.order
      value.match = row.match
      value.action = row.action
      value.source = row.source
      return value
    }
  }
}
