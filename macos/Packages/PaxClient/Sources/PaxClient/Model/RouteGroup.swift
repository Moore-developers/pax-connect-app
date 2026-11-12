import Foundation

/// Saved custom group. Its position in the snapshot determines display order.
public struct RouteGroup: Sendable, Equatable, Identifiable {
  public var id: String
  public var name: String
  public var memberIDs: [String]
  public init(id: String, name: String, memberIDs: [String]) {
    self.id = id
    self.name = name
    self.memberIDs = memberIDs
  }
}

public struct RouteGroupsEdit: Sendable {
  public let groups: [RouteGroup]
  public let expectedRevision: UInt64
  public let requestID: String
  public init(groups: [RouteGroup], expectedRevision: UInt64, requestID: String = UUID().uuidString)
  {
    self.groups = groups
    self.expectedRevision = expectedRevision
    self.requestID = requestID
  }
}
