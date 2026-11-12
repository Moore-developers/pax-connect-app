import Foundation
import PaxProto

// proto → 领域模型。映射只发生在这里（code-standards.md §二）：
// 上层拿到的一律是 PaxClient 的模型类型，不出现任何 Pax_V1_* 类型。

extension ProtectionStatus {
  init(proto: Pax_V1_ProtectionStatus) {
    switch proto {
    case .off: self = .off
    case .starting: self = .starting
    case .protected: self = .protected
    case .degraded: self = .degraded
    case .unspecified, .UNRECOGNIZED: self = .off
    }
  }
}

extension LogLevel {
  init(proto: Pax_V1_LogLevel) {
    switch proto {
    case .info: self = .info
    case .warning: self = .warning
    case .error: self = .error
    case .unspecified, .UNRECOGNIZED: self = .info
    }
  }
}

extension NodeProfile {
  init(proto: Pax_V1_NodeProfile) {
    self.init(
      id: proto.id,
      name: proto.name,
      protocolName: proto.protocolName,
      server: proto.server,
      port: proto.port,
      group: proto.group, serverName: proto.serverName,
      tlsPolicy: proto.hasTlsPolicy ? RouteTLSPolicy(proto: proto.tlsPolicy) : nil,
      revision: proto.revision, transport: proto.transport, security: proto.security,
      sourceID: proto.sourceID, origin: proto.origin, hidden: proto.hidden,
      sourceRemoved: proto.sourceRemoved,
      testSummary: proto.hasTestSummary ? RouteTestSummary(proto: proto.testSummary) : nil,
      configRevision: proto.configRevision,
      downloadSummary: proto.hasDownloadSummary
        ? DownloadTestSummary(proto: proto.downloadSummary) : nil,
      exitSummary: proto.hasExitSummary ? RouteExitSummary(proto: proto.exitSummary) : nil,
      cfEntry: proto.cfEntry,
      chain: proto.hasChain
        ? ChainRoute(frontID: proto.chain.frontID, finalID: proto.chain.finalID) : nil)
  }
}

extension DownloadTestSummary {
  init(proto: Pax_V1_DownloadTestSummary) {
    self.init(
      at: proto.hasAt ? proto.at.date : Date(timeIntervalSince1970: 0), success: proto.success,
      bytesPerSecond: proto.bytesPerSecond,
      error: proto.hasError ? ProductError(proto: proto.error) : nil,
      testedConfigRevision: proto.testedConfigRevision, bytes: proto.bytes, method: proto.method,
      parallel: proto.parallel, setupMS: proto.setupMs, endReason: proto.endReason,
      durationMS: proto.durationMs)
  }
}

extension RouteTestSummary {
  init(proto: Pax_V1_RouteTestSummary) {
    self.init(
      at: proto.hasAt ? proto.at.date : Date(timeIntervalSince1970: 0), success: proto.success,
      durationMS: proto.durationMs,
      error: proto.hasError ? ProductError(proto: proto.error) : nil,
      testedRevision: proto.testedRevision, testedConfigRevision: proto.testedConfigRevision,
      exitIP: proto.exitIp,
      exitError: proto.hasExitError ? ProductError(proto: proto.exitError) : nil,
      coldMS: proto.coldMs, secondMS: proto.secondMs, reused: proto.reused, method: proto.method,
      firstError: proto.firstError, secondError: proto.secondError, stableMS: proto.stableMs,
      stableSamples: proto.stableSamples, stableError: proto.stableError,
      testTarget: proto.testTarget,
      exitLocation: proto.hasExitLocation ? ExitLocation(proto: proto.exitLocation) : nil,
      lastExitLocation: proto.hasLastExitLocation
        ? ExitLocation(proto: proto.lastExitLocation) : nil)
  }
}

extension RefreshReport {
  init(proto: Pax_V1_RefreshReport) {
    self.init(
      partial: proto.partial, added: proto.added, updated: proto.updated, hidden: proto.hidden,
      kept: proto.kept, removed: proto.removed)
  }
}

extension SubscriptionSummary {
  init(proto: Pax_V1_SubscriptionSummary) {
    self.init(
      id: proto.id, name: proto.name, host: proto.host, revision: proto.revision,
      routeCount: proto.routeCount, hiddenCount: proto.hiddenCount,
      lastError: proto.hasLastError ? ProductError(proto: proto.lastError) : nil,
      lastRefresh: RefreshReport(proto: proto.lastRefresh),
      refreshOperationID: proto.refreshOperationID)
    lastAttempt = proto.hasLastAttempt ? SubscriptionAttempt(proto: proto.lastAttempt) : nil
    hasRefreshChannel = proto.hasRefreshChannel_p
    lastRouteID = proto.lastRouteID
    userAgent = proto.userAgent
  }
}

extension TrafficSnapshot {
  init(proto: Pax_V1_TrafficSnapshot) {
    self.init(
      uploadBytes: proto.uploadBytes,
      downloadBytes: proto.downloadBytes,
      proxiedBytes: proto.proxiedBytes,
      directBytes: proto.directBytes,
      unclassifiedBytes: proto.unclassifiedBytes,
      activeConnections: proto.activeConnections
    )
    asOf = proto.hasAsOf ? proto.asOf.date : nil
    activeConnectionsKnown = proto.activeConnectionsKnown
    partial = proto.partial
    collectionState = proto.collectionState
  }
}

extension ProductError {
  init(proto: Pax_V1_ProductError) {
    self.init(
      code: proto.code, messageParams: proto.messageParams, affectedTarget: proto.affectedTarget)
  }
}

extension ProductLog {
  init(proto: Pax_V1_ProductLog) {
    self.init(
      at: proto.hasAt ? proto.at.date : Date(timeIntervalSince1970: 0),
      level: LogLevel(proto: proto.level),
      message: proto.message,
      operationID: proto.operationID,
      snapshotVersion: proto.snapshotVersion,
      connection: proto.hasConnection ? ConnectionEvent(proto.connection) : nil
    )
    flow = proto.hasFlow ? FlowFact(proto.flow) : nil
    recordID = proto.recordID
    problemID = proto.problemID
    occurrences = proto.occurrences
    relatedConnections = proto.relatedConnections
    firstAt = proto.hasFirstAt ? proto.firstAt.date : nil
    lastAt = proto.hasLastAt ? proto.lastAt.date : nil
    category = proto.category
    eventID = proto.eventID
    eventKind = proto.eventKind
    source = proto.source
    connectionKey = proto.connectionKey
    applicationID = proto.applicationID
    application = proto.application
    domain = proto.domain
    ip = proto.ip
    routeID = proto.routeID
    routeName = proto.routeName
    direction = proto.direction
    result = proto.result
  }
}

extension DaemonSettings {
  init(proto: Pax_V1_Settings) {
    self.init(autoConnect: proto.autoConnect)
    logRetentionDays = proto.logRetentionDays
    logRetentionHours =
      [1, 12, 24].contains(proto.logRetentionHours) ? proto.logRetentionHours : 24
    revision = proto.revision
  }
}

extension OperationResult {
  init(proto: Pax_V1_OperationResult) {
    self.init(
      operationID: proto.operationID,
      ok: proto.ok,
      error: proto.hasError ? ProductError(proto: proto.error) : nil,
      routeSave: proto.hasRouteSave
        ? RouteSaveResult(routeID: proto.routeSave.routeID, created: proto.routeSave.created) : nil
    )
  }
}

extension DaemonSnapshot {
  init(proto: Pax_V1_Snapshot) {
    self.init(
      snapshotVersion: proto.snapshotVersion,
      status: ProtectionStatus(proto: proto.status),
      routes: proto.routes.map(NodeProfile.init(proto:)),
      currentRoute: proto.hasCurrentRoute ? NodeProfile(proto: proto.currentRoute) : nil,
      todayTraffic: proto.hasTodayTraffic ? TrafficSnapshot(proto: proto.todayTraffic) : .zero,
      settings: DaemonSettings(proto: proto.settings),
      recentLogs: proto.recentLogs.map(ProductLog.init(proto:)),
      pendingOperationID: proto.pendingOperationID,
      degradedReason: proto.hasDegradedReason ? ProductError(proto: proto.degradedReason) : nil,
      activeTLS: proto.hasActiveTls ? ActiveTLS(proto: proto.activeTls) : nil,
      degradedReasons: proto.degradedReasons.map(ProductError.init(proto:)),
      catalogRevision: proto.catalogRevision, selectedRouteID: proto.selectedRouteID,
      pendingTargetRouteID: proto.pendingTargetRouteID,
      subscriptions: proto.subscriptions.map(SubscriptionSummary.init(proto:)),
      testingRouteIDs: proto.testingRouteIds,
      routeGroups: proto.routeGroups.map {
        RouteGroup(id: $0.id, name: $0.name, memberIDs: $0.memberIds)
      }, downloadingRouteIDs: proto.downloadingRouteIds,
      downloadOperationID: proto.downloadOperationID,
      measurementError: proto.hasMeasurementError
        ? ProductError(proto: proto.measurementError) : nil,
      physicalEgress: proto.hasPhysicalEgress ? PhysicalEgress(proto: proto.physicalEgress) : nil,
      connectionSession: proto.connectionSession, connectionGeneration: proto.connectionGeneration,
      exitPoolState: proto.hasExitPoolState ? ExitPoolState(proto: proto.exitPoolState) : nil,
      routeMeasurementProgress: proto.routeMeasurementProgress.map(
        RouteMeasurementProgress.init(proto:)), latencyOperationID: proto.latencyOperationID,
      testOperationIDs: proto.testOperationIds, exitOperationIDs: proto.exitOperationIds,
      downloadQueue: proto.hasDownloadQueue ? DownloadQueueState(proto: proto.downloadQueue) : nil
    )
    history = HistoryState(proto.history)
    currentPathCheck = CurrentPathCheck(proto.currentPathCheck)
    aiUsage = proto.hasAiUsage ? AIUsageCollectionState(proto.aiUsage) : nil
    externalTUN = proto.hasExternalTun ? ExternalTUN(proto: proto.externalTun) : nil
  }
}

extension DaemonEvent {
  /// 把一条 `WatchEvent` 映射成领域事件；payload 为空的事件由调用方丢弃（返回 nil）。
  init?(proto: Pax_V1_WatchEvent) {
    switch proto.payload {
    case .historyDelta(let v): self = .historyDelta(HistoryDelta(v))
    case .currentPathCheck(let v): self = .currentPathCheck(CurrentPathCheck(v))
    case .snapshot(let snapshot):
      self = .snapshot(DaemonSnapshot(proto: snapshot))
    case .operation(let operation):
      self = .operation(OperationResult(proto: operation))
    case .aiUsage(let state):
      self = .aiUsage(AIUsageCollectionState(state))
    case nil:
      return nil
    }
  }
}

extension TLSMode {
  init(proto: Pax_V1_TLSMode) {
    switch proto {
    case .strict: self = .strict
    case .skipVerify: self = .skipVerify
    default: self = .unknown
    }
  }
}
extension RouteTLSPolicy {
  init(proto: Pax_V1_RouteTLSPolicy) {
    self.init(
      mode: TLSMode(proto: proto.mode), userOverride: proto.userOverride,
      needsConfirmation: proto.needsConfirmation, identityToken: proto.identityToken,
      revision: proto.policyRevision)
  }
}
extension ActiveTLS {
  init(proto: Pax_V1_ActiveTLS) {
    self.init(
      routeID: proto.routeID, identityToken: proto.identityToken, mode: TLSMode(proto: proto.mode),
      verified: proto.verified, skipped: proto.skipped)
  }
}

extension RoutesPreview {
  init(proto: Pax_V1_RoutesPreview) {
    self.init(
      token: proto.token, revision: proto.catalogRevision,
      items: proto.items.map {
        RoutePreviewItem(
          id: $0.itemID, route: $0.hasRoute ? NodeProfile(proto: $0.route) : nil,
          errorCode: $0.errorCode, duplicate: $0.duplicate, line: $0.line, hidden: $0.hidden,
          displayName: $0.displayName, protocolName: $0.protocolName)
      }, sourceHost: proto.sourceHost, sourceName: proto.sourceName,
      subscriptionID: proto.subscriptionID)
  }
}
extension RouteEdit {
  init(proto: Pax_V1_RouteEditor) {
    self.init()
    documentJSON = proto.documentJson
    configRevision = proto.configRevision
    let p = proto.fields
    routeID = p.routeID
    revision = p.expectedRevision
    name = p.name
    protocolName = p.protocol
    server = p.server
    port = p.port
    transport = p.transport
    security = p.security
    serverName = p.serverName
    wsHost = p.wsHost
    wsPath = p.wsPath
    alpn = p.alpn
    fingerprint = p.fingerprint
    realityPublicKey = p.realityPublicKey
    realityShortID = p.realityShortID
    flow = p.flow
    alterID = p.alterID
    vmessSecurity = p.vmessSecurity
    skipSuggested = p.tlsSkipSuggested
    credentialsSet = proto.credentialsSet
  }
  var proto: Pax_V1_RouteEditRequest {
    .with {
      $0.routeID = routeID
      $0.expectedRevision = revision
      $0.requestID = requestID
      if !documentJSON.isEmpty {
        $0.documentJson = documentJSON
        $0.name = name
        return
      }
      $0.name = name
      $0.protocol = protocolName
      $0.server = server
      $0.port = port
      $0.transport = transport
      $0.security = security
      $0.serverName = serverName
      $0.uuid = uuid
      $0.password = password
      $0.replaceCredentials = replaceCredentials
      $0.wsHost = wsHost
      $0.wsPath = wsPath
      $0.alpn = alpn
      $0.fingerprint = fingerprint
      $0.realityPublicKey = realityPublicKey
      $0.realityShortID = realityShortID
      $0.flow = flow
      $0.alterID = alterID
      $0.vmessSecurity = vmessSecurity
      $0.obfsPassword = obfsPassword
      $0.tlsSkipSuggested = skipSuggested
    }
  }
}

extension PhysicalEgress {
  init(proto: Pax_V1_PhysicalEgress) {
    self.init(
      network: proto.network, ipv4: proto.ipv4, ipv6: proto.ipv6, phase: proto.phase,
      at: proto.hasAt ? proto.at.date : nil,
      error: proto.hasError ? ProductError(proto: proto.error) : nil)
  }
}
extension CFCandidate {
  init(proto: Pax_V1_CFCandidate) {
    self.init(
      ip: proto.ip, source: proto.source, latencyMS: proto.latencyMs, network: proto.network,
      at: proto.hasAt ? proto.at.date : nil,
      error: proto.hasError ? ProductError(proto: proto.error) : nil)
  }
}
extension MeasurementPreferences {
  init(proto: Pax_V1_MeasurementPreferences) {
    self.init()
    exitPoolState = proto.hasExitPoolState ? ExitPoolState(proto: proto.exitPoolState) : nil
    revision = proto.revision
    latency = proto.latencyParallel
    exit = proto.exitParallel
    download = proto.downloadParallel
    mode = proto.mode
    latencyMode = proto.latencyMode.isEmpty ? proto.mode : proto.latencyMode
    exitMode = proto.exitMode.isEmpty ? proto.mode : proto.exitMode
    downloadMode = proto.downloadMode.isEmpty ? proto.mode : proto.downloadMode
    recommendedLatency = proto.recommendedLatency
    recommendedExit = proto.recommendedExit
    recommendedDownload = proto.recommendedDownload
    network = proto.recommendationNetwork
    evaluatedAt = proto.hasEvaluatedAt ? proto.evaluatedAt.date : nil
  }
  var proto: Pax_V1_MeasurementPreferences {
    .with {
      $0.revision = revision
      $0.latencyParallel = latency
      $0.exitParallel = exit
      $0.downloadParallel = download
      $0.mode = mode
      $0.latencyMode = latencyMode
      $0.exitMode = exitMode
      $0.downloadMode = downloadMode
    }
  }
}

extension ExitLocation {
  init(proto: Pax_V1_ExitLocation) {
    self.init(
      ip: proto.ip, countryCode: proto.countryCode, country: proto.country, region: proto.region,
      city: proto.city, isp: proto.isp, asn: proto.asn, source: proto.source,
      geoSource: proto.geoSource, geoError: proto.geoError,
      at: proto.hasAt ? proto.at.date : Date(timeIntervalSince1970: 0),
      geoAt: proto.hasGeoAt ? proto.geoAt.date : nil, network: proto.network,
      configRevision: proto.configRevision)
  }
}

extension ExitPoolState {
  init(proto: Pax_V1_ExitPoolState) {
    self.init(
      configuredLimit: proto.configuredLimit, inFlight: proto.inFlight, queued: proto.queued,
      retrying: proto.retrying, availableProviders: proto.availableProviders,
      totalProviders: proto.totalProviders, at: proto.hasAt ? proto.at.date : nil)
  }
}
extension RouteMeasurementProgress {
  init(proto: Pax_V1_RouteMeasurementProgress) {
    let phase: RouteMeasurementPhase
    switch proto.phase {
    case .latency: phase = .latency
    case .exitQueued: phase = .exitQueued
    case .exitRunning: phase = .exitRunning
    case .exitRetrying: phase = .exitRetrying
    default: phase = .unknown
    }
    self.init(
      routeID: proto.routeID, operationID: proto.operationID,
      testedConfigRevision: proto.testedConfigRevision, network: proto.network, phase: phase,
      latencyResult: proto.hasLatencyResult ? RouteTestSummary(proto: proto.latencyResult) : nil,
      updatedAt: proto.hasUpdatedAt ? proto.updatedAt.date : nil, attempt: proto.attempt)
  }
}

extension DownloadQueueState {
  init(proto: Pax_V1_DownloadQueueState) {
    self.init(
      mode: proto.mode, activeRouteID: proto.activeRouteID, queuedRouteIDs: proto.queuedRouteIds,
      operationIDs: proto.operationIds)
  }
}
