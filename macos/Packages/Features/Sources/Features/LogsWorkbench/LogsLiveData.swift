import Foundation
import PaxCore

extension LogsWorkbenchStore {
  convenience init(telemetry: TelemetryStore) {
    self.init(events: [], backendAvailable: false)
    self.telemetry = telemetry
    seenSession = telemetry.sessionGeneration
  }
  func stopQueries() {
    queryTask?.cancel()
    queryTask = nil
    requestGeneration &+= 1
    isLoading = false
    pageLoading = false
    requestedPageIndex = nil
  }
  func scheduleReload(
    directoryOnly: Bool = false, eventsOnly: Bool = false, delay: Bool = false, fresh: Bool = false,
    moreEvents: Bool = false, moreObjects: Bool = false
  ) {
    queryTask?.cancel()
    requestGeneration &+= 1
    pageLoading = false
    requestedPageIndex = nil
    queryTask = Task { [weak self] in
      if delay { do { try await Task.sleep(for: .milliseconds(300)) } catch { return } }
      guard let self, !Task.isCancelled, self.telemetry?.isShown(.logs, focus: false) == true else {
        return
      }
      await self.reload(
        moreEvents: moreEvents, moreObjects: moreObjects, directoryOnly: directoryOnly,
        eventsOnly: eventsOnly, fresh: fresh)
    }
  }
  func revokeSnapshot() {
    guard privacyInvalid, let telemetry else { return }
    let identity = "\(telemetry.sessionGeneration):\(telemetry.invalidationGeneration)"
    guard revokedIdentity != identity else { return }
    revokedIdentity = identity
    invalidationRecovery = initialized
    liveMessage = ""
    stopQueries()
    liveRecords = []
    liveObjects = []
    liveTotal = 0
    recordTotal = 0
    eventCursor = ""
    objectCursor = ""
    readToken = ""
    directoryToken = ""
    // Keep opaque page offsets and the reading anchor, but never reuse revoked tokens.
    perform(.select(nil))
  }
  func updateSnapshotOnEntry(entering: Bool = false) async {
    let recovering = privacyInvalid
    revokeSnapshot()
    if entering {
      reading = false
      resetDirectoryOrder = true
      rangeStart = nil
      rangeEnd = nil
    }
    await reload(fresh: true, followsLatest: !entering && !recovering)
  }
  func resetRange() {
    rangeStart = nil
    rangeEnd = nil
    readToken = ""
    scheduleReload(fresh: true)
  }
  func filter() -> LogFilter {
    var q = LogFilter()
    q.scope = dimension == .system ? "system" : "access"
    q.includeDNS = includeDNS
    if usesViews { q.view = "activity" }
    q.dimension = ["domain", "application", "ip", "system"][dimension.rawValue]
    q.from = rangeStart
    q.until = rangeEnd
    q.objectID = selectedObject == "all" ? "" : selectedObject
    q.level = ["", "error", "warning", "info"][level.rawValue]
    return q
  }
  func reload(
    moreEvents: Bool = false, moreObjects: Bool = false, directoryOnly: Bool = false,
    eventsOnly: Bool = false, fresh: Bool = false, pageCursor: String? = nil,
    pageTarget: Int? = nil,
    pageRequest: UInt64? = nil, followsLatest: Bool = false
  ) async {
    guard let telemetry, telemetry.available, telemetry.logSummaryAvailable else {
      liveMessage = "后台未提供连接摘要，请更新后台"
      return
    }
    if pageCursor == nil && !moreEvents && !moreObjects && !directoryOnly && !invalidationRecovery {
      pageIndex = 0
      requestedPageIndex = 0
      pageCursors = [""]
    }
    if let pageRequest, pageRequest != requestGeneration { return }
    if pageRequest == nil { requestGeneration &+= 1 }
    let generation = requestGeneration
    isLoading = true
    defer {
      if generation == requestGeneration {
        isLoading = false
        if Task.isCancelled {
          if pageRequest != nil { pageLoading = false }
          requestGeneration &+= 1
        }
      }
    }
    let session = telemetry.sessionGeneration
    let previousHistory = telemetry.history
    let until = followsLatest ? Date() : rangeEnd ?? Date()
    let from =
      followsLatest || rangeStart == nil
      ? until.addingTimeInterval(-[1800.0, 21600, 86400][min(2, max(0, rangeIndex))]) : rangeStart
    var base = filter()
    base.from = from
    base.until = until
    var q = base
    var directoryCommit: (() -> Void)?
    var nextReadToken = readToken
    var selectedDirectoryID: String?
    do {
      if moreEvents || moreObjects || eventsOnly { q.readToken = readToken }
      if !moreEvents && !eventsOnly {
        if moreObjects {
          q.readToken = directoryToken
          q.from = directoryStart
          q.until = directoryEnd
        }
        q.search = query
        q.cursor = moreObjects ? objectCursor : ""
        let directory = try await telemetry.objects(q, force: fresh)
        guard !Task.isCancelled, generation == requestGeneration,
          session == telemetry.sessionGeneration,
          !telemetry.logsInvalidated(since: previousHistory, from: from),
          !followsLatest || !reading
        else { return }
        selectedDirectoryID = directory.selectedObject?.id
        if !directoryOnly && !moreObjects { nextReadToken = directory.readToken }
        var objects = directory.items
        if query.isEmpty, let selected = directory.selectedObject,
          !objects.contains(where: { $0.id == selected.id })
        {
          objects.append(selected)
        }
        let rows = objects.map {
          ObjectRow(
            id: $0.id, title: $0.id == "unknown" ? dimension.unknown : $0.name,
            count: Int($0.count), symbol: $0.symbol
          )
        }
        var combined =
          moreObjects
          ? (liveObjects + rows).reduce(into: [ObjectRow]()) { result, row in
            if !result.contains(where: { $0.id == row.id }) { result.append(row) }
          } : rows
        if dimension != .system {
          combined.sort { a, b in
            if a.count != b.count { return a.count > b.count }
            let an = a.title.lowercased()
            let bn = b.title.lowercased()
            return an == bn ? a.id < b.id : an.utf8.lexicographicallyPrecedes(bn.utf8)
          }
        }
        if !resetDirectoryOrder {
          let ranks = Dictionary(
            uniqueKeysWithValues: directoryOrder.enumerated().map { ($0.element, $0.offset) })
          combined = combined.enumerated().sorted {
            let left = ranks[$0.element.id] ?? (ranks.count + $0.offset)
            let right = ranks[$1.element.id] ?? (ranks.count + $1.offset)
            return left < right
          }.map(\.element)
        }
        let directoryFrom = q.from
        let directoryUntil = q.until
        directoryCommit = {
          if !moreObjects {
            self.restoreDirectorySelection(directory.selectedObject?.id)
            self.directoryToken = directory.readToken
            self.directoryStart = directoryFrom
            self.directoryEnd = directoryUntil
          }
          self.resetDirectoryOrder = false
          self.directoryOrder = combined.map(\.id)
          self.liveTotal = directory.totalEvents
          if self.liveObjects != combined { self.liveObjects = combined }
          self.objectCursor = directory.nextCursor
        }
        if directoryOnly || moreObjects {
          directoryCommit?()
          // A directory-only query cannot make previously revoked records valid.
          return
        }
      }
      q = base
      q.objectID = selectedObject == "all" ? "" : selectedObject
      q.readToken = nextReadToken
      if directoryCommit != nil && selectedObject != "all" && selectedDirectoryID != selectedObject
      {
        q.objectID = ""
      }
      q.cursor = pageCursor ?? (moreEvents ? eventCursor : "")
      var page = try await telemetry.logs(q, force: fresh)
      var recoveredPage: Int?
      if invalidationRecovery, pageIndex > 0 {
        let target = min(pageIndex, max(0, (Int(page.total) - 1) / 100))
        if target > 0, pageCursors.indices.contains(target) {
          q.cursor = pageCursors[target]
          q.readToken = page.readToken
          page = try await telemetry.logs(q, force: true)
        }
        recoveredPage = target
      }
      guard !Task.isCancelled, generation == requestGeneration,
        session == telemetry.sessionGeneration,
        !telemetry.logsInvalidated(since: previousHistory, from: from),
        !followsLatest || !reading
      else { return }
      directoryCommit?()
      if let target = recoveredPage ?? pageTarget { pageIndex = target }
      rangeStart = from
      rangeEnd = until
      requestedPageIndex = nil
      if liveRecords != page.records { liveRecords = page.records }
      recordTotal = page.total
      eventCursor = page.nextCursor
      readToken = page.readToken
      seenInvalidation = telemetry.invalidationGeneration
      seenHistory = page.history
      seenSession = session
      liveMessage = invalidationRecovery ? "旧快照已失效，已重新读取最新记录" : ""
      invalidationRecovery = false
      initialized = true

    } catch {
      guard !Task.isCancelled, generation == requestGeneration,
        session == telemetry.sessionGeneration
      else { return }
      liveMessage =
        (privacyInvalid || !initialized ? "读取失败 · " : "读取失败，保留上次快照 · ")
        + ProductErrorPresentation.display(error)
    }
  }
}
