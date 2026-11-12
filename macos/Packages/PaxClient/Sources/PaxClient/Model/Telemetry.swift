import Foundation

// Product history values; protobuf stays inside PaxClient.
public struct HistoryState: Sendable, Equatable {
  public var aggregateCoverageStart: Date? = nil
  public var summaryGeneration = ""
  public var logsRevision: UInt64 = 0
  public var trafficRevision: UInt64 = 0
  public var logHeadSequence: UInt64 = 0
  public var logDeletionGeneration: UInt64 = 0
  public var logRemovalBeforeMs: Int64 = 0
  public var trafficDeletionGeneration: UInt64 = 0
  public var routeHistoryRevision: UInt64 = 0
  public var journalGeneration: String = ""

  public var revision: UInt64 = 0
  public var clearGeneration: UInt64 = 0
  public var coverageStart: Date? = nil
  public var partial: Bool = false
  public var capacityEvicted: Bool = false
  public var collectionState: String = "idle"
  public var errorCode: String = ""
  public init() {}
}

extension HistoryState {
  /// Unknown deletion scope, rebuilds and journal replacement revoke every query.
  public func invalidatesLogs(since previous: Self, from: Date?) -> Bool {
    if summaryGeneration != previous.summaryGeneration
      || journalGeneration != previous.journalGeneration
    {
      return true
    }
    guard logDeletionGeneration != previous.logDeletionGeneration else { return false }
    guard logRemovalBeforeMs > 0, let from else { return true }
    return from.timeIntervalSince1970 * 1000 < Double(logRemovalBeforeMs)
  }
}

public struct LogFilter: Sendable, Equatable, Hashable {
  public var scope = ""
  public var quick = "all"
  public var criteria = LogCriteria()
  public var includeDNS = false
  public var view = ""
  public var problemID = ""
  public var connectionKey = ""
  public var dimension: String = "domain"
  public var objectID: String = ""
  public var search: String = ""
  public var level: String = ""
  public var routeID: String = ""
  public var from: Date? = nil
  public var until: Date? = nil
  public var readToken: String = ""
  public var cursor: String = ""
  public var limit: UInt32 = 100
  public init() {}
}

public struct HistoryLogPage: Sendable, Equatable {
  public var options = LogOptions()
  public var records: [LogRecord] = []
  public var asOf: Date? = nil
  public var problemGroups: UInt64 = 0
  public var headSequence: UInt64 = 0
  public var items: [ProductLog] = []
  public var nextCursor: String = ""
  public var readToken: String = ""
  public var history: HistoryState = .init()
  public var total: UInt64 = 0
  public init() {}
}

public struct HistoryObject: Sendable, Equatable {
  public var symbol = ""
  public var id: String = ""
  public var name: String = ""
  public var count: UInt64 = 0
  public init() {}
}

public struct HistoryObjectPage: Sendable, Equatable {
  public var selectedObject: HistoryObject? = nil
  public var items: [HistoryObject] = []
  public var nextCursor: String = ""
  public var readToken: String = ""
  public var history: HistoryState = .init()
  public var totalEvents: UInt64 = 0
  public var unknownEvents: UInt64 = 0
  public init() {}
}

public struct FlowFact: Sendable, Equatable {
  public var sourceKind = ""
  public var sourceLabel = ""
  public var sourceEvidence = ""
  public var phase = ""
  public var purpose = ""
  public var ipSource = ""
  public var processStatus = ""
  public var dns: DNSFact? = nil
  public var primaryDomain = ""
  public var responseKnown = false
  public var responseMS: UInt64 = 0
  public var stateUnknown = false
  public var observedUntil: Date? = nil
  public var key: String = ""
  public var applicationID: String = ""
  public var application: String = ""
  public var process: String = ""
  public var domain: String = ""
  public var ip: String = ""
  public var port: UInt32 = 0
  public var transport: String = ""
  public var routeID: String = ""
  public var routeName: String = ""
  public var direction: String = "unknown"
  public var rule: String = ""
  public var ruleSource: String = ""
  public var startedAt: Date = Date(timeIntervalSince1970: 0)
  public var closedAt: Date? = nil
  public var establishmentState: String = "not_measurable"
  public var establishmentMS: UInt64 = 0
  public var failure: String = ""
  public var upload: UInt64 = 0
  public var download: UInt64 = 0
  public var evidence: String = ""
  public init() {}
}

public struct DNSFact: Sendable, Equatable {
  public var queryType = ""
  public var result = ""
  public var answers: [String] = []
  public init() {}
}

public struct HistoryLogDetail: Sendable, Equatable {
  public var record: LogRecord? = nil
  public var timeline: [ProductLog] = []
  public var totalEvents: UInt64 = 0
  public var event: ProductLog? = nil
  public var flow: FlowFact? = nil
  public var raw: [String] = []
  public var truncated: Bool = false
  public var rawOmitted: String = ""
  public init() {}
}

public struct TrafficFilter: Sendable, Equatable, Hashable {
  public var until: Date? = nil
  public var dimension: String = "application"
  public var direction: String = "all"
  public var window: String = "minute"
  public var search: String = ""
  public var readToken: String = ""
  public var cursor: String = ""
  public var limit: UInt32 = 100
  public init() {}
}

public struct TrafficLatencySample: Sendable, Hashable {
  public var count: UInt64 = 0
  public var p95MS: UInt64 = 0
  public var seconds: UInt32 = 0
  public var at = Date(timeIntervalSince1970: 0)
  public var milliseconds: UInt64 = 0
  public var known = false
  public init() {}
}
public struct TrafficResponsePoint: Sendable, Hashable {
  public var connectionKey: String
  public var startedAt: Date
  public var milliseconds: UInt64
  public init(connectionKey: String, startedAt: Date, milliseconds: UInt64) {
    self.connectionKey = connectionKey
    self.startedAt = startedAt
    self.milliseconds = milliseconds
  }
}
public struct TrafficExit: Sendable, Hashable {
  public var connections: UInt64 = 0
  public var routeID = ""
  public var routeName = ""
  public var direction = ""
  public var upload: UInt64 = 0
  public var download: UInt64 = 0
  public init() {}
}
public struct TrafficRoutingMatch: Sendable, Equatable {
  public var at: Date
  public var rule: String
  public var source: String
  public var applicationID: String
  public var application: String
  public var identity: String
  public init(
    at: Date, rule: String, source: String, applicationID: String = "", application: String = "",
    identity: String
  ) {
    self.at = at
    self.rule = rule
    self.source = source
    self.applicationID = applicationID
    self.application = application
    self.identity = identity
  }
}

public struct TrafficRow: Sendable, Equatable {
  public var latestMatch: TrafficRoutingMatch? = nil
  public var exits: [TrafficExit] = []
  public var recentResponses: [TrafficResponsePoint] = []
  public var recentResponsesState = ""
  public var recentResponsesError = ""
  public var p50MS: UInt64 = 0
  public var p95MS: UInt64 = 0
  public var latencyAggregated = false
  public var sourceKind = ""
  public var sourceLabel = ""
  public var sourceEvidence = ""
  public var kind = "access"
  public var bytesKnown = true
  public var validLatencyCount: UInt64 = 0
  public var missingLatencyCount: UInt64 = 0
  public var failedCount: UInt64 = 0
  public var earlierCount: UInt64 = 0
  public var latencySampled = false
  public var latestLatencyMS: UInt64 = 0
  public var latestLatencyAt: Date? = nil
  /// 窗口内最近一次活动；列表按它从新到旧排序，nil 表示未知。
  public var lastActiveAt: Date? = nil
  public var latencySamples: [TrafficLatencySample] = []
  public var latencyKnown = false
  public var slowestLatencyMS: UInt64 = 0
  public var bases: [TrafficBasis] = []
  public var id: String = ""
  public var objectID: String = ""
  public var name: String = ""
  public var routeID: String = ""
  public var routeName: String = ""
  public var direction: String = "unknown"
  public var upload: UInt64 = 0
  public var download: UInt64 = 0
  public var connections: UInt64 = 0
  public var slowestMS: UInt64 = 0
  public var establishmentKnown: Bool = false
  public var basis: String = ""
  public init() {}
}

public struct TrafficPoint: Sendable, Equatable {
  public var partial = false
  public var seconds: UInt32 = 0
  public var at: Date = Date(timeIntervalSince1970: 0)
  public var upload: UInt64 = 0
  public var download: UInt64 = 0
  public var gap: Bool = false
  public init() {}
}

public struct TrafficPage: Sendable, Equatable {
  public var window = ""
  public var unknownSource: TrafficRow? = nil
  public var dnsSummary: TrafficRow? = nil
  public var items: [TrafficRow] = []
  public var nextCursor: String = ""
  public var readToken: String = ""
  public var asOf: Date = Date(timeIntervalSince1970: 0)
  public var from: Date = Date(timeIntervalSince1970: 0)
  public var upload: UInt64 = 0
  public var download: UInt64 = 0
  public var connections: UInt64 = 0
  /// 仅应用页：系统服务的流量，不在应用小计内。
  public var systemUpload: UInt64 = 0
  public var systemDownload: UInt64 = 0
  public var otherProcessUpload: UInt64 = 0
  public var otherProcessDownload: UInt64 = 0
  public var otherProcessConnections: UInt64 = 0
  public var trend: [TrafficPoint] = []
  public var history: HistoryState = .init()
  public var totalRows: UInt64 = 0
  public init() {}
}

public struct TrafficBasis: Sendable, Equatable {
  public var target = ""
  public var routeName = ""
  public var rule: String = ""
  public var source: String = ""
  public var connections: UInt64 = 0
  public init() {}
}

public struct TrafficDetail: Sendable, Equatable {
  public var retainedConnectionCount: UInt64 = 0
  public var recentResponses: [TrafficResponsePoint] = []
  public var recentResponsesState = ""
  public var recentResponsesError = ""
  public var aggregateOnly = false
  public var window = ""
  public var trend: [TrafficPoint] = []
  public var dnsGroups: [DNSTrafficGroup] = []
  public var dnsGroupID = ""
  public var dnsGroupTotal: UInt64 = 0
  public var related: [TrafficRelated] = []
  public var timings: [FlowFact] = []
  public var successful: UInt64 = 0
  public var failed: UInt64 = 0
  public var unmeasurable: UInt64 = 0
  public var earlier: UInt64 = 0
  public var timingsTruncated = false
  public var summary: TrafficRow = .init()
  public var items: [FlowFact] = []
  public var basis: [TrafficBasis] = []
  public var nextCursor: String = ""
  public var readToken: String = ""
  public var asOf: Date = Date(timeIntervalSince1970: 0)
  public var from: Date = Date(timeIntervalSince1970: 0)
  public var history: HistoryState = .init()
  public init() {}
}

public struct TrafficDetailQuery: Sendable, Equatable, Hashable {
  public var dnsGroupID = ""
  public var rowID: String = ""
  public var connectionKey: String = ""
  public var readToken: String = ""
  public var cursor: String = ""
  public var limit: UInt32 = 100
  public init() {}
}

public struct TrafficRelated: Sendable, Equatable {
  public var id = ""
  public var name = ""
  public var connectionKeys: [String] = []
  public var download: UInt64 = 0
  public var connections: UInt64 = 0
  public init() {}
}
public enum TrafficDimension: Int, Sendable { case application, domain, ip, system }

public struct CurrentPathCheck: Sendable, Equatable {
  public var commandOperationIDs: [String] = []
  public var checkID = ""
  public var triggerOperationID = ""
  public var routeID = ""
  public var configRevision: UInt64 = 0
  public var coreSession = ""
  public var runtimeVersion: UInt64 = 0
  public var networkEpoch = ""
  public var state = "idle"
  public var cause = ""
  public var startedAt: Date? = nil
  public var completedAt: Date? = nil
  public var evidenceScope = "current_mixed_egress"
  public init() {}
}
public struct HistoryDelta: Sendable {
  public var history = HistoryState()
  public var traffic: TrafficSnapshot? = nil
  public var recentLogs: [ProductLog] = []
  public var replaceRecentLogs = false
  public init() {}
}
public struct LogUpdateRequest: Sendable {
  public var filter = LogFilter()
  public var afterSequence: UInt64 = 0
  public var deletionGeneration: UInt64 = 0
  public var journalGeneration = ""
  public var countOnly = true
  public var limit: UInt32 = 200
  public init() {}
}
public struct LogUpdatePage: Sendable {
  public var items: [ProductLog] = []
  public var count: UInt64 = 0
  public var exact = true
  public var highWater: UInt64 = 0
  public var nextSequence: UInt64 = 0
  public var hasMore = false
  public var resetRequired = false
  public var history = HistoryState()
  public var asOf = Date(timeIntervalSince1970: 0)
  public init() {}
}

public struct LogDetailFilter: Sendable {
  public var recordID = ""
  public var query = LogFilter()
  public init() {}
}

public struct ConnectionMetrics: Sendable, Equatable {
  public var durationMS: UInt64 = 0
  public var durationState = "unknown"
  public var upload: UInt64 = 0
  public var download: UInt64 = 0
  public var bytesState = "unknown"
  public var bytesFrom: Date? = nil
  public var asOf: Date? = nil
  public var reason = ""
  public init() {}
}
public struct LogRecord: Sendable, Equatable, Identifiable {
  public var systemEvent: SystemEvent? = nil
  public var categoryName = ""
  public var categorySymbol = ""
  public var systemCategory = ""
  public var operationFrom = ""
  public var operationTo = ""
  public var id: String { recordID }
  public var kind = "diagnostic"
  public var recordID = ""
  public var eventID = ""
  public var level: LogLevel = .info
  public var title = ""
  public var source = ""
  public var flow: FlowFact? = nil
  public var metrics = ConnectionMetrics()
  public var firstAt: Date? = nil
  public var lastAt: Date? = nil
  public var eventCount: UInt64 = 0
  public var asOf: Date? = nil
  public init() {}
}

public struct LogCriteria: Sendable, Equatable, Hashable {
  public var kinds: [String] = []
  public var levels: [String] = []
  public var application = ""
  public var domain = ""
  public var domainMode = "contains"
  public var targetIP = ""
  public var transports: [String] = []
  public var port: UInt32 = 0
  public var directions: [String] = []
  public var route = ""
  public var rulePackage = ""
  public var minLatencyMS: UInt64 = 0
  public var dnsAnswerIP = ""
  public init() {}
}
public struct LogOptions: Sendable, Equatable {
  public var applications: [String] = []
  public var routes: [String] = []
  public var rulePackages: [String] = []
  public init() {}
}

public struct EventCategory: Sendable, Equatable {
  public var key = ""
  public var name = ""
  public var symbol = ""
  public init() {}
}
public struct EventField: Sendable, Equatable {
  public var label = ""
  public var value = ""
  public var before = ""
  public var after = ""
  public init() {}
}
public struct EventSection: Sendable, Equatable {
  public var kind = ""
  public var title = ""
  public var fields: [EventField] = []
  public init() {}
}
public struct SystemEvent: Sendable, Equatable {
  public var type = ""
  public var name = ""
  public var category = EventCategory()
  public var result = ""
  public var errorCode = ""
  public var startedAt: Date? = nil
  public var sections: [EventSection] = []
  public init() {}
}

public struct DNSTrafficGroup: Sendable, Equatable {
  public var id = ""
  public var domain = ""
  public var queryType = ""
  public var result = ""
  public var answers: [String] = []
  public var queries: UInt64 = 0
  public var lastAt = Date(timeIntervalSince1970: 0)
  public var latencyKnown = false
  public var latencyMS: UInt64 = 0
  public init() {}
}
