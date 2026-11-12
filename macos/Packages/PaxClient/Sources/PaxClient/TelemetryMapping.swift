import Foundation
import PaxProto

extension HistoryState {
  init(_ v: Pax_V1_HistoryState) {
    self.init()
    aggregateCoverageStart = v.hasAggregateCoverageStart ? v.aggregateCoverageStart.date : nil
    summaryGeneration = v.summaryGeneration
    logsRevision = v.logsRevision
    trafficRevision = v.trafficRevision
    logHeadSequence = v.logHeadSequence
    logDeletionGeneration = v.logDeletionGeneration
    logRemovalBeforeMs = v.logRemovalBeforeMs
    trafficDeletionGeneration = v.trafficDeletionGeneration
    routeHistoryRevision = v.routeHistoryRevision
    journalGeneration = v.journalGeneration

    revision = v.revision
    clearGeneration = v.clearGeneration_p
    coverageStart = v.hasCoverageStart ? v.coverageStart.date : nil
    partial = v.partial
    capacityEvicted = v.capacityEvicted
    collectionState = v.collectionState
    errorCode = v.errorCode
  }
}
extension HistoryLogPage {
  init(_ v: Pax_V1_LogPage) {
    self.init()
    options.applications = v.options.applications
    options.routes = v.options.routes
    options.rulePackages = v.options.rulePackages
    records = v.records.map(LogRecord.init)
    asOf = v.hasAsOf ? v.asOf.date : nil
    problemGroups = v.problemGroups
    headSequence = v.headSequence
    items = v.items.map { ProductLog(proto: $0) }
    nextCursor = v.nextCursor
    readToken = v.readToken
    history = HistoryState(v.history)
    total = v.total
  }
}
extension HistoryObject {
  init(_ v: Pax_V1_LogObject) {
    self.init()
    symbol = v.symbol
    id = v.id
    name = v.name
    count = v.count
  }
}
extension HistoryObjectPage {
  init(_ v: Pax_V1_LogObjectPage) {
    self.init()
    selectedObject = v.hasSelectedObject ? HistoryObject(v.selectedObject) : nil
    items = v.items.map(HistoryObject.init)
    nextCursor = v.nextCursor
    readToken = v.readToken
    history = HistoryState(v.history)
    totalEvents = v.totalEvents
    unknownEvents = v.unknownEvents
  }
}
extension FlowFact {
  init(_ v: Pax_V1_FlowRecord) {
    self.init()
    sourceKind = v.sourceKind
    sourceLabel = v.sourceLabel
    sourceEvidence = v.sourceEvidence
    phase = v.phase
    purpose = v.purpose
    ipSource = v.ipSource
    processStatus = v.processStatus
    if v.hasDns {
      var value = DNSFact()
      value.queryType = v.dns.queryType
      value.result = v.dns.result
      value.answers = v.dns.answers
      dns = value
    }
    responseKnown = v.responseKnown
    responseMS = v.responseMs
    stateUnknown = v.stateUnknown
    observedUntil = v.hasObservedUntil ? v.observedUntil.date : nil
    key = v.key
    applicationID = v.applicationID
    application = v.application
    process = v.process
    primaryDomain = v.primaryDomain
    domain = v.domain
    ip = v.ip
    port = v.port
    transport = v.transport
    routeID = v.routeID
    routeName = v.routeName
    direction = v.direction
    rule = v.rule
    ruleSource = v.ruleSource
    startedAt = v.startedAt.date
    closedAt = v.hasClosedAt ? v.closedAt.date : nil
    establishmentState = v.establishmentState
    establishmentMS = v.establishmentMs
    failure = v.failure
    upload = v.upload
    download = v.download
    evidence = v.evidence
  }
}
extension HistoryLogDetail {
  init(_ v: Pax_V1_LogDetail) {
    self.init()
    record = v.hasRecord ? LogRecord(v.record) : nil
    timeline = v.timeline.map { ProductLog(proto: $0) }
    totalEvents = v.totalEvents
    event = v.hasEvent ? ProductLog(proto: v.event) : nil
    flow = v.hasFlow ? FlowFact(v.flow) : nil
    raw = v.raw
    truncated = v.truncated
    rawOmitted = v.rawOmitted
  }
}
extension TrafficExit {
  init(_ v: Pax_V1_TrafficExit) {
    self.init()
    connections = v.connections
    routeID = v.routeID
    routeName = v.routeName
    direction = v.direction
    upload = v.upload
    download = v.download
  }
}
extension TrafficRoutingMatch {
  init(_ v: Pax_V1_TrafficRoutingMatch) {
    self.init(
      at: v.at.date, rule: v.rule, source: v.source, applicationID: v.applicationID,
      application: v.application, identity: v.identity)
  }
}
extension TrafficRow {
  init(_ v: Pax_V1_TrafficRow) {
    self.init()
    latestMatch = v.hasLatestMatch ? TrafficRoutingMatch(v.latestMatch) : nil
    exits = v.exits.map(TrafficExit.init)
    recentResponses = v.recentResponses.map {
      TrafficResponsePoint(
        connectionKey: $0.connectionKey, startedAt: $0.startedAt.date,
        milliseconds: $0.milliseconds)
    }
    recentResponsesState = v.recentResponsesState
    recentResponsesError = v.recentResponsesError
    p50MS = v.p50Ms
    p95MS = v.p95Ms
    latencyAggregated = v.latencyAggregated
    sourceKind = v.sourceKind
    sourceLabel = v.sourceLabel
    sourceEvidence = v.sourceEvidence
    kind = v.kind
    bytesKnown = v.bytesKnown
    validLatencyCount = v.validLatencyCount
    missingLatencyCount = v.missingLatencyCount
    failedCount = v.failedCount
    earlierCount = v.earlierCount
    latencySampled = v.latencySampled
    id = v.id
    objectID = v.objectID
    name = v.name
    routeID = v.routeID
    routeName = v.routeName
    direction = v.direction
    upload = v.upload
    download = v.download
    connections = v.connections
    slowestMS = v.slowestMs
    establishmentKnown = v.establishmentKnown
    basis = v.basis
    latestLatencyMS = v.latestLatencyMs
    latestLatencyAt = v.hasLatestLatencyAt ? v.latestLatencyAt.date : nil
    lastActiveAt =
      v.lastActiveAt > 0 ? Date(timeIntervalSince1970: Double(v.lastActiveAt) / 1000) : nil
    latencyKnown = v.latencyKnown
    slowestLatencyMS = v.slowestLatencyMs
    bases = v.bases.map(TrafficBasis.init)
    latencySamples = v.latencySamples.map { value in
      var sample = TrafficLatencySample()
      sample.at = value.at.date
      sample.milliseconds = value.milliseconds
      sample.known = value.known
      sample.count = value.count
      sample.p95MS = value.p95Ms
      sample.seconds = value.seconds
      return sample
    }
  }
}
extension TrafficPoint {
  init(_ v: Pax_V1_TrafficPoint) {
    self.init()
    seconds = v.seconds
    partial = v.partial
    at = v.at.date
    upload = v.upload
    download = v.download
    gap = v.gap
  }
}
extension TrafficPage {
  init(_ v: Pax_V1_TrafficPage) {
    self.init()
    window = v.window
    unknownSource = v.hasUnknownSource ? TrafficRow(v.unknownSource) : nil
    dnsSummary = v.hasDnsSummary ? TrafficRow(v.dnsSummary) : nil
    items = v.items.map(TrafficRow.init)
    nextCursor = v.nextCursor
    readToken = v.readToken
    asOf = v.asOf.date
    from = v.from.date
    upload = v.upload
    download = v.download
    connections = v.connections
    systemUpload = v.systemUpload
    systemDownload = v.systemDownload
    otherProcessUpload = v.otherProcessUpload
    otherProcessDownload = v.otherProcessDownload
    otherProcessConnections = v.otherProcessConnections
    trend = v.trend.map(TrafficPoint.init)
    history = HistoryState(v.history)
    totalRows = v.totalRows
  }
}
extension TrafficBasis {
  init(_ v: Pax_V1_TrafficBasis) {
    self.init()
    target = v.target
    routeName = v.routeName
    rule = v.rule
    source = v.source
    connections = v.connections
  }
}
extension TrafficDetail {
  init(_ v: Pax_V1_TrafficDetail) {
    self.init()
    retainedConnectionCount = v.retainedConnectionCount
    recentResponses = v.recentResponses.map {
      TrafficResponsePoint(
        connectionKey: $0.connectionKey, startedAt: $0.startedAt.date,
        milliseconds: $0.milliseconds)
    }
    recentResponsesState = v.recentResponsesState
    recentResponsesError = v.recentResponsesError
    aggregateOnly = v.aggregateOnly
    window = v.window
    trend = v.trend.map(TrafficPoint.init)
    dnsGroups = v.dnsGroups.map(DNSTrafficGroup.init)
    dnsGroupID = v.dnsGroupID
    dnsGroupTotal = v.dnsGroupTotal
    related = v.related.map(TrafficRelated.init)
    timings = v.timings.map(FlowFact.init)
    successful = v.successful
    failed = v.failed
    unmeasurable = v.unmeasurable
    earlier = v.earlier
    timingsTruncated = v.timingsTruncated
    summary = TrafficRow(v.summary)
    items = v.items.map(FlowFact.init)
    basis = v.basis.map(TrafficBasis.init)
    nextCursor = v.nextCursor
    readToken = v.readToken
    asOf = v.asOf.date
    from = v.from.date
    history = HistoryState(v.history)
  }
}

extension TrafficRelated {
  init(_ v: Pax_V1_TrafficRelated) {
    self.init()
    id = v.id
    name = v.name
    connectionKeys = v.connectionKeys
    download = v.download
    connections = v.connections
  }
}

extension CurrentPathCheck {
  init(_ v: Pax_V1_CurrentPathCheck) {
    self.init()
    commandOperationIDs = v.commandOperationIds
    checkID = v.checkID
    triggerOperationID = v.triggerOperationID
    routeID = v.routeID
    configRevision = v.configRevision
    coreSession = v.coreSession
    runtimeVersion = v.runtimeVersion
    networkEpoch = v.networkEpoch
    state = v.state
    cause = v.cause
    evidenceScope = v.evidenceScope
    startedAt = v.hasStartedAt ? v.startedAt.date : nil
    completedAt = v.hasCompletedAt ? v.completedAt.date : nil
  }
}
extension HistoryDelta {
  init(_ v: Pax_V1_HistoryDelta) {
    self.init()
    history = HistoryState(v.history)
    traffic = v.hasTraffic ? TrafficSnapshot(proto: v.traffic) : nil
    recentLogs = v.recentLogs.map(ProductLog.init(proto:))
    replaceRecentLogs = v.replaceRecentLogs
  }
}
extension LogUpdatePage {
  init(_ v: Pax_V1_LogUpdatePage) {
    self.init()
    items = v.items.map(ProductLog.init(proto:))
    count = v.count
    exact = v.exact
    highWater = v.highWater
    nextSequence = v.nextSequence
    hasMore = v.hasMore_p
    resetRequired = v.resetRequired
    history = HistoryState(v.history)
    asOf = v.asOf.date
  }
}

extension ConnectionMetrics {
  init(_ value: Pax_V1_ConnectionMetrics) {
    self.init()
    durationMS = value.durationMs
    durationState = value.durationState
    upload = value.upload
    download = value.download
    bytesState = value.bytesState
    bytesFrom = value.hasBytesFrom ? value.bytesFrom.date : nil
    asOf = value.hasAsOf ? value.asOf.date : nil
    reason = value.reason
  }
}
extension LogRecord {
  init(_ value: Pax_V1_LogRecord) {
    self.init()
    systemEvent = value.hasSystemEvent ? SystemEvent(value.systemEvent) : nil
    categoryName = value.categoryName
    categorySymbol = value.categorySymbol
    operationFrom = value.operationFrom
    operationTo = value.operationTo
    systemCategory = value.systemCategory
    kind = value.kind
    recordID = value.recordID
    eventID = value.eventID
    level = value.level == .error ? .error : value.level == .warning ? .warning : .info
    title = value.title
    source = value.source
    flow = value.hasFlow ? FlowFact(value.flow) : nil
    metrics = ConnectionMetrics(value.metrics)
    firstAt = value.hasFirstAt ? value.firstAt.date : nil
    lastAt = value.hasLastAt ? value.lastAt.date : nil
    eventCount = value.eventCount
    asOf = value.hasAsOf ? value.asOf.date : nil
  }
}

extension SystemEvent {
  init(_ v: Pax_V1_SystemEvent) {
    self.init()
    type = v.type
    name = v.name
    category.key = v.category.key
    category.name = v.category.name
    category.symbol = v.category.symbol
    result = v.result
    errorCode = v.errorCode
    startedAt = v.hasStartedAt ? v.startedAt.date : nil
    sections = v.sections.map { s in
      var b = EventSection()
      b.kind = s.kind
      b.title = s.title
      b.fields = s.fields.map { f in
        var v = EventField()
        v.label = f.label
        v.value = f.value
        v.before = f.before
        v.after = f.after
        return v
      }
      return b
    }
  }
}

extension DNSTrafficGroup {
  init(_ v: Pax_V1_DNSTrafficGroup) {
    self.init()
    id = v.id
    domain = v.domain
    queryType = v.queryType
    result = v.result
    answers = v.answers
    queries = v.queries
    lastAt = v.lastAt.date
    latencyKnown = v.latencyKnown
    latencyMS = v.latencyMs
  }
}
