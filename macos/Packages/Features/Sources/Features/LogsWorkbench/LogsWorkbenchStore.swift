import Foundation
import Network
import Observation
import PaxCore

/// 隔离的日志浏览状态；仅内存样例，不采集本机日志或调用 daemon。
@MainActor @Observable
final class LogsWorkbenchStore {
  var isLoading = false
  var pageLoading = false
  var pageIndex = 0
  var requestedPageIndex: Int?
  var requestedObject: String?
  var followsLatestPage: Bool { (requestedPageIndex ?? pageIndex) == 0 && !reading }
  var pageCursors = [""]
  var reading = false
  var atTop = true
  var canPreviousPage: Bool { pageIndex > 0 && canChangePage }
  var canNextPage: Bool { !eventCursor.isEmpty && canChangePage }
  private var canChangePage: Bool {
    !privacyInvalid && requestedObject == nil && !pageLoading && !isLoading
  }
  var pageTitle: String { pageTitle(context: .chinese) }
  func pageTitle(context: PresentationContext) -> String {
    if privacyInvalid {
      return liveMessage.isEmpty
        ? context.text("logs_presentation.9df987ee3c")
        : context.text("logs_presentation.42119368ca")
    }
    if telemetry != nil && !initialized {
      return liveMessage.isEmpty
        ? context.text("logs_presentation.c8a5375f98")
        : context.text("logs_presentation.42119368ca")
    }
    return context.text(
      "logs_presentation.a02251e150",
      [
        "arg0": String(describing: pageIndex + 1),
        "arg1": String(describing: max(1, Int((recordTotal + 99) / 100))),
        "arg2": String(describing: recordTotal),
      ])
  }
  func changePage(_ delta: Int) {
    let target = pageIndex + delta
    guard canChangePage, target >= 0, delta < 0 || canNextPage else { return }
    requestedPageIndex = target
    reading = target > 0
    let cursor: String
    if delta > 0 {
      cursor = eventCursor
      if pageCursors.count <= target {
        pageCursors.append(cursor)
      } else {
        pageCursors[target] = cursor
      }
    } else {
      cursor = pageCursors[target]
    }
    queryTask?.cancel()
    requestGeneration &+= 1
    let request = requestGeneration
    pageLoading = true
    queryTask = Task { [weak self] in
      guard let self else { return }
      defer { if request == self.requestGeneration { self.pageLoading = false } }
      await self.reload(
        eventsOnly: true, pageCursor: cursor, pageTarget: target, request: request,
        recoverPage: self.usesApplicationPages && target > 0 && cursor.isEmpty)
    }
  }
  var recordTotal: UInt64 = 0
  var hasViewUpdates = false
  var usesViews: Bool { telemetry?.logViewsAvailable == true }
  var usesApplicationPages: Bool {
    dimension == .application && usesViews && telemetry?.applicationWorkbenchPageAvailable == true
  }
  var unit: String { unit(context: .chinese) }
  func unit(context: PresentationContext) -> String { context.text("logs_presentation.6016bf63b0") }
  enum Dimension: Int, CaseIterable {
    case domain, application, ip, system
    var title: String { title(context: .chinese) }
    func title(context: PresentationContext) -> String {
      [
        context.text("logs_presentation.2229524311"), context.text("logs_presentation.63c73c4730"),
        "IP", context.text("logs_presentation.5b50d7c4b5"),
      ][rawValue]
    }
    func value(_ event: LogPreviewEvent) -> String? {
      switch self {
      case .domain: event.domain
      case .application: event.application
      case .ip: event.ip
      case .system: nil
      }
    }
    var unknown: String { unknown(context: .chinese) }
    func unknown(context: PresentationContext) -> String {
      self == .application
        ? context.text("logs_presentation.0a1cf0dcc8")
        : context.text("logs_presentation.542629d51f", ["arg0": title(context: context)])
    }
  }
  enum Level: Int, CaseIterable {
    case all, error, warning, info
    var title: String { title(context: .chinese) }
    func title(context: PresentationContext) -> String {
      [
        context.text("logs_presentation.81f9b55545"), context.text("logs_presentation.0bc1fb72ae"),
        context.text("logs_presentation.a8b7a44804"), context.text("logs_presentation.e7028601e7"),
      ][rawValue]
    }
  }
  enum Scene: String, CaseIterable {
    case normal = "正常"
    case empty = "无日志"
    case loading = "加载中"
    case failed = "读取失败"
    case disconnected = "后台断开"
    case missing = "对象字段缺失"
  }
  enum Intent {
    case dimension(Dimension)
    case search(String)
    case object(String)
    case level(Level)
    case select(String?)
    case scene(Scene)
  }
  struct ObjectRow: Equatable {
    let id: String
    let title: String
    let count: Int
    var symbol: String = ""
  }
  @ObservationIgnored var queryTask: Task<Void, Never>?
  // The bounds of the committed snapshot survive changes to the next query.
  var seenSnapshot: (history: HistoryState, from: Date?, session: UInt64)?
  var resetDirectoryOrder = true
  var directoryOrder: [String] = []
  var initialized = false
  var revokedIdentity: String?
  var invalidationRecovery = false
  var rangeEnd: Date? = nil
  var privacyInvalid: Bool {
    guard let telemetry, let seenSnapshot else { return false }
    return telemetry.logsInvalidated(since: seenSnapshot.history, from: seenSnapshot.from)
      || (telemetry.connected && seenSnapshot.session != telemetry.sessionGeneration)
  }
  var telemetry: TelemetryStore?
  var liveRecords: [LogRecord] = []
  var visibleRecords: [LogRecord] { privacyInvalid ? [] : liveRecords }
  var liveObjects: [ObjectRow] = []
  var liveTotal: UInt64 = 0
  var eventCursor = ""
  var objectCursor = ""
  var readToken = ""
  var directoryToken = ""
  var directoryStart: Date? = nil
  var directoryEnd: Date? = nil
  var directoryHistory: HistoryState?
  var directoryIsSyncing: Bool {
    usesApplicationPages && !readToken.isEmpty && directoryToken != readToken
  }
  var requestGeneration: UInt64 = 0
  var logReadMessage = RetainedProductMessage(original: "")
  var rangeIndex = 0
  var includeDNS = false
  private var directoryMemory: [Dimension: (query: String, object: String)] = [:]
  var allTitle: String { allTitle(context: .chinese) }
  func allTitle(context: PresentationContext) -> String {
    dimension == .system
      ? context.text("logs_presentation.4c101e3d34")
      : dimension == .domain
        ? context.text("logs_presentation.161c3b1e06")
        : context.text("logs_presentation.93e7f401b6")
  }
  var rangeStart: Date? = nil
  var fromDate: Date? {
    (rangeEnd ?? Date()).addingTimeInterval(-[1800.0, 21600, 86400][min(2, max(0, rangeIndex))])
  }
  private let sample: [LogPreviewEvent]
  let backendAvailable: Bool
  private(set) var dimension: Dimension = .application
  private(set) var query = ""
  private(set) var selectedObject = "all"
  private(set) var level: Level = .all
  @ObservationIgnored private(set) var selectedEvent: String?
  private(set) var scene: Scene = .normal
  init(events: [LogPreviewEvent] = LogPreviewEvent.samples(), backendAvailable: Bool = true) {
    self.backendAvailable = backendAvailable
    sample = backendAvailable ? events : []
  }
  var events: [LogPreviewEvent] {
    if telemetry != nil || dimension == .system { return [] }
    switch scene {
    case .empty, .loading, .failed: return []
    case .missing:
      return sample.map {
        var event = $0
        event.application = nil
        event.domain = nil
        return event
      }
    case .normal, .disconnected: return sample
    }
  }
  var objects: [ObjectRow] {
    if telemetry != nil {
      if privacyInvalid {
        return []
      }
      if directoryIsSyncing, let telemetry, let directoryHistory,
        telemetry.logsInvalidated(since: directoryHistory, from: directoryStart)
      {
        return []
      }
      return [
        .init(id: "all", title: allTitle, count: Int(liveTotal))
      ] + liveObjects
    }
    let objects = events.compactMap { event in dimension.objectValue(event).map { ($0, event) } }
    let grouped = Dictionary(grouping: objects) { $0.0 }
    let rows = grouped.map { key, value in
      ObjectRow(id: key, title: key, count: value.count)
    }.sorted {
      if $0.count != $1.count { return $0.count > $1.count }
      let a = $0.title.lowercased()
      let b = $1.title.lowercased()
      return a == b ? $0.id < $1.id : a.utf8.lexicographicallyPrecedes(b.utf8)
    }
    return [
      .init(id: "all", title: allTitle, count: events.count)
    ]
      + rows.filter {
        query.isEmpty || $0.title.localizedStandardContains(query)
      }
  }
  var visibleEvents: [LogPreviewEvent] {
    if telemetry != nil { return [] }
    return events.filter {
      (selectedObject == "all" || dimension.objectValue($0) == selectedObject)
        && (level == .all || $0.level == level)
    }.sorted { $0.sequence > $1.sequence }
  }
  var objectTitle: String { objectTitle(context: .chinese) }
  func objectTitle(context: PresentationContext) -> String {
    if privacyInvalid { return context.text("logs_presentation.0bcb7c62fd") }
    return selectedObject == "all"
      ? allTitle(context: context)
      : selectedObject == "unknown" ? dimension.unknown(context: context) : selectedObject
  }
  var message: String { message(context: .chinese) }
  func message(context: PresentationContext) -> String {
    if privacyInvalid {
      return liveMessage.isEmpty
        ? context.text("logs_presentation.9df987ee3c") : liveMessageText(context: context)
    }
    if telemetry != nil && !initialized && liveMessage.isEmpty {
      return context.text("logs_presentation.87e0cec6c5")
    }
    if let telemetry {
      return liveMessage.isEmpty
        ? telemetry.qualityMessage(context: context) : liveMessageText(context: context)
    }

    return switch scene {
    case .loading: context.text("logs_presentation.1718310516")
    case .failed: context.text("logs_presentation.a78489a3a5")
    case .disconnected: context.text("logs_presentation.f0f2133c79")
    default: ""
    }
  }
  var emptyMessage: String { emptyMessage(context: .chinese) }
  func emptyMessage(context: PresentationContext) -> String {
    if scene == .loading || scene == .failed { return message(context: context) }
    if telemetry != nil {
      if privacyInvalid || !liveMessage.isEmpty || !initialized {
        return message(context: context).isEmpty
          ? context.text("logs_presentation.87e0cec6c5") : message(context: context)
      }
      return context.text("logs_presentation.5174f68f19")
        + (selectedObject == "all"
          ? dimension.title(context: context)
          : objects.first(where: { $0.id == selectedObject })?.title
            ?? dimension.title(context: context)) + context.text("logs_presentation.9fad24e2c3")
    }
    return events.isEmpty
      ? context.text("logs_presentation.10fafbeaa7") : context.text("logs_presentation.17f13d145b")
  }
  func perform(_ intent: Intent) {
    switch intent {
    case .dimension(let value):
      resetDirectoryOrder = true
      directoryMemory[dimension] = (query, selectedObject)
      dimension = value
      liveRecords = []
      liveObjects = []
      liveTotal = 0
      recordTotal = 0
      objectCursor = ""
      eventCursor = ""
      directoryToken = ""
      pageIndex = 0
      pageCursors = [""]
      query = directoryMemory[value]?.query ?? ""
      selectedObject = directoryMemory[value]?.object ?? "all"
      selectedEvent = nil
      initialized = false
    case .search(let value):
      resetDirectoryOrder = true
      query = value
    case .object(let value):
      guard objects.contains(where: { $0.id == value }) else { return }
      if telemetry == nil {
        commitObjectSelection(value)
      } else if value != (requestedObject ?? selectedObject) {
        liveMessage = ""
        scheduleReload(objectTarget: value)
      }
      return
    case .level(let value):
      level = value
      liveRecords = []
      recordTotal = 0
      initialized = false
      reading = false
      selectedEvent = nil
    case .select(let id):
      selectedEvent = id
      reading = id != nil || !atTop || pageIndex > 0
    case .scene(let value):
      scene = value
      selectedObject = "all"
      selectedEvent = nil
    }
    guard telemetry != nil else { return }
    switch intent {
    case .search:
      scheduleReload(directoryOnly: true, delay: true)
    case .level, .dimension:
      liveMessage = ""
      resetRange()
    case .object, .select, .scene:
      break
    }
  }

  func commitObjectSelection(_ value: String) {
    selectedObject = value
    selectedEvent = nil
    pageIndex = 0
    pageCursors = [""]
    reading = false
  }
}

struct LogPreviewEvent: Identifiable, Sendable, Equatable {
  var title: String { title(context: .chinese) }
  func title(context: PresentationContext) -> String { kind.title }
  enum Kind: Int, CaseIterable, Sendable {
    case timeout, rule, dns, started, closed, dnsFailure
    var symbol: String {
      self == .timeout || self == .dnsFailure ? "exclamationmark.circle" : "info.circle"
    }
    var title: String { title(context: .chinese) }
    func title(context: PresentationContext) -> String {
      ["连接超时", "已选择线路", "已找到服务器地址", "开始连接", "连接已关闭", "域名解析失败"][rawValue]
    }
  }
  let id: String
  let sequence: Int
  let time: String
  var application: String?
  var domain: String?
  let ip: String?
  let route: String
  let kind: Kind
  var level: LogsWorkbenchStore.Level {
    return switch kind {
    case .timeout: .error
    case .dnsFailure: .warning
    default: .info
    }
  }
  var target: String {
    return ip.map { $0 + ":443" } ?? "未记录"
  }
  var contextID: String { String(506_000_000 + sequence) }
  var duration: String { String(format: "%.2fs", Double(234 + sequence * 137 % 1600) / 100) }
  var localSocket: String { "192.0.2.100:\(56000 + sequence)" }
  var result: String {
    return switch kind {
    case .timeout: "本次连接失败；后续是否恢复未记录。"
    case .dnsFailure: "本次域名解析失败；后续是否恢复未记录。"
    case .started: "连接已发起；结果尚未记录。"
    default: summary
    }
  }
  var summary: String {
    return switch kind {
    case .timeout: "连接失败。等待 \(duration) 后超时。"
    case .rule: "使用\(route)访问此目标。"
    case .dns: "已查到服务器地址。"
    case .started: "正在连接服务器。结果尚未记录。"
    case .closed: "本次连接已结束。"
    case .dnsFailure: "未查到服务器地址。无法开始连接。"
    }
  }
  var raw: String {
    let target = domain ?? ip ?? "unknown"
    let message: String
    switch kind {
    case .timeout: message = "connection: read tcp \(localSocket)->\(self.target): i/o timeout"
    case .rule: message = "router: match domain=\(target) => outbound=\(route)"
    case .dns: message = "dns: exchanged A \(target) answer=\(ip ?? "unknown")"
    case .started: message = "outbound: outbound connection to \(target):443"
    case .closed: message = "connection: connection closed"
    case .dnsFailure: message = "dns: exchange failed for \(target): timeout"
    }
    return
      "+0800 2026-10-13 \(time) \(level == .error ? "ERROR" : level == .warning ? "WARN" : "INFO") [\(contextID) \(duration)] \(message)"
  }

}

extension LogsWorkbenchStore.Dimension {
  var iconKind: WorkbenchObjectIcons.Kind {
    switch self {
    case .domain: .domain
    case .application: .application
    case .ip: .ip
    case .system: .application
    }
  }
  func objectValue(_ event: LogPreviewEvent) -> String? {
    guard let raw = value(event)?.trimmingCharacters(in: .whitespacesAndNewlines), !raw.isEmpty
    else { return nil }
    switch self {
    case .system: return nil
    case .application: return ["未识别应用", "未识别的应用", "unknown"].contains(raw) ? nil : raw
    case .ip: return IPv4Address(raw) != nil || IPv6Address(raw) != nil ? raw : nil
    case .domain:
      guard IPv4Address(raw) == nil, IPv6Address(raw) == nil,
        raw.range(of: "^[a-zA-Z0-9_\\-]+(?:\\.[a-zA-Z0-9_\\-]+)*\\.?$", options: .regularExpression)
          != nil
      else { return nil }
      return raw.lowercased().trimmingCharacters(in: CharacterSet(charactersIn: "."))
    }
  }
}

extension LogsWorkbenchStore {
  func recordIdentity(_ record: LogRecord, context: PresentationContext = .chinese) -> String {
    guard let flow = record.flow else {
      return record.kind == "operation"
        ? "Pax Connect → "
          + (record.systemEvent.map { ProductTextPresentation.eventName($0, context: context) }
            ?? ProductTextPresentation.recordTitle(record, context: context))
        : context.text("logs_presentation.db809aae35")
    }
    if dimension == .domain {
      let domain = flow.domain.isEmpty ? context.text("logs_presentation.421f95faeb") : flow.domain
      let address: String
      if flow.ip.isEmpty {
        address =
          flow.ipSource == "proxy_domain"
          ? context.text("logs_presentation.4cf80028f6")
          : context.text("logs_presentation.6a47cd39fa")
      } else {
        var ipFlow = flow
        ipFlow.domain = ""
        address = FlowPresentation.target(ipFlow, context: context)
      }
      return domain + " → " + address
    }
    return LogDetailPresentation.application(record, context: context) + " → "
      + FlowPresentation.target(flow, context: context)
  }
  func restoreDirectorySelection(_ availableID: String?) {
    if selectedObject != "all" && availableID != selectedObject {
      selectedObject = "all"
      directoryMemory[dimension] = (query, "all")
    }
  }

  func eventIdentity(_ event: LogPreviewEvent, context: PresentationContext = .chinese) -> String {
    return (event.application ?? context.text("logs_presentation.9465a6fde8")) + " → "
      + (event.domain ?? event.ip ?? context.text("logs_presentation.947d020039"))
  }
}
