import PaxClient

// 领域模型的对外出口。
//
// 模型类型定义在 PaxClient（理由见其模块说明：映射只发生在 PaxClient，而依赖方向是 PaxCore → PaxClient）。
// 上层（Features / App target）只 import PaxCore 即可拿到这些类型，不必直接依赖 PaxClient —— 这样
// 「哪些模块认识传输层」在编译期就是明确的。
public typealias NodeProfile = PaxClient.NodeProfile
public typealias ProtectionStatus = PaxClient.ProtectionStatus
public typealias TrafficSnapshot = PaxClient.TrafficSnapshot
public typealias ProductLog = PaxClient.ProductLog
public typealias ProductError = PaxClient.ProductError
public typealias LogLevel = PaxClient.LogLevel
public typealias DaemonEvent = PaxClient.DaemonEvent
public typealias DaemonSnapshot = PaxClient.DaemonSnapshot
public typealias DaemonSettings = PaxClient.DaemonSettings
public typealias OperationResult = PaxClient.OperationResult
public typealias DaemonHandshake = PaxClient.DaemonHandshake
public typealias DaemonConnection = PaxClient.DaemonConnection

public typealias ConnectionSession = PaxClient.ConnectionSession
public typealias ConnectionUpdate = PaxClient.ConnectionUpdate
public typealias ClientError = PaxClient.ClientError

public typealias TLSMode = PaxClient.TLSMode
public typealias RouteTLSPolicy = PaxClient.RouteTLSPolicy
public typealias ActiveTLS = PaxClient.ActiveTLS
public typealias RouteTLSIntent = PaxClient.RouteTLSIntent

public typealias RoutesPreview = PaxClient.RoutesPreview
public typealias RoutePreviewItem = PaxClient.RoutePreviewItem
public typealias RouteEdit = PaxClient.RouteEdit
public typealias SubscriptionSummary = PaxClient.SubscriptionSummary
public typealias RefreshReport = PaxClient.RefreshReport
public typealias DownloadTestSummary = PaxClient.DownloadTestSummary
public typealias RouteExitSummary = PaxClient.RouteExitSummary
public typealias RouteTestSummary = PaxClient.RouteTestSummary
public typealias RouteGroup = PaxClient.RouteGroup
public typealias RouteGroupsEdit = PaxClient.RouteGroupsEdit

public typealias PhysicalEgress = PaxClient.PhysicalEgress
public typealias TrafficPathFinding = PaxClient.TrafficPathFinding
public typealias ConnectionDiagnosticStep = PaxClient.TrafficPathDiagnostics.Step
public typealias TrafficPathDiagnostics = PaxClient.TrafficPathDiagnostics
public typealias MeasurementPreferences = PaxClient.MeasurementPreferences
public typealias MeasurementRequest = PaxClient.MeasurementRequest
public typealias MeasurementReply = PaxClient.MeasurementReply
public typealias CFCandidate = PaxClient.CFCandidate

public typealias CFRecord = PaxClient.CFRecord
public typealias CFSample = PaxClient.CFSample
public typealias CFWorkspace = PaxClient.CFWorkspace
public typealias CFWorkspaceQuery = PaxClient.CFWorkspaceQuery

public typealias ConnectionEvent = PaxClient.ConnectionEvent

public typealias ChainEdit = PaxClient.ChainEdit
public typealias ChainRoute = PaxClient.ChainRoute
public typealias RouteHistorySample = PaxClient.RouteHistorySample
public typealias RouteHistoryPage = PaxClient.RouteHistoryPage
public typealias RouteUsagePage = PaxClient.RouteUsagePage
public typealias RouteUsageBucket = PaxClient.RouteUsageBucket

public typealias ExitLocation = PaxClient.ExitLocation

public typealias ExitPoolState = PaxClient.ExitPoolState
public typealias RouteMeasurementProgress = PaxClient.RouteMeasurementProgress
public typealias RouteMeasurementPhase = PaxClient.RouteMeasurementPhase

public typealias DownloadQueueState = PaxClient.DownloadQueueState

public typealias CFIPLocation = PaxClient.CFIPLocation

public typealias HistoryState = PaxClient.HistoryState
public typealias LogFilter = PaxClient.LogFilter
public typealias HistoryLogPage = PaxClient.HistoryLogPage
public typealias HistoryObject = PaxClient.HistoryObject
public typealias HistoryObjectPage = PaxClient.HistoryObjectPage
public typealias FlowFact = PaxClient.FlowFact
public typealias DNSFact = PaxClient.DNSFact
public typealias DNSTrafficGroup = PaxClient.DNSTrafficGroup
public typealias HistoryLogDetail = PaxClient.HistoryLogDetail
public typealias TrafficFilter = PaxClient.TrafficFilter
public typealias TrafficExit = PaxClient.TrafficExit
public typealias TrafficRow = PaxClient.TrafficRow
public typealias TrafficRoutingMatch = PaxClient.TrafficRoutingMatch
public typealias TrafficPoint = PaxClient.TrafficPoint
public typealias TrafficPage = PaxClient.TrafficPage
public typealias TrafficBasis = PaxClient.TrafficBasis
public typealias TrafficDetail = PaxClient.TrafficDetail
public typealias TrafficDetailQuery = PaxClient.TrafficDetailQuery

public typealias TrafficRelated = PaxClient.TrafficRelated
public typealias TrafficDimension = PaxClient.TrafficDimension

public typealias CurrentPathCheck = PaxClient.CurrentPathCheck
public typealias HistoryDelta = PaxClient.HistoryDelta
public typealias LogUpdateRequest = PaxClient.LogUpdateRequest
public typealias LogUpdatePage = PaxClient.LogUpdatePage

public typealias LogDetailFilter = PaxClient.LogDetailFilter

public typealias LogRecord = PaxClient.LogRecord
public typealias ConnectionMetrics = PaxClient.ConnectionMetrics
public typealias ObjectIconResult = PaxClient.ObjectIconResult
public typealias RoutingRule = PaxClient.RoutingRule
public typealias RoutingRulesSnapshot = PaxClient.RoutingRulesSnapshot

public typealias RoutingLibrarySnapshot = PaxClient.RoutingLibrarySnapshot
public typealias RoutingPackage = PaxClient.RoutingPackage
public typealias RoutingEntry = PaxClient.RoutingEntry
public typealias RoutingApplication = PaxClient.RoutingApplication
public typealias RoutingMutation = PaxClient.RoutingMutation

public typealias TrafficLatencySample = PaxClient.TrafficLatencySample
public typealias TrafficResponsePoint = PaxClient.TrafficResponsePoint

public typealias GeoRulesPage = PaxClient.GeoRulesPage

public typealias LogCriteria = PaxClient.LogCriteria
public typealias LogOptions = PaxClient.LogOptions

public typealias SystemEvent = PaxClient.SystemEvent
public typealias EventSection = PaxClient.EventSection
public typealias EventField = PaxClient.EventField

public typealias NetworkPrivacy = PaxClient.NetworkPrivacy

public typealias AIServiceSource = PaxClient.AIServiceSource
public typealias AIClient = PaxClient.AIClient
public typealias AIProfile = PaxClient.AIProfile
public typealias AIBinding = PaxClient.AIBinding
public typealias AIProfilesWorkspace = PaxClient.AIProfilesWorkspace
public typealias AIProfilePlan = PaxClient.AIProfilePlan

public typealias AIProviderModel = PaxClient.AIProviderModel

public typealias AIUsageRequest = PaxClient.AIUsageRequest
public typealias AIUsageBucket = PaxClient.AIUsageBucket
public typealias AIUsageTotals = PaxClient.AIUsageTotals
public typealias AIUsageFilter = PaxClient.AIUsageFilter
public typealias AIUsageLogPage = PaxClient.AIUsageLogPage
public typealias AIUsageCollectionState = PaxClient.AIUsageCollectionState

public typealias AISessionKey = PaxClient.AISessionKey
public typealias AISessionCatalogEntry = PaxClient.AISessionCatalogEntry
public typealias AISessionCatalogPage = PaxClient.AISessionCatalogPage
public typealias AISessionRow = PaxClient.AISessionRow
public typealias AISessionAnalysis = PaxClient.AISessionAnalysis
public typealias AISessionContextUsage = PaxClient.AISessionContextUsage
public typealias AISessionContextBand = PaxClient.AISessionContextBand

public typealias NetworkDiagnosticReport = PaxClient.NetworkDiagnosticReport
public typealias NetworkDiagnosticProgress = PaxClient.NetworkDiagnosticProgress
public typealias NetworkDiagnosticSource = PaxClient.NetworkDiagnosticReport.Source
public typealias NetworkDiagnosticFact = PaxClient.NetworkDiagnosticReport.Fact
public typealias NetworkDiagnosticProbe = PaxClient.NetworkDiagnosticReport.Probe
public typealias EnvironmentDiagnosticReport = PaxClient.EnvironmentDiagnosticReport
public typealias EnvironmentDiagnosticSample = PaxClient.EnvironmentDiagnosticReport.Sample
