import Foundation

public struct PhysicalEgress: Sendable, Equatable {
  public let network: String
  public let ipv4: String
  public let ipv6: String
  public let phase: String
  public let at: Date?
  public let error: ProductError?
  public init(
    network: String, ipv4: String, ipv6: String, phase: String, at: Date? = nil,
    error: ProductError? = nil
  ) {
    self.network = network
    self.ipv4 = ipv4
    self.ipv6 = ipv6
    self.phase = phase
    self.at = at
    self.error = error
  }
}
public struct MeasurementPreferences: Sendable, Equatable {
  public var exitPoolState: ExitPoolState?
  public var revision: UInt64 = 1
  public var latency: UInt32 = 32
  public var exit: UInt32 = 8
  public var download: UInt32 = 8
  public var mode = "default"
  public var latencyMode = "default"
  public var exitMode = "default"
  public var downloadMode = "default"
  public var recommendedLatency: UInt32 = 0
  public var recommendedExit: UInt32 = 0
  public var recommendedDownload: UInt32 = 0
  public var network = ""
  public var evaluatedAt: Date?
  public init() {}
}
public struct CFCandidate: Sendable, Equatable {
  public let ip: String
  public let source: String
  public let latencyMS: UInt32
  public let network: String
  public let at: Date?
  public let error: ProductError?
  public init(
    ip: String, source: String, latencyMS: UInt32 = 0, network: String = "", at: Date? = nil,
    error: ProductError? = nil
  ) {
    self.ip = ip
    self.source = source
    self.latencyMS = latencyMS
    self.network = network
    self.at = at
    self.error = error
  }
}
public enum MeasurementRequest: Sendable {
  case networkPrivacy
  case saveNetworkPrivacy(NetworkPrivacy)
  case preferences
  case savePreferences(MeasurementPreferences)
  case candidates
  case cfWorkspace(CFWorkspaceQuery)
  case cfTask(String, [String])
  case validateCF(String, UInt64, String, String)
  case validateChainCF(String, UInt64, String, String, String, UInt64)
  case evaluate([NodeProfile])
  case refreshEgress
}
public enum MeasurementReply: Sendable {
  case networkPrivacy(NetworkPrivacy)
  case preferences(MeasurementPreferences)
  case candidates([CFCandidate])
  case candidate(CFCandidate)
  case workspace(CFWorkspace)
  case operation(String)
  case egress(PhysicalEgress)
}

public struct ExitPoolState: Sendable, Equatable {
  public let configuredLimit, inFlight, queued, retrying, availableProviders, totalProviders: UInt32
  public let at: Date?
  public init(
    configuredLimit: UInt32, inFlight: UInt32 = 0, queued: UInt32 = 0, retrying: UInt32 = 0,
    availableProviders: UInt32 = 0, totalProviders: UInt32 = 0, at: Date? = nil
  ) {
    self.configuredLimit = configuredLimit
    self.inFlight = inFlight
    self.queued = queued
    self.retrying = retrying
    self.availableProviders = availableProviders
    self.totalProviders = totalProviders
    self.at = at
  }
}
public enum RouteMeasurementPhase: Sendable, Equatable {
  case latency, exitQueued, exitRunning, exitRetrying, unknown
  public var caption: String {
    switch self {
    case .latency: "正在测量延迟"
    case .exitQueued: "出口查询排队中"
    case .exitRunning: "正在查询出口 IP"
    case .exitRetrying: "正在等待其他出口查询服务"
    case .unknown: "测量中"
    }
  }
}
public struct RouteMeasurementProgress: Sendable, Equatable {
  public let routeID, operationID, network: String
  public let testedConfigRevision: UInt64
  public let phase: RouteMeasurementPhase
  public let latencyResult: RouteTestSummary?
  public let updatedAt: Date?
  public let attempt: UInt32
  public init(
    routeID: String, operationID: String, testedConfigRevision: UInt64, network: String = "",
    phase: RouteMeasurementPhase, latencyResult: RouteTestSummary? = nil, updatedAt: Date? = nil,
    attempt: UInt32 = 0
  ) {
    self.routeID = routeID
    self.operationID = operationID
    self.testedConfigRevision = testedConfigRevision
    self.network = network
    self.phase = phase
    self.latencyResult = latencyResult
    self.updatedAt = updatedAt
    self.attempt = attempt
  }
}

public struct DownloadQueueState: Sendable, Equatable {
  public let mode: String
  public let activeRouteID: String
  public let queuedRouteIDs: [String]
  public let operationIDs: [String: String]
  public init(
    mode: String, activeRouteID: String = "", queuedRouteIDs: [String] = [],
    operationIDs: [String: String] = [:]
  ) {
    self.mode = mode
    self.activeRouteID = activeRouteID
    self.queuedRouteIDs = queuedRouteIDs
    self.operationIDs = operationIDs
  }
}
