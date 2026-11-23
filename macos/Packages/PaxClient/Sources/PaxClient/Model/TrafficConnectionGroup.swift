import Foundation

/// A full-target aggregate of retained connections, before page truncation.
public struct TrafficConnectionGroup: Sendable, Equatable, Identifiable {
  public var id = ""
  public var target = ""
  public var kind = "unknown"
  public var connections: UInt64 = 0
  public var upload: UInt64 = 0
  public var download: UInt64 = 0
  public var bytesState = "unknown"
  public var lastAt = Date(timeIntervalSince1970: 0)
  public var rules: [TrafficBasis] = []
  public var exits: [TrafficExit] = []
  public var recentResponses: [TrafficResponsePoint] = []
  public var validLatencyCount: UInt64 = 0
  public var sources: [TrafficConnectionSource] = []
  public var addresses: [TrafficConnectionAddress] = []
  public var results: [TrafficConnectionResult] = []
  public init() {}
}

public struct TrafficConnectionSource: Sendable, Equatable {
  public var applicationID = ""
  public var application = ""
  public var process = ""
  public var kind = ""
  public var connections: UInt64 = 0
  public init() {}
}
public struct TrafficConnectionAddress: Sendable, Equatable {
  public var ip = ""
  public var port: UInt32 = 0
  public var transport = ""
  public var connections: UInt64 = 0
  public init() {}
}
public struct TrafficConnectionResult: Sendable, Equatable {
  public var state = "unknown"
  public var failure = ""
  public var connections: UInt64 = 0
  public init() {}
}
