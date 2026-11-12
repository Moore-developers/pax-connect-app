import Foundation
import Observation
import PaxClient

/// Saved catalog has one owner; ConnectionStore exposes a shared projection for TLS consumers.
@MainActor @Observable
public final class RoutesStore {
  public private(set) var groups: [RouteGroup] = []
  public private(set) var routes: [NodeProfile] = []
  public private(set) var subscriptions: [SubscriptionSummary] = []
  public private(set) var selectedRouteID = ""
  public private(set) var catalogRevision: UInt64 = 0
  public private(set) var connected = false
  public private(set) var capabilities: Set<String> = []
  public private(set) var busy = false
  public private(set) var pendingID: String?
  public private(set) var message = ""
  public private(set) var actionFeedbacks: [String: ConnectionFeedback] = [:]
  public private(set) var feedbackOrder: [String: UInt64] = [:]
  private var feedbackSequence: UInt64 = 0
  private var feedbackOwners: [String: String] = [:]
  private func recordFeedback(_ owner: String, detail: String?) {
    feedbackSequence &+= 1
    feedbackOrder[owner] = feedbackSequence
    actionFeedbacks[owner] = detail.map {
      ConnectionFeedback(
        id: owner, title: owner == "measurement" ? "线路测量未完成" : "线路操作未完成",
        detail: $0, action: .details)
    }
  }
  public private(set) var refreshFailures: [String: ProductError] = [:]
  public private(set) var selectionError: ProductError?
  public private(set) var selecting = false
  public private(set) var selectionTargetID: String?
  func clearSelectionFeedback() { selectionError = nil }
  private var selectionOperationID: String?
  public private(set) var measurementError: ProductError?
  public private(set) var physicalEgress: PhysicalEgress?
  public private(set) var exitPoolState: ExitPoolState?
  public private(set) var routeMeasurementProgress: [String: RouteMeasurementProgress] = [:]
  public private(set) var measurementPreferences: MeasurementPreferences?
  public private(set) var history: [String: [RouteHistorySample]] = [:]
  public private(set) var usage: [String: RouteUsagePage] = [:]
  public private(set) var historyCursors: [String: String] = [:]
  var usagePages: [String: Int] = [:]
  var usageFrom: [String: Date] = [:]
  var usagePeriod: [String: String] = [:]
  let periodUsageQuery: (@Sendable (String, String) async throws -> RouteUsagePage)?
  var usageQueryGeneration: [String: UInt64] = [:]
  public private(set) var usageCursors: [String: String] = [:]
  private var historyErrors: [String: String] = [:]
  private var usageErrors: [String: String] = [:]
  private var historyLoading: Set<String> = []
  public var dataErrors: [String: String] {
    usageErrors.merging(historyErrors) { _, history in history }
  }
  public func historyError(_ id: String) -> String? { historyErrors[id] }
  public func usageError(_ id: String) -> String? { usageErrors[id] }
  private let historyQuery: @Sendable (String, String) async throws -> RouteHistoryPage
  let usageQuery: @Sendable (String, String, Date?) async throws -> RouteUsagePage
  private let measurementCommand: @Sendable (MeasurementRequest) async throws -> MeasurementReply
  /// 手动刷新进行中的来源：sourceID → operation id；断线即清（合并结果仍会进快照）。
  public private(set) var refreshing: [String: String] = [:]
  private var pendingRefreshes: [String: String] = [:]
  /// 连通性测试进行中的线路；事实来自快照 testingRouteIDs，断线随之消失。
  public private(set) var exitOperationIDs: [String: String] = [:]
  private var pendingExitTests: Set<String> = []
  public private(set) var downloadingRouteIDs: Set<String> = []
  public private(set) var downloadOperationID = ""
  public private(set) var downloadQueue: DownloadQueueState?
  public private(set) var testingRouteIDs: Set<String> = []
  public private(set) var latencyOperationID = ""
  public private(set) var testOperationIDs: [String: String] = [:]
  public var filter = ""
  public var sourceFilter: SourceFilter = .all
  public var browsedID: String?
  private let preview: @Sendable (String, Bool, String) async throws -> RoutesPreview
  private let cancel: @Sendable (String) async throws -> Void
  private let cancelOperation: @Sendable (String) async throws -> Void
  private let editor: @Sendable (String) async throws -> RouteEdit
  private let command: @Sendable (Intent) async throws -> String
  private var saveWaiter: (id: String, continuation: CheckedContinuation<OperationResult, Error>)?
  private(set) var sessionGeneration: UInt64 = 0
  private var localPending: String?
  private var pendingTests: [String: Bool] = [:]
  private var pendingTestRoutes: [String: Set<String>] = [:]
  private var exitIP = RouteExitCompare()
  private var assessmentIDs: Set<String> = []
  private var results: [String: OperationResult] = [:]
  private var completed: [String] = []
  public init(
    preview: @escaping @Sendable (String, Bool, String) async throws -> RoutesPreview,
    cancel: @escaping @Sendable (String) async throws -> Void,
    cancelOperation: @escaping @Sendable (String) async throws -> Void,
    editor: @escaping @Sendable (String) async throws -> RouteEdit,
    command: @escaping @Sendable (Intent) async throws -> String,
    history: @escaping @Sendable (String, String) async throws -> RouteHistoryPage = { _, _ in
      throw DocumentSaveError.unavailable
    },
    periodUsage: (@Sendable (String, String) async throws -> RouteUsagePage)? = nil,
    usage: @escaping @Sendable (String, String, Date?) async throws -> RouteUsagePage = { _, _, _ in
      throw DocumentSaveError.unavailable
    },
    measurement: @escaping @Sendable (MeasurementRequest) async throws -> MeasurementReply = { _ in
      throw DocumentSaveError.unavailable
    }
  ) {
    self.historyQuery = history
    self.usageQuery = usage
    self.preview = preview
    self.cancel = cancel
    self.cancelOperation = cancelOperation
    self.editor = editor
    periodUsageQuery = periodUsage
    self.command = command
    self.measurementCommand = measurement
  }
  public func saveChain(_ edit: ChainEdit) async throws {
    try await performAndWait(.saveChain(edit))
  }
  public func loadMeasurementHistory(_ id: String, more: Bool = false) async {
    guard connected, !historyLoading.contains(id) else { return }
    historyLoading.insert(id)
    defer { historyLoading.remove(id) }
    let generation = sessionGeneration
    if capabilities.contains("routes.history"), !more || !(historyCursors[id] ?? "").isEmpty {
      do {
        let page = try await historyQuery(id, more ? historyCursors[id] ?? "" : "")
        guard connected, generation == sessionGeneration else { return }
        history[id] = (more ? history[id] ?? [] : []) + page.items
        historyCursors[id] = page.nextCursor
        historyErrors.removeValue(forKey: id)
      } catch { historyErrors[id] = ProductErrorPresentation.display(error) }
    }
  }
  func commitUsageError(_ id: String, _ error: Error?) {
    if let error {
      usageErrors[id] = ProductErrorPresentation.display(error)
    } else {
      usageErrors.removeValue(forKey: id)
    }
  }
  func commitUsageWindow(
    _ id: String, items: [RouteUsageBucket], page: RouteUsagePage, cursor: String, count: Int
  ) {
    var merged = page
    merged.items = items
    merged.nextCursor = cursor
    usage[id] = merged
    usageCursors[id] = cursor
    usagePages[id] = count
  }
  public var browsedRoute: NodeProfile? { routes.first { $0.id == browsedID } }
  /// 只读投影：已选定线路（概览页在 OFF 时展示下一次开启的走向）。
  public var selectedRoute: NodeProfile? { routes.first { $0.id == selectedRouteID } }
  public var canWrite: Bool {
    connected && !busy && pendingID == nil && localPending == nil
  }
  public func sourceName(_ id: String) -> String {
    subscriptions.first { $0.id == id }?.name ?? ""
  }
  /// 是否有已隐藏的订阅线路：决定「已隐藏」入口是否显示。
  public var hasHidden: Bool { routes.contains { $0.hidden } }
  public func isRefreshing(_ sourceID: String?) -> Bool {
    guard let sourceID else { return false }
    return refreshing[sourceID] != nil
  }
  public func isTesting(_ routeID: String) -> Bool { testingRouteIDs.contains(routeID) }
  public func consume(_ update: ConnectionUpdate) {
    switch update {
    case .ready(let handshake, let snapshot):
      failSaveWaiter()
      sessionGeneration &+= 1
      busy = false
      localPending = nil
      pendingTests.removeAll()
      pendingTestRoutes.removeAll()
      feedbackOwners.removeAll()
      results = [:]
      completed = []
      message = ""
      capabilities = handshake.capabilities
      connected = true
      refreshing = [:]
      pendingRefreshes = [:]
      apply(snapshot, replaySourceFailures: false)
    case .events(let events):
      for event in events {
        if case .snapshot(let s) = event { apply(s) }
        if case .operation(let r) = event {
          let wasSelection = selectionOperationID == r.operationID
          if wasSelection {
            selecting = false
            selectionOperationID = nil
            selectionError = r.error
          }
          completed.append(r.operationID)
          if completed.count > 64 { completed.removeFirst() }
          results[r.operationID] = r
          if saveWaiter?.id == r.operationID {
            let waiter = saveWaiter
            saveWaiter = nil
            waiter?.continuation.resume(returning: r)
          }
          if results.count > 64 { results.removeValue(forKey: completed[0]) }
          if let sourceID = pendingRefreshes.first(where: { $0.value == r.operationID })?.key
            ?? refreshing.first(where: { $0.value == r.operationID })?.key
          {
            refreshFailures[sourceID] = r.ok ? nil : r.error
            refreshing[sourceID] = nil
            pendingRefreshes[sourceID] = nil
            message =
              r.ok
              ? refreshDoneMessage(sourceID)
              : r.error.map(ProductErrorPresentation.message) ?? "操作失败"
          }
          if r.operationID == localPending {
            localPending = nil
            pendingID = nil
            message = r.ok ? "操作完成" : r.error.map(ProductErrorPresentation.message) ?? "操作失败"

          }
          if let owner = feedbackOwners.removeValue(forKey: r.operationID), !wasSelection {
            recordFeedback(
              owner, detail: r.ok ? nil : r.error.map(ProductErrorPresentation.message) ?? "操作失败")
          }
          pendingTestRoutes[r.operationID] = nil
          if pendingExitTests.remove(r.operationID) != nil {
            pendingTestRoutes[r.operationID] = nil
            message = exitIP.finish(r, nil, routes)
          } else if let download = pendingTests.removeValue(forKey: r.operationID) {
            message = measurementMessage(r, download: download)
          }
          if assessmentIDs.remove(r.operationID) != nil {
            message =
              r.ok ? "并发评估完成，重新读取配置查看推荐" : r.error.map(ProductErrorPresentation.message) ?? "评估未完成"
          }
        }
      }
    case .disconnected, .retrying, .failed:
      selecting = false
      selectionOperationID = nil
      failSaveWaiter()
      sessionGeneration &+= 1
      connected = false
      busy = false
      pendingID = nil
      localPending = nil
      pendingTests.removeAll()
      pendingTestRoutes.removeAll()
      feedbackOwners.removeAll()
      refreshing = [:]
      pendingRefreshes = [:]
      testingRouteIDs = []
      latencyOperationID = ""
      testOperationIDs = [:]
      routeMeasurementProgress = [:]
      exitPoolState = nil
      exitOperationIDs = [:]
      pendingExitTests = []
      exitIP.reset()
      downloadingRouteIDs = []
      downloadOperationID = ""
      downloadQueue = nil
    case .connecting:
      failSaveWaiter()
      sessionGeneration &+= 1
      connected = false
    }
  }
  private func refreshDoneMessage(_ sourceID: String) -> String {
    guard let summary = subscriptions.first(where: { $0.id == sourceID }) else { return "订阅已刷新" }
    let report = summary.lastRefresh
    if report.partial {
      return "部分刷新失败：保留 \(report.kept) 条未确认线路"
    }
    if report.removed > 0 {
      return "刷新完成：新增 \(report.added)，来源移除 \(report.removed)"
    }
    return "订阅已刷新"
  }
  private func apply(_ snapshot: DaemonSnapshot, replaySourceFailures: Bool = true) {
    physicalEgress = snapshot.physicalEgress
    let poolSupported = capabilities.contains("measurement.exit_pool.v1")
    exitPoolState = poolSupported ? snapshot.exitPoolState : nil
    routeMeasurementProgress =
      poolSupported
      ? Dictionary(
        uniqueKeysWithValues: snapshot.routeMeasurementProgress.filter { progress in
          snapshot.testingRouteIDs.contains(progress.routeID)
            && snapshot.routes.contains {
              $0.id == progress.routeID && $0.configRevision == progress.testedConfigRevision
            }
            && (snapshot.physicalEgress?.network.isEmpty != false || progress.network.isEmpty
              || progress.network == snapshot.physicalEgress?.network)
        }.map { ($0.routeID, $0) }) : [:]
    let measurementAdvanced = snapshot.routes.contains { next in
      guard let previous = routes.first(where: { $0.id == next.id }) else { return false }
      return (next.testSummary != nil && previous.testSummary != next.testSummary)
        || (next.exitSummary != nil && previous.exitSummary != next.exitSummary)
        || (next.downloadSummary != nil && previous.downloadSummary != next.downloadSummary)
    }
    routes = snapshot.routes
    exitIP.resolve(routes, &message)
    let previousSources = subscriptions
    let validSources = Set(snapshot.subscriptions.map { "subscription:" + $0.id })
    actionFeedbacks = actionFeedbacks.filter {
      !$0.key.hasPrefix("subscription:") || validSources.contains($0.key)
    }
    feedbackOrder = feedbackOrder.filter {
      !$0.key.hasPrefix("subscription:") || validSources.contains($0.key)
    }
    subscriptions = snapshot.subscriptions
    refreshFailures = refreshFailures.filter { id, _ in subscriptions.contains { $0.id == id } }
    for source in subscriptions {
      let previous = previousSources.first { $0.id == source.id }
      if source.lastError != previous?.lastError
        || previous?.revision != source.revision
      {
        refreshFailures[source.id] = replaySourceFailures ? source.lastError : nil
        actionFeedbacks["subscription:" + source.id] = nil
      }
    }
    if capabilities.contains("subscriptions.refresh") {
      refreshing = Dictionary(
        uniqueKeysWithValues: subscriptions.filter { !$0.refreshOperationID.isEmpty }.map {
          ($0.id, $0.refreshOperationID)
        })
    }
    groups = snapshot.routeGroups
    testingRouteIDs = Set(snapshot.testingRouteIDs)
    testOperationIDs =
      capabilities.contains("measurement.test_cancel.v1")
      ? snapshot.testOperationIDs.filter { testingRouteIDs.contains($0.key) } : [:]
    latencyOperationID = poolSupported ? snapshot.latencyOperationID : ""
    exitOperationIDs = snapshot.exitOperationIDs
    downloadingRouteIDs = Set(snapshot.downloadingRouteIDs)
    downloadOperationID = snapshot.downloadOperationID
    downloadQueue =
      capabilities.contains("routes.download.single_queue") ? snapshot.downloadQueue : nil
    if measurementAdvanced || measurementError != snapshot.measurementError {
      actionFeedbacks["measurement"] = nil
    }
    measurementError = snapshot.measurementError
    selectedRouteID = snapshot.selectedRouteID
    catalogRevision = snapshot.catalogRevision
    pendingID = snapshot.pendingOperationID.isEmpty ? nil : snapshot.pendingOperationID
    if browsedID == nil { browsedID = routes.first?.id }
  }
  public func measurement(_ request: MeasurementRequest) async throws -> MeasurementReply {
    guard connected else { throw DocumentSaveError.unavailable }
    let generation = sessionGeneration
    let response = try await measurementCommand(request)
    try Task.checkCancellation()
    guard connected && generation == sessionGeneration else { throw DocumentSaveError.unavailable }
    if case .preferences(let value) = response { measurementPreferences = value }
    if case .operation(let id) = response, case .evaluate = request {
      if let result = results[id] {
        message =
          result.ok
          ? "评估完成，重新读取配置查看推荐" : result.error.map(ProductErrorPresentation.message) ?? "评估失败"
      } else {
        assessmentIDs.insert(id)
        message = "正在后台评估推荐并发，可继续浏览"
      }
    }
    return response
  }
  /// Read only the terminal belonging to a measurement submitted by this screen.
  public func measurementResult(_ operationID: String) -> OperationResult? { results[operationID] }
  public func makePreview(_ source: String, subscription: Bool = false, sourceName: String = "")
    async throws -> RoutesPreview
  {
    guard connected else { throw DocumentSaveError.unavailable }
    let generation = sessionGeneration
    let result = try await preview(source, subscription, sourceName)
    guard connected && generation == sessionGeneration && !Task.isCancelled else {
      try? await cancel(result.token)
      throw DocumentSaveError.unavailable
    }
    return result
  }
  /// Cancellation is a request; only the daemon terminal changes selection.
  public func cancelSelection() async throws {
    guard connected, let id = selectionOperationID else { return }
    try await cancelOperation(id)
  }
  public func cancelPreview(_ token: String) async { try? await cancel(token) }
  public var hasLatencyTests: Bool {
    !testingRouteIDs.isEmpty || pendingTests.values.contains(false)
  }
  public func cancelTests(routeID: String? = nil) async {
    var ids = Set<String>()
    if let routeID {
      if let id = testOperationIDs[routeID] {
        ids.insert(id)
      } else if let progress = routeMeasurementProgress[routeID] {
        ids.insert(progress.operationID)
      } else if testOperationIDs.isEmpty && !latencyOperationID.isEmpty
        && testingRouteIDs.contains(routeID)
      {
        ids.insert(latencyOperationID)
      } else {
        for (id, download) in pendingTests
        where !download && pendingTestRoutes[id]?.contains(routeID) == true { ids.insert(id) }
      }
    } else {
      ids.formUnion(testOperationIDs.values)
      if !latencyOperationID.isEmpty { ids.insert(latencyOperationID) }
      for (id, download) in pendingTests where !download { ids.insert(id) }
      if !testOperationIDs.isEmpty || latencyOperationID.isEmpty {
        for progress in routeMeasurementProgress.values
        where testOperationIDs[progress.routeID] == nil { ids.insert(progress.operationID) }
      }
    }

    for id in ids {
      do { try await cancelOperation(id) } catch {
        message = ProductErrorPresentation.display(error)
      }
    }
  }
  public func cancelDownload(routeID: String? = nil) async {
    var ids: [String] = []
    if let queue = downloadQueue {
      if let routeID {
        if let id = queue.operationIDs[routeID] { ids = [id] }
      } else if queue.mode == "single" {
        // Revoke the waiting jobs before stopping the active job, so releasing
        // bandwidth cannot start one of the jobs this action intends to stop.
        ids = queue.queuedRouteIDs.compactMap { queue.operationIDs[$0] }
        ids += queue.operationIDs.filter {
          $0.key != queue.activeRouteID && !queue.queuedRouteIDs.contains($0.key)
        }.map(\.value)
        if let active = queue.operationIDs[queue.activeRouteID] { ids.append(active) }
      } else {
        ids =
          queue.operationIDs.isEmpty
          ? (downloadOperationID.isEmpty ? [] : [downloadOperationID])
          : Array(queue.operationIDs.values)
      }
    } else {
      ids = downloadOperationID.isEmpty ? [] : [downloadOperationID]
    }
    var sent = Set<String>()
    for id in ids where sent.insert(id).inserted {
      do { try await cancelOperation(id) } catch {
        message = ProductErrorPresentation.display(error)
      }
    }
  }

  public func cancelRefresh(_ sourceID: String) async {
    guard let operationID = refreshing[sourceID] else { return }
    try? await cancelOperation(operationID)
  }
  public func loadEditor(_ id: String) async throws -> RouteEdit {
    guard connected else { throw DocumentSaveError.unavailable }
    let generation = sessionGeneration
    let value = try await editor(id)
    try Task.checkCancellation()
    guard connected && generation == sessionGeneration else { throw DocumentSaveError.unavailable }
    return value
  }
  public enum DocumentSaveError: Error, LocalizedError {
    case unavailable
    case rejected(ProductError?)
    public var errorDescription: String? {
      switch self {
      case .unavailable: "与后台连接已断开或有操作正在进行，草稿仍保留"
      case .rejected(let error): error.map(ProductErrorPresentation.message) ?? "保存失败，草稿仍保留"
      }
    }
  }
  private func failSaveWaiter() {
    let waiter = saveWaiter
    saveWaiter = nil
    waiter?.continuation.resume(throwing: DocumentSaveError.unavailable)
  }
  public func saveGroups(_ edit: RouteGroupsEdit) async throws {
    guard capabilities.contains("routes.groups") else { throw DocumentSaveError.unavailable }
    try await performAndWait(.saveGroups(edit))
  }
  public func confirmImport(_ preview: RoutesPreview, items: [String]) async throws {
    guard !items.isEmpty else {
      throw DocumentSaveError.rejected(ProductError(code: "empty_import_selection"))
    }
    try await performAndWait(.confirm(preview, items))
  }
  public func importAutomatically(_ preview: RoutesPreview, items: [String]) async throws {
    guard capabilities.contains("routes.import.auto") else { throw DocumentSaveError.unavailable }
    try Task.checkCancellation()
    try await performAndWait(.automaticImport(preview, items))
    message = preview.sourceHost.isEmpty ? "已自动导入线路" : "已自动创建或更新订阅分组"
  }
  public func deleteSubscription(_ source: SubscriptionSummary, expectedCatalogRevision: UInt64)
    async throws -> UInt64
  {
    guard capabilities.contains("subscriptions.delete") else { throw DocumentSaveError.unavailable }
    try await performAndWait(.deleteSubscription(source, expectedCatalogRevision))
    message = "已删除订阅及所属线路"
    // The daemon contract advances exactly once, independently of later snapshots.
    return expectedCatalogRevision + 1
  }
  public var certificateFailures: [NodeProfile] {
    routes.filter { route in
      guard !route.hidden, route.security == "tls", route.tlsPolicy?.mode != .skipVerify,
        let summary = route.testSummary
      else { return false }
      return summary.error?.code == "tls_certificate_invalid"
        && summary.testedConfigRevision == route.configRevision
    }
  }
  public func confirmTLSBatch(_ frozen: [NodeProfile]) async throws {
    guard capabilities.contains("routes.tls.batch"), !frozen.isEmpty else {
      throw DocumentSaveError.unavailable
    }
    try await performAndWait(.confirmTLSBatch(frozen))
  }
  @discardableResult func performAndWait(_ intent: Intent) async throws -> OperationResult {
    guard canWrite else { throw DocumentSaveError.unavailable }
    let generation = sessionGeneration
    busy = true
    defer { if generation == sessionGeneration { busy = false } }
    let id = try await command(intent)
    guard generation == sessionGeneration && connected else { throw DocumentSaveError.unavailable }
    try Task.checkCancellation()
    localPending = id
    pendingID = id
    let result: OperationResult
    if let finished = results[id] {
      result = finished
    } else {
      result = try await withTaskCancellationHandler {
        try await withCheckedThrowingContinuation { continuation in
          if Task.isCancelled {
            continuation.resume(throwing: CancellationError())
          } else {
            saveWaiter = (id, continuation)
          }
        }
      } onCancel: {
        Task { @MainActor [weak self] in
          guard self?.saveWaiter?.id == id else { return }
          self?.failSaveWaiter()
        }
      }
    }
    guard generation == sessionGeneration && connected else { throw DocumentSaveError.unavailable }
    localPending = nil
    pendingID = nil
    guard result.ok else { throw DocumentSaveError.rejected(result.error) }
    return result
  }
  private func measurementMessage(_ result: OperationResult, download: Bool) -> String {
    if result.ok { return download ? "下载测速完成" : "测试完成" }
    return result.error.map(ProductErrorPresentation.message) ?? "测试失败"
  }
  public func perform(_ intent: Intent) async {
    guard canWrite else { return }
    let generation = sessionGeneration
    busy = true
    message = ""
    let owner = intent.feedbackOwner
    recordFeedback(owner, detail: nil)
    defer { if generation == sessionGeneration { busy = false } }
    do {
      switch intent {
      case .refresh(let source), .refreshVia(let source, _), .refreshSaved(let source),
        .replaceSource(let source, _, _):
        refreshFailures[source.id] = nil
        // 刷新只占用它自己的来源，不阻塞其他线路操作。
        let id = try await command(intent)
        guard generation == sessionGeneration && connected else { return }
        if let result = results[id] {
          recordFeedback(
            owner,
            detail: result.ok ? nil : result.error.map(ProductErrorPresentation.message) ?? "订阅刷新失败"
          )
          refreshFailures[source.id] = result.ok ? nil : result.error
          message =
            result.ok
            ? refreshDoneMessage(source.id)
            : result.error.map(ProductErrorPresentation.message) ?? "订阅刷新失败"
        } else {
          refreshing[source.id] = id
          pendingRefreshes[source.id] = id
          feedbackOwners[id] = owner
          message = "正在后台刷新订阅：" + source.name
        }
      case .queryExit, .queryExitBatch:
        let baseline = exitIP.begin(intent, routes)
        let id = try await command(intent)
        guard generation == sessionGeneration && connected else { return }
        if let result = results[id] {
          message = exitIP.finish(result, baseline, routes)
          recordFeedback(owner, detail: result.ok ? nil : message)
        } else {
          pendingExitTests.insert(id)
          exitIP.record(id, baseline)
          switch intent {
          case .queryExit(let route): pendingTestRoutes[id] = [route.id]
          case .queryExitBatch(let nodes): pendingTestRoutes[id] = Set(nodes.map(\.id))
          default: break
          }
          feedbackOwners[id] = owner
          message = "正在查询 IP 地区"
        }
      case .test, .diagnose, .download, .testBatch:
        // 测试只占用一条线路，不阻塞其他操作；状态以快照 testingRouteIDs 为准。
        let id = try await command(intent)
        guard generation == sessionGeneration && connected else { return }
        let download: Bool
        switch intent {
        case .download: download = true
        case .testBatch(_, let isDownload): download = isDownload
        default: download = false
        }
        if let result = results[id] {
          message = measurementMessage(result, download: download)
          recordFeedback(owner, detail: result.ok ? nil : message)
        } else {
          pendingTests[id] = download
          switch intent {
          case .test(let route), .diagnose(let route), .download(let route):
            pendingTestRoutes[id] = [route.id]
          case .testBatch(let nodes, _): pendingTestRoutes[id] = Set(nodes.map(\.id))
          default: break
          }
          feedbackOwners[id] = owner
          message = download ? "正在后台测速，可继续浏览，完成后自动更新" : "正在后台测量，完成后自动更新"
        }
      default:
        if case .select(let route) = intent {
          selecting = true
          selectionError = nil
          selectionTargetID = route.id
        }
        let id = try await command(intent)
        guard generation == sessionGeneration && connected else { return }
        if let result = results[id] {
          if case .select = intent {
            selecting = false
            selectionError = result.error
          }
          message =
            result.ok ? "操作完成" : result.error.map(ProductErrorPresentation.message) ?? "操作失败"
          if case .select = intent {
          } else {
            recordFeedback(owner, detail: result.ok ? nil : message)
          }
        } else {
          if case .select = intent { selectionOperationID = id }
          localPending = id
          pendingID = id
          if case .select = intent {} else { feedbackOwners[id] = owner }
        }
      }
    } catch {
      if case .select = intent {
        selecting = false
        selectionError = (error as? ProductError) ?? ProductError(code: "route_selection_failed")
      }
      guard generation == sessionGeneration && connected else { return }
      if let product = error as? ProductError {
        message = ProductErrorPresentation.message(product)
      } else if let client = error as? ClientError {
        message = client.message
      } else {
        message = "操作未完成，请检查后台连接或线路设置"
      }
      if case .select = intent {} else { recordFeedback(owner, detail: message) }
    }
  }

  public func cancelExitQueries(routeID: String? = nil) async {
    var ids = Set(exitOperationIDs.filter { routeID == nil || $0.key == routeID }.values)
    for id in pendingExitTests
    where routeID == nil || pendingTestRoutes[id]?.contains(routeID ?? "") == true {
      ids.insert(id)
    }
    for id in ids {
      do { try await cancelTestCommand(id) } catch { message = "取消 IP 地区查询失败" }
    }
  }

  func cancelTestCommand(_ id: String) async throws {
    try await cancelOperation(id)
  }

  /// One admission transfers the entire frozen scope to the daemon owner.
  public func performBatch(_ routes: [NodeProfile], download: Bool) async {
    guard capabilities.contains("routes.test.batch"), !routes.isEmpty else { return }
    if download && routes.count > 20 && !capabilities.contains("routes.download.queue") {
      message = "后台版本不支持自动续批，请更新后台服务"
      return
    }
    await perform(.testBatch(routes, download))
  }
}
