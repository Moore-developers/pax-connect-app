import Foundation

/// 产品日志（对应 proto 的 `ProductLog`）：面向用户的可读事件，不含原始 sing-box 日志。
///
/// `operationID` / `snapshotVersion` 用于把事件关联到一次操作与一份快照（spec.md 的日志验收）。
public struct ProductLog: Sendable, Equatable {
  public var flow: FlowFact? = nil
  public var recordID = ""
  public var problemID = ""
  public var occurrences: UInt64 = 0
  public var relatedConnections: UInt64 = 0
  public var firstAt: Date? = nil
  public var lastAt: Date? = nil
  public var category = ""
  public var eventID = ""
  public var eventKind = ""
  public var source = ""
  public var connectionKey = ""
  public var applicationID = ""
  public var application = ""
  public var domain = ""
  public var ip = ""
  public var routeID = ""
  public var routeName = ""
  public var direction = ""
  public var result = ""

  public let connection: ConnectionEvent?
  public let at: Date
  public let level: LogLevel
  public let message: String
  public let operationID: String
  public let snapshotVersion: UInt64

  public init(
    at: Date, level: LogLevel, message: String, operationID: String, snapshotVersion: UInt64,
    connection: ConnectionEvent? = nil
  ) {
    self.connection = connection
    self.at = at
    self.level = level
    self.message = message
    self.operationID = operationID
    self.snapshotVersion = snapshotVersion
  }
}

public enum LogLevel: Sendable, Equatable {
  case info
  case warning
  case error
}

/// 产品错误（对应 proto 的 `ProductError`）：UI 只见产品错误，不见 daemon 内部错误。
///
/// `code` 的枚举清单随 ticket 增长维护（code-standards.md §四第 1 条）；
/// UI 用 `code` + `messageParams` 渲染文案，不自行拼接半成品句子。
public struct ProductError: Error, Sendable, Equatable {
  public let code: String
  public let messageParams: [String]
  public let affectedTarget: String

  public init(code: String, messageParams: [String] = [], affectedTarget: String = "") {
    self.code = code
    self.messageParams = messageParams
    self.affectedTarget = affectedTarget
  }
}
