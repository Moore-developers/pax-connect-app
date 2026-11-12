import Foundation
import Observation
import PaxCore

/// One picker retains discovered IPs; only explicit user actions perform measurements.
@MainActor @Observable final class CFWorkspaceStore {
  private(set) var workspace = CFWorkspace()
  private(set) var busy = false
  private(set) var loading = false
  private(set) var error = ""
  var search = "" { didSet { if search != oldValue { resetPage() } } }
  private(set) var sortColumn = 1
  private(set) var sortAscending = true
  private(set) var displayLimit = 50
  private(set) var visibleRows: [CFRecord] = []
  private(set) var rows: [CFRecord] = []
  private(set) var displayCount = 0
  private(set) var taskRunning = false
  private(set) var testingIPs: Set<String> = []
  private var hasDisplayedSnapshot = false
  var selectedIPs: Set<String> = []
  var selected: String { didSet { if selected != oldValue { rebuildPage() } } }
  private var query: CFWorkspaceQuery
  private let command: @Sendable (MeasurementRequest) async throws -> MeasurementReply
  private let cancel: @Sendable (String) async throws -> Void
  private var generation: UInt64 = 0
  private var refreshSequence: UInt64 = 0
  private var ownedOperation = ""
  private var unconfirmedOperation = ""
  private var testingAction = ""
  private var testingStarted: Date?
  private let locationsEnabled: Bool
  /// 任务进行中的进度读取由 PaxRefresh 的 `.cfTask`（最小 1s）把节拍；nil（演示 / 测试装配）时回退为 1s 等待。
  private let pageRefresh: PaxRefresh?
  private var tickWaiter: CheckedContinuation<Void, Never>?
  init(
    query: CFWorkspaceQuery, selected: String, locationsEnabled: Bool = true,
    refresh: PaxRefresh? = nil,
    command: @escaping @Sendable (MeasurementRequest) async throws -> MeasurementReply,
    cancel: @escaping @Sendable (String) async throws -> Void
  ) {
    pageRefresh = refresh
    self.locationsEnabled = locationsEnabled
    self.query = query
    self.selected = selected
    selectedIPs = RouteWorkbenchStore.validIP(selected) ? [selected] : []
    self.command = command
    self.cancel = cancel
  }
  var canShowMore: Bool { visibleRows.count < displayCount }
  func showMore() {
    guard canShowMore else { return }
    displayLimit += 50
    rebuildPage()
  }
  func setSort(column: Int, ascending: Bool) {
    guard (0...2).contains(column), column != sortColumn || ascending != sortAscending else {
      return
    }
    sortColumn = column
    sortAscending = ascending
    resetPage()
  }
  private func resetPage() {
    displayLimit = 50
    rebuildRows()
  }
  private func rebuildRows() {
    var values = matchingRecords()
    values.sort { left, right in
      if sortColumn == 0 {
        let comparison = left.ip.localizedStandardCompare(right.ip)
        return sortAscending ? comparison == .orderedAscending : comparison == .orderedDescending
      }
      let l = metric(left)
      let r = metric(right)
      if l == nil && r == nil { return left.ip < right.ip }
      if l == nil || r == nil { return l != nil && r == nil }
      if let l, let r, l != r { return sortAscending ? l < r : l > r }
      return left.ip < right.ip
    }
    if rows != values { rows = values }
    rebuildPage()
  }
  private func matchingRecords() -> [CFRecord] {
    let query = search.trimmingCharacters(in: .whitespacesAndNewlines)
    return workspace.records.filter {
      query.isEmpty || $0.ip.localizedCaseInsensitiveContains(query)
        || $0.prefix.localizedCaseInsensitiveContains(query)
    }
  }
  private var scanRunning: Bool {
    !workspace.operationID.isEmpty
      && ["scan", "discovery", "precision", "scan_saving"].contains(workspace.phase)
  }
  var emptyMessage: String {
    if !workspace.network.isEmpty && workspace.network != workspace.currentNetwork {
      return "网络已变化，请重新扫描"
    }
    if !workspace.records.isEmpty && !search.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
      && matchingRecords().isEmpty
    {
      return "没有匹配的 IP 或网段"
    }
    if scanRunning { return "正在扫描，等待可用入口…" }
    if workspace.phase == "scan_incomplete" { return "扫描未完成，请重新扫描" }
    if workspace.phase == "scan_complete" { return "本次未发现可用入口" }
    return "暂无 IP。点击扫描网段查找可用入口。"
  }
  private func updateScanRows() {
    let values = matchingRecords()
    let updated = Dictionary(uniqueKeysWithValues: values.map { ($0.ip, $0) })
    let kept = rows.compactMap { updated[$0.ip] }
    let ids = Set(kept.map(\.ip))
    rows = kept + values.filter { !ids.contains($0.ip) }
    rebuildPage()
  }
  private func metric(_ row: CFRecord) -> Double? {
    if sortColumn == 1 && !row.tcp.errorCode.isEmpty { return nil }
    if sortColumn == 2 && !row.download.errorCode.isEmpty { return nil }
    let value = sortColumn == 1 ? row.tcp.value : row.download.value
    return value > 0 && value.isFinite ? value : nil
  }
  private func rebuildPage() {
    let values = Array(rows.prefix(displayLimit))
    let count = rows.count
    if displayCount != count { displayCount = count }
    if visibleRows != values { visibleRows = values }
    let keptSelection = selectedIPs.intersection(Set(values.map(\.ip)))
    if keptSelection != selectedIPs { selectedIPs = keptSelection }
  }
  func open() async {
    await refresh()
    let token = generation
    while token == generation, taskRunning, !Task.isCancelled {
      await nextTick()
      guard !Task.isCancelled else { return }
      await refresh()
    }
  }
  /// 等到下一次允许读取进度：只有任务进行中才会被调用，空闲页面不拉。
  private func nextTick() async {
    guard let pageRefresh else {
      try? await Task.sleep(for: .seconds(1))
      return
    }
    await withTaskCancellationHandler {
      await withCheckedContinuation { continuation in
        if Task.isCancelled {
          continuation.resume()
          return
        }
        tickWaiter?.resume()
        tickWaiter = continuation
        pageRefresh.poke(.cfTask)
      }
    } onCancel: {
      Task { @MainActor [weak self] in self?.releaseTick() }
    }
  }
  /// PaxRefresh 放行（或页面离开 / 取消）时唤醒等待中的轮询。
  func releaseTick() {
    tickWaiter?.resume()
    tickWaiter = nil
  }
  func refresh() async {
    let token = generation
    refreshSequence &+= 1
    let requestSequence = refreshSequence
    loading = workspace.currentNetwork.isEmpty && workspace.records.isEmpty
    defer { if token == generation && requestSequence == refreshSequence { loading = false } }
    var request = query
    request.network = ""
    do {
      guard case .workspace(let value) = try await command(.cfWorkspace(request)),
        token == generation, requestSequence == refreshSequence,
        !Task.isCancelled
      else { return }
      let wasRunning = taskRunning
      let wasScanning = scanRunning
      let previousOperation = workspace.operationID
      let recordsChanged =
        workspace.records != value.records || workspace.network != value.network
        || workspace.currentNetwork != value.currentNetwork
      let networkChanged = workspace.network != value.network
      unconfirmedOperation = ""
      if workspace != value { workspace = value }
      let running = !value.operationID.isEmpty
      if taskRunning != running { taskRunning = running }
      if scanRunning {
        if !wasScanning || previousOperation != value.operationID {
          rows = []
          visibleRows = []
        }
        hasDisplayedSnapshot = true
        updateScanRows()
      } else if !hasDisplayedSnapshot
        || !running && (recordsChanged || wasRunning || !testingIPs.isEmpty || rows.isEmpty)
      {
        hasDisplayedSnapshot = true
        if networkChanged { displayLimit = 50 }
        if wasScanning || testingAction == "scan" {
          sortColumn = 1
          sortAscending = true
        }
        rebuildRows()
      } else if running {
        let updated = Dictionary(uniqueKeysWithValues: value.records.map { ($0.ip, $0) })
        rows = rows.map { updated[$0.ip] ?? $0 }
        visibleRows = visibleRows.map { updated[$0.ip] ?? $0 }
      }
      if !ownedOperation.isEmpty && value.operationID != ownedOperation { testingIPs = [] }
      error =
        value.error.flatMap {
          $0.code == "operation_cancelled" ? nil : ProductErrorPresentation.message($0)
        } ?? ""
    } catch {
      guard token == generation, requestSequence == refreshSequence,
        !Task.isCancelled
      else { return }
      self.error = ProductErrorPresentation.display(error)
      // A failed read cannot prove the last observed task is still running.
      // Retain its cancel identity, but never keep stale cell progress alive.
      if !workspace.operationID.isEmpty {
        unconfirmedOperation = workspace.operationID
      } else if !ownedOperation.isEmpty {
        unconfirmedOperation = ownedOperation
      }
      workspace.operationID = ""
      taskRunning = false
      busy = false
      testingIPs = []
      testingAction = ""
      testingStarted = nil
    }
  }
  /// Only explicit actions observe their own in-flight operation. Idle pages never poll.
  func run(_ action: String, ips: [String] = []) async {
    guard !busy, !taskRunning else { return }
    generation &+= 1
    let token = generation
    if await execute(action, ips: ips), locationsEnabled, action != "locate", token == generation,
      !Task.isCancelled
    {
      let targets = action == "scan" ? visibleRows : visibleRows.filter { ips.contains($0.ip) }
      let locations = targets.filter { row in
        guard let location = row.location, let at = location.at else { return true }
        let age = Date().timeIntervalSince(at)
        return !location.errorCode.isEmpty || age < 0 || age >= 86400
      }.map(\.ip)
      if !locations.isEmpty { _ = await execute("locate", ips: locations) }
    }
  }
  private func execute(_ action: String, ips: [String]) async -> Bool {
    busy = true
    testingIPs = Set(ips)
    testingAction = action
    testingStarted = Date()
    if action == "scan" {
      rows = []
      visibleRows = []
      displayCount = 0
      hasDisplayedSnapshot = false
    }
    error = ""
    let token = generation
    defer {
      if token == generation {
        busy = false
        testingIPs = []
        testingAction = ""
        testingStarted = nil
      }
    }
    do {
      guard case .operation(let id) = try await command(.cfTask(action, ips)) else { return false }
      guard token == generation, !Task.isCancelled else {
        if !id.isEmpty { try? await cancel(id) }
        return false
      }
      ownedOperation = id
      await refresh()
      guard token == generation, !Task.isCancelled else { return false }
      busy = false
      while token == generation, workspace.operationID == id, !Task.isCancelled {
        await nextTick()
        guard !Task.isCancelled else { break }
        await refresh()
      }
      guard token == generation, !Task.isCancelled else { return false }
      if error.isEmpty { ownedOperation = "" }
      return workspace.error == nil && error.isEmpty
    } catch {
      if token == generation, !Task.isCancelled {
        self.error = ProductErrorPresentation.display(error)
      }
      return false
    }
  }
  /// The admitted task owns these IPs; each completed sample clears its own cell.
  func pendingColumn(for row: CFRecord) -> Int? {
    guard testingIPs.contains(row.ip), let started = testingStarted else { return nil }
    let column: Int
    let attempted: Date?
    switch testingAction {
    case "check", "latency_one":
      column = 1
      attempted = row.tcp.attemptedAt
    case "download", "download_one":
      column = 2
      attempted = row.download.attemptedAt
    case "measure_one":
      column = workspace.phase == "single_download" ? 2 : 1
      attempted = column == 1 ? row.tcp.attemptedAt : row.download.attemptedAt
    case "locate":
      // The daemon skips successful cached regions for 24 hours. Their original
      // collection time stays unchanged and must not imply an in-flight request.
      if let location = row.location, location.errorCode.isEmpty, let at = location.at {
        let age = Date().timeIntervalSince(at)
        if age >= 0 && age < 86400 { return nil }
      }
      column = 3
      attempted = row.location?.at
    default: return nil
    }
    if row.network == workspace.currentNetwork || column == 3,
      let attempted, attempted >= started
    {
      return nil
    }
    return column
  }
  var displayedIPs: [String] { visibleRows.map(\.ip) }
  var canStopTask: Bool { taskRunning || !unconfirmedOperation.isEmpty }
  func stop() async {
    let id = workspace.operationID.isEmpty ? unconfirmedOperation : workspace.operationID
    guard !id.isEmpty else { return }
    do {
      try await cancel(id)
      await refresh()
    } catch { self.error = ProductErrorPresentation.display(error) }
  }
  func dismiss() {
    generation &+= 1
    releaseTick()
    loading = false
    let id = ownedOperation
    ownedOperation = ""
    if !id.isEmpty { Task { try? await cancel(id) } }
  }
  func exportJSON() throws -> Data {
    let encoder = JSONEncoder()
    encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
    encoder.dateEncodingStrategy = .iso8601
    return try encoder.encode(workspace.records)
  }
  func exportCSV() -> String {
    let header = "IP,网段,IP地区,地区来源,地区时间,网络,TCP RTT(ms),RTT时间,CF下载(B/s),下载时间,线路验证(ms),验证时间,最近错误\n"
    let lines = workspace.records.map { row in
      [
        row.ip, row.prefix,
        row.location.map {
          [$0.country, $0.region, $0.city].filter { !$0.isEmpty }.joined(separator: " · ")
        } ?? "", row.location?.source ?? "", MeasurementTimestamp.text(row.location?.at),
        row.network, String(row.tcp.value),
        MeasurementTimestamp.text(row.tcp.measuredAt), String(row.download.value),
        MeasurementTimestamp.text(row.download.measuredAt), String(row.validation.value),
        MeasurementTimestamp.text(row.validation.measuredAt), row.issue,
      ].map { "\"" + $0.replacingOccurrences(of: "\"", with: "\"\"") + "\"" }.joined(separator: ",")
    }
    return header + lines.joined(separator: "\n")
  }
}
