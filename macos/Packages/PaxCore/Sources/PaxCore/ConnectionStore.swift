import Foundation
import Observation
import PaxClient

/// 连接与保护状态的唯一状态源（architecture.md §13：视图只读 Store、发 Intent）。
///
/// 代理Intent、ACK与最终快照/结果在MainActor统一对账。
/// 断线时**不清空**状态——UI 保留最后快照并显示断开（§13）。
@MainActor
@Observable
public final class ConnectionStore {
  public private(set) var initialInstall: Bool?
  public enum Intent: Sendable {
    case setProxyEnabled(Bool)
    case reconnect
    case updateRouteTLS(RouteTLSIntent)
  }
  public enum ConnectionState: Sendable, Equatable {
    case disconnected
    case connected
    case connecting
    case retrying
    case failed
  }

  public private(set) var connection: ConnectionState = .disconnected
  public private(set) var clientError: ClientError?
  public internal(set) var currentPathCheck = CurrentPathCheck()
  public internal(set) var currentPathMessage = ""
  public private(set) var currentPathSupported = false
  let currentPathCommand: (@Sendable (String) async throws -> String)?
  var currentPathRequesting = false
  public private(set) var status: ProtectionStatus = .off
  public private(set) var pendingTargetRouteID = ""
  /// 仅 `.degraded` 时有值。
  public private(set) var degradedReason: ProductError?
  public let routesStore: RoutesStore?
  private var fallbackRoutes: [NodeProfile] = []
  public var routes: [NodeProfile] { routesStore?.routes ?? fallbackRoutes }
  public private(set) var activeTLS: ActiveTLS?
  public private(set) var degradedReasons: [ProductError] = []
  public private(set) var tlsSaveRouteID: String?
  public private(set) var tlsSaveError: String?
  public private(set) var tlsSaveMessage: String?
  public private(set) var tlsSaving = false
  private var tlsOperationIDs: Set<String> = []
  public private(set) var currentRoute: NodeProfile?
  public private(set) var todayTraffic: TrafficSnapshot = .zero
  public private(set) var recentLogs: [ProductLog] = []
  public private(set) var connectionLogs: [ProductLog] = []
  public private(set) var connectionSession = ""
  public private(set) var connectionGeneration: UInt64 = 0
  public private(set) var connectionLogTruncated = false
  /// 正在进行的操作；UI 据此显示「启动中」（收到 OperationResult 后清空）。
  public private(set) var pendingOperationID: String?

  public private(set) var serviceUpdating = false
  public func setServiceUpdating(_ updating: Bool) { serviceUpdating = updating }
  public func showUpdateWarning(_ error: ClientError?) { clientError = error }
  public private(set) var requesting = false
  private var enablingRequest = false
  private var pendingEnableID: String?
  @ObservationIgnored var feedbackTimeline = FeedbackTimeline()
  var dismissedFeedback: [String: ConnectionFeedback] = [:]
  var dismissedFeedbackAttempt: [String: UInt64] = [:]
  public private(set) var powerForwardingFailed = false
  public func setPowerForwardingFailed(_ failed: Bool) { powerForwardingFailed = failed }
  public private(set) var operationError: ProductError?
  private var lastProxyResultID: String?
  private var presentedConflictID: String?
  /// Shared pages claim each conflict once; browsing back does not repeat a dialog.
  public func claimConflictPrompt() -> ProductError? {
    guard let operationError,
      ["tun_conflict_other", "pax_tun_stale"].contains(operationError.code),
      let lastProxyResultID, presentedConflictID != lastProxyResultID
    else { return nil }
    presentedConflictID = lastProxyResultID
    return operationError
  }
  private let tlsCommand: (@Sendable (RouteTLSIntent) async throws -> String)?
  private let proxyCommand: (@Sendable (Bool) async throws -> String)?
  private let reconnectCommand: (@Sendable () async throws -> String)?
  private var completedIDs: [String] = []
  private var completedResults: [String: OperationResult] = [:]
  private var ownedOperationIDs: Set<String> = []
  private var snapshotVersion: UInt64 = 0
  /// Latest pushed AI usage version/generation/collection state (nil until first reported).
  public private(set) var aiUsage: AIUsageCollectionState?
  private var commandGeneration: UInt64 = 0
  private let logCapacity: Int
  public let trafficPathStore: TrafficPathStore?
  public private(set) var daemonVersion: String?

  public init(
    logCapacity: Int = 20, daemonVersion: String? = nil,
    proxyCommand: (@Sendable (Bool) async throws -> String)? = nil,
    tlsCommand: (@Sendable (RouteTLSIntent) async throws -> String)? = nil,
    routesStore: RoutesStore? = nil, trafficPathStore: TrafficPathStore? = nil,
    reconnectCommand: (@Sendable () async throws -> String)? = nil,
    currentPathCommand: (@Sendable (String) async throws -> String)? = nil
  ) {
    self.routesStore = routesStore
    self.trafficPathStore = trafficPathStore
    self.tlsCommand = tlsCommand
    self.proxyCommand = proxyCommand
    self.reconnectCommand = reconnectCommand
    self.currentPathCommand = currentPathCommand
    self.logCapacity = logCapacity
    self.daemonVersion = daemonVersion
  }

  /// 握手成功后：标记已连接并灌入第一份快照。
  public func markHandshaked(_ handshake: DaemonHandshake, snapshot: DaemonSnapshot) {
    initialInstall =
      handshake.capabilities.contains("onboarding.v1") ? handshake.initialInstall : false
    currentPathSupported = handshake.capabilities.contains("connection.current_path.v1")
    connection = .connected
    clientError = nil
    apply(.snapshot(snapshot))
    daemonVersion = handshake.daemonVersion
  }

  /// 连接断开：保留状态，只改连接标记。
  public func markDisconnected() {
    cancelCoreAuthorization()
    connection = .disconnected
    requesting = false
    enablingRequest = false
    pendingEnableID = nil
    commandGeneration &+= 1
    tlsSaveRouteID = nil
    tlsSaveError = nil
    tlsOperationIDs.removeAll()
    ownedOperationIDs.removeAll()
    completedResults.removeAll()
    completedIDs.removeAll()
    tlsSaving = false
    tlsSaveMessage = nil
  }

  /// Shared OFF-selection admission for Dashboard and Routes; snapshots remain authoritative.
  public var canSelectRoute: Bool {
    routesStore?.capabilities.contains("routes.select.off") == true
      && routesStore?.canWrite == true && connection == .connected
      && (status == .off
        || (status != .starting
          && routesStore?.capabilities.contains("routes.select.running") == true))
      && !serviceUpdating && !requesting && !tlsSaving && pendingOperationID == nil
  }

  public func selectRoute(_ id: String) async {
    guard canSelectRoute, let routesStore,
      let route = routesStore.routes.first(where: { $0.id == id && !$0.hidden })
    else { return }
    operationError = nil
    dismissedFeedback.removeValue(forKey: "proxy")
    dismissedFeedback.removeValue(forKey: "selection")
    await routesStore.perform(.select(route))
  }

  public var canOperate: Bool {
    connection == .connected && !requesting && !serviceUpdating && !tlsSaving && proxyCommand != nil
  }

  /// Both workbench pages use the same admission and authoritative Watch state.
  public var proxyEnabled: Bool {
    status != .off || proxyStarting || (needsServiceAuthorization && coreAuthorizationRoute != nil)
  }
  /// 快照里的第三方 TUN 观测；nil 表示没有。
  public private(set) var externalTUN: ExternalTUN?
  public var externalTUNActive: Bool {
    ["external_tun_active", "tun_taken_over"].contains(degradedReason?.code ?? "")
  }
  public var connectionTransitioning: Bool {
    ["network_recovering", "tun_releasing", "pax_cleanup_pending"].contains(
      degradedReason?.code ?? "")
  }
  public func canRequestProxy(_ enabled: Bool) -> Bool {
    if enabled && needsServiceAuthorization { return canToggleProxy }
    return canToggleProxy && (enabled != proxyEnabled || (!enabled && externalTUNActive))
  }
  public var proxyStarting: Bool {
    status == .starting || enablingRequest || pendingEnableID != nil
  }

  public var requestBackendRecovery: (@MainActor () -> Void)?
  public private(set) var backendRecoveryInProgress = false
  public func setBackendRecoveryInProgress(_ value: Bool) { backendRecoveryInProgress = value }
  public var requestAuthorization: (@MainActor () -> Void)?
  public var needsServiceAuthorization = false
  public private(set) var coreAuthorizationToken: UInt64 = 0
  private var coreAuthorizationRoute: NodeProfile?
  private var authorizationOperationID: String?

  /// A canceled or superseded ON can never be resumed by a late password result.
  public func cancelCoreAuthorization() {
    coreAuthorizationToken &+= 1
    coreAuthorizationRoute = nil
    authorizationOperationID = nil
    needsServiceAuthorization = false
    if operationError?.code == "core_required" { operationError = nil }
  }

  public func resumeProxyAfterAuthorization(token: UInt64) async {
    guard token == coreAuthorizationToken, needsServiceAuthorization else { return }
    guard coreAuthorizationRoute == routesStore?.selectedRoute, connection == .connected else {
      cancelCoreAuthorization()
      return
    }
    cancelCoreAuthorization()
    await setProxyFromWorkbench(true)
  }

  private func offerCoreAuthorization(_ result: OperationResult) {
    guard result.error?.code == "core_required",
      authorizationOperationID == result.operationID,
      routesStore?.capabilities.contains("proxy.core") == true,
      !needsServiceAuthorization
    else { return }
    coreAuthorizationToken &+= 1
    coreAuthorizationRoute = routesStore?.selectedRoute
    needsServiceAuthorization = true
    requestAuthorization?()
  }
  public var canToggleProxy: Bool {
    if needsServiceAuthorization { return !serviceUpdating }
    if enablingRequest {
      return connection == .connected && !serviceUpdating && !tlsSaving
    }
    return canOperate && routesStore?.capabilities.contains("proxy.power") == true
      && (proxyEnabled
        || (pendingOperationID == nil && (externalTUNActive || routesStore?.selectedRoute != nil)))
  }
  public var canReconnect: Bool {
    canOperate && reconnectCommand != nil && !needsServiceAuthorization
      && pendingOperationID == nil && status != .starting
      && routesStore?.capabilities.contains("proxy.reconnect") == true
  }
  public func reconnectProxy() async {
    guard canReconnect else { return }
    await perform(.reconnect)
  }
  public var proxyConfirmationRoute: NodeProfile? {
    guard !needsServiceAuthorization, !proxyEnabled, let route = routesStore?.selectedRoute,
      route.security == "tls", route.tlsPolicy?.needsConfirmation == true
    else { return nil }
    return route
  }
  public var proxyMessage: String {
    if connection != .connected { return clientError?.message ?? "后台服务未连接" }
    if let operationError { return ProductErrorPresentation.message(operationError) }
    if let degradedReason {
      if status == .degraded && activeTLS?.skipped == true {
        let others = degradedReasons.filter { $0.code != "tls_unverified" }
        let suffix =
          others.isEmpty && degradedReason.code != "tls_unverified"
          ? " · " + ProductErrorPresentation.message(degradedReason)
          : others.map { " · " + ProductErrorPresentation.message($0) }.joined()
        return "代理已连接 · 证书未校验" + suffix
      }
      return ProductErrorPresentation.message(degradedReason)
    }
    if let clientError { return clientError.message }
    if proxyStarting { return "连接中，可选择直连取消" }
    if status == .protected { return "代理已开启" }
    if proxyConfirmationRoute != nil { return "开启前请确认线路证书设置" }
    return routesStore?.selectedRoute == nil ? "请先选择线路" : "代理已关闭"
  }
  public func setProxyFromWorkbench(_ enabled: Bool) async {
    if !enabled && needsServiceAuthorization {
      cancelCoreAuthorization()
      await perform(.setProxyEnabled(false))
      return
    }
    if enabled && needsServiceAuthorization {
      requestAuthorization?()
      return
    }
    guard canRequestProxy(enabled),
      !enabled || proxyConfirmationRoute == nil
    else { return }
    routesStore?.clearSelectionFeedback()
    await perform(.setProxyEnabled(enabled))
  }

  public var canEditTLS: Bool {
    connection == .connected && !requesting && !serviceUpdating && !tlsSaving
      && pendingOperationID == nil
      && status != .starting && tlsCommand != nil
  }

  /// Intent follows the daemon ACK/result contract, without predicting proxy state.
  public func perform(_ intent: Intent) async {
    let priorityStop: Bool
    if case .setProxyEnabled(false) = intent {
      cancelCoreAuthorization()
      priorityStop = enablingRequest
    } else {
      priorityStop = false
    }
    guard connection == .connected, !requesting || priorityStop, !serviceUpdating else { return }
    let command: @Sendable () async throws -> String
    var savingTLS = false
    switch intent {
    case .reconnect:
      guard canReconnect, let reconnectCommand else { return }
      command = reconnectCommand
    case .setProxyEnabled(let enabled):
      guard canOperate || priorityStop, let proxyCommand else { return }
      command = { try await proxyCommand(enabled) }
    case .updateRouteTLS(let request):
      guard canEditTLS, let tlsCommand else { return }
      command = { try await tlsCommand(request) }
      savingTLS = true
      tlsSaveRouteID = request.routeID
      tlsSaveError = nil
    }
    requesting = true
    if case .setProxyEnabled(false) = intent { pendingEnableID = nil }
    if case .setProxyEnabled(let enabled) = intent {
      enablingRequest = enabled
    } else if case .reconnect = intent {
      enablingRequest = true
    } else {
      enablingRequest = false
    }
    if savingTLS { tlsSaving = true }
    tlsSaveMessage = nil
    operationError = nil
    dismissedFeedback.removeValue(forKey: "proxy")
    dismissedFeedback.removeValue(forKey: "selection")
    let before = snapshotVersion
    clientError = nil
    commandGeneration &+= 1
    let token = commandGeneration
    defer {
      if token == commandGeneration {
        requesting = false
        enablingRequest = false
      }
    }
    do {
      let id = try await command()
      guard token == commandGeneration, connection == .connected else { return }
      if case .setProxyEnabled(true) = intent { authorizationOperationID = id }
      if let result = completedResults[id] {
        lastProxyResultID = id
        operationError = result.ok ? nil : result.error
        offerCoreAuthorization(result)
      } else {
        ownedOperationIDs.insert(id)
      }
      if savingTLS && !completedIDs.contains(id) { tlsOperationIDs.insert(id) }
      if savingTLS && completedIDs.contains(id) {
        tlsSaving = false
        tlsSaveError = completedResults[id]?.error.map(ProductErrorPresentation.message)
      }
      if savingTLS && completedIDs.contains(id) && operationError == nil {
        tlsSaveMessage = status == .off ? "已保存，下次开启生效" : "已保存，关闭再开启以应用"
        tlsOperationIDs.remove(id)
      }
      let reconciled = snapshotVersion > before && pendingOperationID == nil
      if savingTLS && reconciled {
        tlsOperationIDs.remove(id)
        tlsSaving = false
      }
      if !completedIDs.contains(id) && !reconciled {
        pendingOperationID = id
        if case .setProxyEnabled(true) = intent, status == .off { pendingEnableID = id }
        if case .reconnect = intent { pendingEnableID = id }
      }
    } catch {
      guard token == commandGeneration else { return }
      if savingTLS {
        tlsSaving = false
        tlsSaveError = ProductErrorPresentation.display(error)
      }
      clientError = error as? ClientError ?? .unknown
    }
  }

  /// 会话有序更新，业务数据只由快照和事件改变。
  public func consume(_ update: ConnectionUpdate) {
    routesStore?.consume(update)
    defer {
      let current = unresolvedFeedback
      dismissedFeedback = dismissedFeedback.filter { _, issue in current.contains(issue) }
    }
    switch update {
    case .connecting:
      connection = .connecting
    case .ready(let handshake, let snapshot):
      markHandshaked(handshake, snapshot: snapshot)
    case .events(let events):
      for event in events { apply(event) }
    case .retrying(let error, _):
      markDisconnected()
      connection = .retrying
      clientError = error
    case .failed(let error):
      markDisconnected()
      connection = .failed
      clientError = error
    case .disconnected:
      markDisconnected()
    }
  }

  public func apply(_ event: DaemonEvent) {
    switch event {
    case .historyDelta(let delta):
      if let traffic = delta.traffic, todayTraffic != traffic { todayTraffic = traffic }
      if delta.replaceRecentLogs {
        connectionLogs = Array(delta.recentLogs.filter { $0.connection != nil }.suffix(500))
        recentLogs = Array(delta.recentLogs.filter { $0.connection == nil }.suffix(logCapacity))
      }
    case .currentPathCheck(let check):
      currentPathCheck = check
    case .snapshot(let snapshot):
      currentPathCheck = snapshot.currentPathCheck
      if status != snapshot.status { tlsSaveMessage = nil }
      status = snapshot.status
      pendingTargetRouteID = snapshot.pendingTargetRouteID
      degradedReason = snapshot.degradedReason
      externalTUN = snapshot.externalTUN
      snapshotVersion = snapshot.snapshotVersion
      pendingOperationID = snapshot.pendingOperationID.isEmpty ? nil : snapshot.pendingOperationID
      if let pendingEnableID,
        pendingOperationID != pendingEnableID || status == .protected || status == .degraded
      {
        self.pendingEnableID = nil
      }
      if routes.map({ $0.tlsPolicy }) != snapshot.routes.map({ $0.tlsPolicy }) {
        tlsSaveMessage = nil
      }
      if routesStore == nil { fallbackRoutes = snapshot.routes }
      activeTLS = snapshot.activeTLS
      degradedReasons = snapshot.degradedReasons
      if let state = snapshot.aiUsage {
        aiUsage = state
      }
      currentRoute = snapshot.currentRoute
      todayTraffic = snapshot.todayTraffic
      connectionSession = snapshot.connectionSession
      connectionGeneration = snapshot.connectionGeneration
      connectionLogs = Array(snapshot.recentLogs.filter { $0.connection != nil }.suffix(500))
      connectionLogTruncated = snapshot.recentLogs.count >= 500
      recentLogs = Array(snapshot.recentLogs.filter { $0.connection == nil }.suffix(logCapacity))
    case .operation(let result):
      if pendingEnableID == result.operationID { pendingEnableID = nil }
      if completedResults[result.operationID] == nil { completedIDs.append(result.operationID) }
      completedResults[result.operationID] = result
      if completedIDs.count > 64 {
        let removed = completedIDs.removeFirst()
        completedResults.removeValue(forKey: removed)
      }
      if ownedOperationIDs.remove(result.operationID) != nil {
        lastProxyResultID = result.operationID
        operationError = result.ok ? nil : result.error
        offerCoreAuthorization(result)
      }
      if tlsOperationIDs.remove(result.operationID) != nil {
        tlsSaving = false
        tlsSaveError = result.error.map(ProductErrorPresentation.message)
        if result.ok {
          tlsSaveMessage = status == .off ? "已保存，下次开启生效" : "已保存，关闭再开启以应用"
        } else {
          tlsSaveMessage = nil
        }
      }
      if result.operationID == pendingOperationID {
        pendingOperationID = nil
      }
    case .aiUsage(let state):
      aiUsage = state
    }
  }
}
