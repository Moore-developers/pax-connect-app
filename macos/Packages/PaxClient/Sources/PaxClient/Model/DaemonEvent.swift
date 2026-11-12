/// daemon 推给 UI 的事件（`WatchEvent.payload` 的领域模型形态）。
///
/// 上层只认这些 case，不认识 proto；新增载荷时在这里加 case 并在 `init(proto:)` 里映射。
public enum DaemonEvent: Sendable {
  case historyDelta(HistoryDelta)
  case currentPathCheck(CurrentPathCheck)
  case snapshot(DaemonSnapshot)
  case operation(OperationResult)
  /// AI 用量版本号、代数与采集状态；明细由消费页带已知版本号查询。
  case aiUsage(AIUsageCollectionState)
}

/// 完整快照：冷启动、重连、窗口重新可见时由 daemon 提供，是 UI 的唯一真相源。
public struct DaemonSnapshot: Sendable, Equatable {
  public var history = HistoryState()
  public var currentPathCheck = CurrentPathCheck()
  /// 首帧 AI 用量状态；daemon 未采集时为 nil。
  public var aiUsage: AIUsageCollectionState?
  /// 非 Pax 的 utun 承接公网默认路由时非空。
  public var externalTUN: ExternalTUN?
  public let downloadQueue: DownloadQueueState?
  public let exitOperationIDs: [String: String]
  public let testOperationIDs: [String: String]
  public let latencyOperationID: String
  public let exitPoolState: ExitPoolState?
  public let routeMeasurementProgress: [RouteMeasurementProgress]
  public let connectionSession: String
  public let connectionGeneration: UInt64
  public let physicalEgress: PhysicalEgress?
  public let catalogRevision: UInt64
  public let selectedRouteID: String
  public let pendingTargetRouteID: String
  public let activeTLS: ActiveTLS?
  public let degradedReasons: [ProductError]
  public let pendingOperationID: String
  public let degradedReason: ProductError?
  public let snapshotVersion: UInt64
  public let status: ProtectionStatus
  public let routes: [NodeProfile]
  public let currentRoute: NodeProfile?
  public let todayTraffic: TrafficSnapshot
  public let settings: DaemonSettings
  public let recentLogs: [ProductLog]
  /// 已保存订阅来源的脱敏摘要；URL 与 query 永不出现在这里。
  public let subscriptions: [SubscriptionSummary]
  /// 正在进行连通性测试的线路 ID；结果经 RouteTestSummary 与操作事件送达。
  public let testingRouteIDs: [String]
  public let routeGroups: [RouteGroup]
  public let downloadingRouteIDs: [String]
  public let downloadOperationID: String
  public let measurementError: ProductError?

  public init(
    snapshotVersion: UInt64,
    status: ProtectionStatus,
    routes: [NodeProfile],
    currentRoute: NodeProfile?,
    todayTraffic: TrafficSnapshot,
    settings: DaemonSettings,
    recentLogs: [ProductLog],
    pendingOperationID: String = "", degradedReason: ProductError? = nil,
    activeTLS: ActiveTLS? = nil, degradedReasons: [ProductError] = [], catalogRevision: UInt64 = 0,
    selectedRouteID: String = "", pendingTargetRouteID: String = "",
    subscriptions: [SubscriptionSummary] = [], testingRouteIDs: [String] = [],
    routeGroups: [RouteGroup] = [], downloadingRouteIDs: [String] = [],
    downloadOperationID: String = "", measurementError: ProductError? = nil,
    physicalEgress: PhysicalEgress? = nil, connectionSession: String = "",
    connectionGeneration: UInt64 = 0, exitPoolState: ExitPoolState? = nil,
    routeMeasurementProgress: [RouteMeasurementProgress] = [], latencyOperationID: String = "",
    testOperationIDs: [String: String] = [:], exitOperationIDs: [String: String] = [:],
    downloadQueue: DownloadQueueState? = nil
  ) {
    self.downloadQueue = downloadQueue
    self.testOperationIDs = testOperationIDs
    self.exitOperationIDs = exitOperationIDs
    self.latencyOperationID = latencyOperationID
    self.exitPoolState = exitPoolState
    self.routeMeasurementProgress = routeMeasurementProgress
    self.connectionSession = connectionSession
    self.connectionGeneration = connectionGeneration
    self.catalogRevision = catalogRevision
    self.physicalEgress = physicalEgress
    self.selectedRouteID = selectedRouteID
    self.pendingTargetRouteID = pendingTargetRouteID
    self.activeTLS = activeTLS
    self.degradedReasons = degradedReasons
    self.pendingOperationID = pendingOperationID
    self.degradedReason = degradedReason
    self.snapshotVersion = snapshotVersion
    self.status = status
    self.routes = routes
    self.currentRoute = currentRoute
    self.todayTraffic = todayTraffic
    self.settings = settings
    self.recentLogs = recentLogs
    self.subscriptions = subscriptions
    self.testingRouteIDs = testingRouteIDs
    self.routeGroups = routeGroups
    self.downloadingRouteIDs = downloadingRouteIDs
    self.downloadOperationID = downloadOperationID
    self.measurementError = measurementError
  }
}

/// 一次订阅刷新的结果计数；partial 表示本轮不是完整可信正文。
public struct RefreshReport: Sendable, Equatable {
  public let partial: Bool
  public let added: UInt32
  public let updated: UInt32
  public let hidden: UInt32
  public let kept: UInt32
  public let removed: UInt32

  public init(
    partial: Bool = false, added: UInt32 = 0, updated: UInt32 = 0, hidden: UInt32 = 0,
    kept: UInt32 = 0, removed: UInt32 = 0
  ) {
    self.partial = partial
    self.added = added
    self.updated = updated
    self.hidden = hidden
    self.kept = kept
    self.removed = removed
  }
}

/// 订阅来源的脱敏摘要：只有用户来源名与 hostname，没有 URL。
public struct SubscriptionSummary: Sendable, Equatable, Identifiable {
  public var lastAttempt: SubscriptionAttempt?
  public var hasRefreshChannel = false
  public var lastRouteID = ""
  public var userAgent = ""
  public let refreshOperationID: String
  public let id: String
  public let name: String
  public let host: String
  public let revision: UInt64
  public let routeCount: UInt32
  public let hiddenCount: UInt32
  /// 最近一次刷新的产品错误码；成功时为空。
  public let lastError: ProductError?
  public let lastRefresh: RefreshReport

  public init(
    id: String, name: String, host: String, revision: UInt64, routeCount: UInt32,
    hiddenCount: UInt32, lastError: ProductError? = nil,
    lastRefresh: RefreshReport = RefreshReport(), refreshOperationID: String = ""
  ) {
    self.id = id
    self.name = name
    self.host = host
    self.revision = revision
    self.routeCount = routeCount
    self.hiddenCount = hiddenCount
    self.lastError = lastError
    self.lastRefresh = lastRefresh
    self.refreshOperationID = refreshOperationID
  }
}

/// daemon 侧设置。"显示在程序坞"属 App 本地设置，不进 daemon。
public struct DaemonSettings: Sendable, Equatable {
  public var logRetentionDays: UInt32 = 1
  public var logRetentionHours: UInt32 = 24
  public var revision: UInt64 = 0
  public let autoConnect: Bool

  public init(autoConnect: Bool) {
    self.autoConnect = autoConnect
  }
}

/// 命令的最终结果（命令本身只立即拿到 ack）。
public struct RouteSaveResult: Sendable, Equatable {
  public let routeID: String
  public let created: Bool
  public init(routeID: String, created: Bool) {
    self.routeID = routeID
    self.created = created
  }
}

public struct OperationResult: Sendable, Equatable {
  public let operationID: String
  public let ok: Bool
  public let error: ProductError?

  public let routeSave: RouteSaveResult?

  public init(
    operationID: String, ok: Bool, error: ProductError?, routeSave: RouteSaveResult? = nil
  ) {
    self.routeSave = routeSave
    self.operationID = operationID
    self.ok = ok
    self.error = error
  }
}

/// 握手结果：一次连接的开场白。
public struct DaemonHandshake: Sendable, Equatable {
  public let ownerAppPID: UInt32
  public let initialInstall: Bool
  public let capabilities: Set<String>
  public let serviceFingerprint: String
  public let daemonVersion: String
  public let protocolVersion: UInt32
  public let snapshotSeq: UInt64

  public init(
    daemonVersion: String, protocolVersion: UInt32, snapshotSeq: UInt64,
    serviceFingerprint: String = "", capabilities: Set<String> = [], initialInstall: Bool = false,
    ownerAppPID: UInt32 = 0
  ) {
    self.ownerAppPID = ownerAppPID
    self.initialInstall = initialInstall
    self.capabilities = capabilities
    self.serviceFingerprint = serviceFingerprint
    self.daemonVersion = daemonVersion
    self.protocolVersion = protocolVersion
    self.snapshotSeq = snapshotSeq
  }
}

/// 保留产品事件顺序与快照边界；传输类型仅在 PaxClient 内使用。
public struct DaemonEventEnvelope: Sendable {
  public let seq: UInt64
  public let snapshotVersion: UInt64
  public let event: DaemonEvent

  public init(seq: UInt64, snapshotVersion: UInt64, event: DaemonEvent) {
    self.seq = seq
    self.snapshotVersion = snapshotVersion
    self.event = event
  }
}
