import Foundation

public struct ChainRoute: Sendable, Equatable {
  public let frontID: String
  public let finalID: String
  public init(frontID: String, finalID: String) {
    self.frontID = frontID
    self.finalID = finalID
  }
}
public struct ChainEdit: Sendable {
  public var id: String = ""
  public var name: String
  public var front: NodeProfile
  public var final: NodeProfile
  public var revision: UInt64 = 0
  public var requestID: String = UUID().uuidString
  public init(
    id: String = "", name: String, front: NodeProfile, final: NodeProfile, revision: UInt64 = 0
  ) {
    self.id = id
    self.name = name
    self.front = front
    self.final = final
    self.revision = revision
  }
}
public struct RouteHistorySample: Sendable, Equatable {
  public let exitLocation: ExitLocation?
  public let metric: String
  public let network: String
  public let at: Date
  public let configRevision: UInt64
  public let method: String
  public let value: Double
  public let success: Bool
  public let error: ProductError?
  public let exitIP: String
  public init(
    metric: String, network: String = "", at: Date, configRevision: UInt64, value: Double,
    success: Bool, error: ProductError? = nil, exitIP: String = "", method: String = "",
    exitLocation: ExitLocation? = nil
  ) {
    self.exitLocation = exitLocation
    self.method = method
    self.metric = metric
    self.network = network
    self.at = at
    self.configRevision = configRevision
    self.value = value
    self.success = success
    self.error = error
    self.exitIP = exitIP
  }
}
public struct RouteHistoryPage: Sendable {
  public let items: [RouteHistorySample]
  public let nextCursor: String
  public let partial: Bool
  public init(items: [RouteHistorySample], nextCursor: String = "", partial: Bool = false) {
    self.items = items
    self.nextCursor = nextCursor
    self.partial = partial
  }
}
public struct RouteUsageBucket: Sendable, Equatable {
  public var partial = false
  /// 该时段内新建的连接数。
  public var connections: UInt64 = 0
  public var seconds: UInt32 = 0
  public let at: Date
  public let upload: UInt64
  public let download: UInt64
  public let gap: Bool
  public init(at: Date, upload: UInt64, download: UInt64, gap: Bool = false) {
    self.at = at
    self.upload = upload
    self.download = download
    self.gap = gap
  }
}
public struct RouteUsagePage: Sendable {
  public var coverageStart: Date? = nil
  public var upload: UInt64 = 0
  public var download: UInt64 = 0
  public var from: Date? = nil
  public var until: Date? = nil
  public var basis = ""
  /// 所选时间窗内新建的连接总数（与「当前连接」不同）。
  public var windowConnections: UInt64 = 0
  public var items: [RouteUsageBucket]
  public var nextCursor: String
  public let partial: Bool
  public let todayUpload: UInt64
  public let todayDownload: UInt64
  public let connections: UInt64
  public let sampledAt: Date?
  public let error: ProductError?
  public init(
    items: [RouteUsageBucket], nextCursor: String = "", partial: Bool = false,
    todayUpload: UInt64 = 0, todayDownload: UInt64 = 0, connections: UInt64 = 0,
    sampledAt: Date? = nil, error: ProductError? = nil
  ) {
    self.items = items
    self.nextCursor = nextCursor
    self.partial = partial
    self.todayUpload = todayUpload
    self.todayDownload = todayDownload
    self.connections = connections
    self.sampledAt = sampledAt
    self.error = error
  }
}
