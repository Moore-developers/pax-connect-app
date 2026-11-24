import Foundation

public struct TrafficAlertRule: Sendable, Equatable {
  public var enabled: Bool
  public var upload: UInt64
  public var download: UInt64
  public init(enabled: Bool = true, upload: UInt64, download: UInt64) {
    self.enabled = enabled
    self.upload = upload
    self.download = download
  }
}

public struct TrafficAlertSettings: Sendable, Equatable {
  public var revision: UInt64 = 1
  public var enabled = false
  public var mediumSeconds: UInt32 = 3_600
  public var short = TrafficAlertRule(upload: 100_000_000, download: 250_000_000)
  public var hour = TrafficAlertRule(upload: 500_000_000, download: 1_000_000_000)
  public var twoHours = TrafficAlertRule(upload: 1_000_000_000, download: 2_000_000_000)
  public var day = TrafficAlertRule(upload: 2_000_000_000, download: 5_000_000_000)
  public var medium: TrafficAlertRule {
    get { mediumSeconds == 7_200 ? twoHours : hour }
    set { if mediumSeconds == 7_200 { twoHours = newValue } else { hour = newValue } }
  }
  public init() {}
}

public struct TrafficAlertNotice: Sendable, Equatable {
  public var id: String
  public var key: String
  public var source: String
  public var sourceKind: String
  public var direction: String
  public var windowSeconds: UInt32
  public var bytes: UInt64
  public var threshold: UInt64
  public var until: Date
  public var routeID: String
  public var routeName: String
  public var routeCount: UInt32
  public var partial: Bool
  public init(
    id: String, key: String, source: String, sourceKind: String, direction: String,
    windowSeconds: UInt32, bytes: UInt64, threshold: UInt64, until: Date,
    routeID: String = "", routeName: String = "", routeCount: UInt32 = 1, partial: Bool = false
  ) {
    self.id = id
    self.key = key
    self.source = source
    self.sourceKind = sourceKind
    self.direction = direction
    self.windowSeconds = windowSeconds
    self.bytes = bytes
    self.threshold = threshold
    self.until = until
    self.routeID = routeID
    self.routeName = routeName
    self.routeCount = routeCount
    self.partial = partial
  }
}

public struct TrafficAlertState: Sendable, Equatable {
  public var revision: UInt64 = 0
  public var settings: TrafficAlertSettings
  public var monitorState: String
  public var errorCode: String
  public var notices: [TrafficAlertNotice]
  public var activeNoticeIDs: [String]
  public init(
    settings: TrafficAlertSettings = .init(), monitorState: String = "checking",
    errorCode: String = "", notices: [TrafficAlertNotice] = [], activeNoticeIDs: [String] = []
  ) {
    self.settings = settings
    self.monitorState = monitorState
    self.errorCode = errorCode
    self.notices = notices
    self.activeNoticeIDs = activeNoticeIDs
  }
}
