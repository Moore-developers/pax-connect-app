import CoreFoundation
import CryptoKit
import Darwin
import Foundation
import Observation
import PaxCore
import PaxUI

/// UI 阶段隔离的目录与编辑会话；节点复用 NodeProfile，附加数据仅用于本地呈现。
@MainActor @Observable
final class RouteWorkbenchStore {
  struct Measurement: Equatable, Sendable {
    var value: Double?
    var failure: String?
    var historical = false
    var time: String?
    var running = false
    var samples: [Double] = []
    var text: String {
      if historical && !running { return "历史测量，待更新" }
      return running
        ? (time == "排队等待" ? "排队等待" : "测试中…")
        : failure ?? value.map(MeasurementFormatting.number) ?? "—"
    }
    var caption: String {
      if historical && !running { return (time.map { $0 + " · " } ?? "") + "历史测量，待更新" }
      return running
        ? (time == "排队等待" ? "排队等待" : "等待结果")
        : time.map { $0 + (failure == nil ? " · 测试" : " · 失败") } ?? "未测试"
    }
  }
  struct Presentation: Equatable {
    var exitIP: String?
    var exitLocation: ExitLocation?
    var lastExitLocation: ExitLocation?
    var exitMessage: String?
    var exitAt: Date?
    var latencyAt: Date?
    var cfIP: String?
    var cfPreferred = false
    var latency = Measurement()
    var firstConnection = Measurement()
    var speed = Measurement()
    var cfLatency = Measurement()
    var uploadMB: Double?
    var downloadMB: Double?
    var activeConnections: Int?
    /// 所选时间窗内新建的连接总数；与 activeConnections（此刻打开的连接）不同。
    var windowConnections: UInt64?
    var transferSamples: PaxTimeSeries?
    var chain: Chain?
    var override = false
  }
  struct Chain: Equatable {
    var front: String
    var final: String
  }
  struct Group: Equatable, Identifiable {
    enum Kind: Equatable { case custom, subscription }
    let id: String
    var name: String
    var members: Set<String>
    var kind: Kind = .custom
    var displayTitle: String { "\(name.isEmpty ? "新分组" : name)（\(members.count)）" }
  }
  struct Candidate: Equatable, Identifiable, Sendable {
    var id: String { ip }
    let ip: String
    var measurement: Measurement
    var network = ""
    var validUntil: Date?
  }
  struct Draft: Equatable, Sendable {
    var id: String
    var revision: UInt64 = 0
    var name: String
    var server: String
    var port: String
    var host: String
    var sni: String
    var cf = false
    var recordFragment = false
    var preferredIP: String?
    var json: String
    var frontID: String?
    var frozenFront: NodeProfile?
    var frozenFinal: NodeProfile?
    var chainContext = ""
    var chainRevision: UInt64 = 0
  }
  enum Sort: String, CaseIterable {
    case source = "默认顺序"
    case name = "名称"
    case nameDown = "名称 ↓"
    case featureUp = "特性 ↑"
    case featureDown = "特性 ↓"
    case addressUp = "出口地址 ↑"
    case addressDown = "出口地址 ↓"
    case latencyUp = "延迟 ↑"
    case latencyDown = "延迟 ↓"
    case speedUp = "速度 ↑"
    case speedDown = "速度 ↓"
  }
  var sortColumn: Int? {
    switch sort {
    case .source: nil
    case .name, .nameDown: 1
    case .featureUp, .featureDown: 2
    case .addressUp, .addressDown: 3
    case .latencyUp, .latencyDown: 4
    case .speedUp, .speedDown: 5
    }
  }
  var sortAscending: Bool {
    ![Sort.nameDown, .featureDown, .addressDown, .latencyDown, .speedDown].contains(sort)
  }
  func sortByColumn(_ column: Int, ascending: Bool) {
    switch column {
    case 1: perform(.sort(ascending ? .name : .nameDown))
    case 2: perform(.sort(ascending ? .featureUp : .featureDown))
    case 3: perform(.sort(ascending ? .addressUp : .addressDown))
    case 4: perform(.sort(ascending ? .latencyUp : .latencyDown))
    case 5: perform(.sort(sortColumn == 5 && ascending ? .speedUp : .speedDown))
    default: break
    }
  }
  enum Test { case latency, exit, speed, cf }
  enum Intent {
    case search(String)
    case group(String)
    case sort(Sort)
    case use(String)
    case test(String, Test)
    case refresh
    case remove(String)
  }

  private let session: WorkbenchDemoStore?
  let routes: RoutesStore?
  let connectionStore: ConnectionStore?
  private(set) var nodes: [NodeProfile] {
    get { routes?.routes ?? session?.catalog.routes ?? [] }
    set {
      guard routes == nil else { return }
      session?.replaceRouteCatalog(newValue)
    }
  }
  var isLive: Bool { routes != nil }
  var physicalNetwork: String {
    routes?.connected == true ? routes?.physicalEgress?.network ?? "" : ""
  }
  var hasCFDisplayedBatch: Bool {
    routes?.capabilities.contains("routes.cf.displayed_batch.v1") == true
  }
  var hasCFRegions: Bool { routes?.capabilities.contains("routes.cf.regions.v1") == true }
  var hasCFSingleTests: Bool { routes?.capabilities.contains("routes.cf.single") == true }
  var hasCFWorkspace: Bool { routes?.capabilities.contains("routes.cf.workspace") == true }
  func cfRequest(_ request: MeasurementRequest) async throws -> MeasurementReply {
    guard let routes else { throw EditError("CF 工作台尚未连接后台") }
    return try await routes.measurement(request)
  }
  func cancelCFOperation(_ id: String) async throws {
    try await routes?.cancelMeasurementOperation(id)
  }
  var hasCFCandidates: Bool { routes?.capabilities.contains("routes.cf.candidates") == true }
  func refreshPhysical() async {
    guard let routes else { return }
    _ = try? await routes.measurement(.refreshEgress)
  }
  var physicalCaption: String {
    guard let value = routes?.physicalEgress else { return "本机公网出口尚未获取" }
    return value.phase == "ready"
      ? "本机出口 IP · \(MeasurementTimestamp.text(value.at))"
      : "本机出口 IP · \(value.phase=="loading" ? "检测中" : "未获取")"
  }
  func downloadCaption(_ id: String) -> String {
    guard let value = node(id)?.downloadSummary else { return "" }
    guard value.method == "window6s-v2" else { return "下载记录来自旧测量口径" }
    return
      "下载正文平均值 · \(value.parallel) 路并发 · 已接收 \(TrafficFormatting.bytes(value.bytes)) · 采样 \(MeasurementFormatting.latency(Double(value.durationMS))?.text ?? "—") · 准备 \(MeasurementFormatting.latency(Double(value.setupMS))?.text ?? "—")"
  }
  func measurementCaption(_ id: String) -> String {
    guard let summary = node(id)?.testSummary else { return "首次包含线路与目标HTTPS连接建立；稳定延迟为同会话三次请求的中位数。" }
    guard summary.method == "https204-median-v3" else { return "历史延迟采用旧口径，重新测试后分别记录首次连接与稳定延迟。" }
    let target = summary.testTarget == "gstatic" ? "Google gstatic" : "Google"
    let samples =
      summary.stableSamples == 3 && summary.stableError.isEmpty
      ? "同会话3次 · 中位数" : "稳定样本不足（\(summary.stableSamples)/3），首次结果单独保留"
    return "\(target) HTTPS 首响应 · \(samples)；包含到测试网站的路径，不是服务器IP的ping。"
  }
  var currentRouteID: String? { connectionStore?.currentRoute?.id ?? session?.currentRouteID }
  var isRunning: Bool {
    if let connectionStore {
      return connectionStore.status == .protected || connectionStore.status == .degraded
    }
    return session?.isRunning ?? false
  }
  var isStarting: Bool { connectionStore?.proxyStarting == true || session?.scenario == .starting }
  var canSelectRoute: Bool {
    if let connectionStore { return connectionStore.canSelectRoute }
    return session?.canOperate ?? false
  }
  var canEdit: Bool {
    routes.map {
      $0.canWrite && $0.capabilities.contains("routes.documents")
        && connectionStore?.serviceUpdating == false
    } ?? true
  }
  var canImport: Bool {
    routes.map {
      $0.canWrite && $0.capabilities.contains("routes.import.auto")
        && connectionStore?.serviceUpdating == false
    } ?? true
  }
  func makeImportSession() -> RouteImportSession {
    if let routes { return RouteImportSession(routes: routes) }
    return RouteImportSession(
      preview: { [self] text, _, _ in
        let nodes = try importPreview(text)
        return RoutesPreview(
          token: "demo", revision: 0,
          items: nodes.enumerated().map { index, node in
            RoutePreviewItem(
              id: node.id, route: node, errorCode: "", duplicate: false, line: UInt32(index + 1))
          })
      }, cancel: { _ in },
      confirm: { [self] preview, selected in
        commitImport(preview.items.filter { selected.contains($0.id) }.compactMap(\.route))
      })
  }
  func canTest(_ id: String) -> Bool {
    guard let routes else { return true }
    return routes.canWrite && routes.capabilities.contains("routes.test.single")
      && !routes.isTesting(id) && node(id)?.hidden == false
      && connectionStore?.serviceUpdating == false
  }
  func canDiagnose(_ id: String) -> Bool {
    canTest(id) && routes?.capabilities.contains("routes.diagnostics") == true
  }
  func diagnose(_ id: String) {
    guard let routes, let node = node(id), canDiagnose(id) else { return }
    Task { await routes.perform(.diagnose(node)) }
  }
  func canDownload(_ id: String) -> Bool {
    guard let routes else { return true }
    return routes.canWrite && routes.capabilities.contains("routes.download.single")
      && (routes.downloadOperationID.isEmpty || routes.downloadQueue?.mode == "single")
      && node(id)?.hidden == false
      && connectionStore?.serviceUpdating == false
  }
  func isDownloadQueued(_ id: String) -> Bool {
    routes?.downloadQueue?.queuedRouteIDs.contains(id) == true
  }
  func isDownloading(_ id: String) -> Bool { routes?.downloadingRouteIDs.contains(id) == true }
  var hasLatencyTests: Bool { routes?.hasLatencyTests == true }
  func stopTests(_ id: String? = nil) {
    guard let routes else { return }
    Task { await routes.cancelTests(routeID: id) }
  }
  func stopDownload(_ id: String? = nil) {
    guard let routes, !routes.downloadOperationID.isEmpty else { return }
    Task { await routes.cancelDownload(routeID: id) }
  }
  var certificateFailures: [NodeProfile] { routes?.certificateFailures ?? [] }
  func confirmCertificateFailures(_ frozen: [NodeProfile]) async throws {
    guard let routes else { return }
    try await routes.confirmTLSBatch(frozen)
  }
  var canConfirmCertificateFailures: Bool {
    routes?.capabilities.contains("routes.tls.batch") == true && routes?.canWrite == true
      && connectionStore?.serviceUpdating == false
  }
  var canManage: Bool { !isLive || routes?.capabilities.contains("routes.chains") == true }
  var canManageGroups: Bool {
    routes.map {
      $0.canWrite && $0.capabilities.contains("routes.groups")
        && connectionStore?.serviceUpdating == false
    } ?? true
  }
  var catalogRevision: UInt64 { routes?.catalogRevision ?? 0 }
  var canToggleProxy: Bool { connectionStore?.canToggleProxy ?? (session?.canOperate ?? false) }
  var statusMessage: String {
    if let routes {
      // Failures have one owner in the shared header. Keep only non-error task feedback here.
      if !routes.connected { return "" }
      if connectionStore?.unresolvedFeedback.contains(where: { $0.detail == routes.message })
        == true
      {
        return ""
      }
      return message.isEmpty ? routes.message : message
    }
    return message
  }
  func toggleProxy() {
    guard !isLive, canToggleProxy else { return }
    session?.perform(.toggleProxy)
  }

  var canBatchTest: Bool {
    routes.map { $0.canWrite && $0.capabilities.contains("routes.test.batch") } ?? true
  }
  var canBatchExit: Bool {
    canBatchTest && (routes?.capabilities.contains("routes.measure.exit") ?? true)
      && connectionStore?.serviceUpdating != true
  }
  var canBatchDownload: Bool {
    canBatchTest
      && (routes.map {
        $0.capabilities.contains("routes.download.single") && $0.downloadOperationID.isEmpty
      } ?? true)
  }
  var hasDownload: Bool { routes?.downloadOperationID.isEmpty == false }
  var downloadParallel: Int { Int(routes?.measurementPreferences?.download ?? 8) }
  func performTest(_ frozen: [NodeProfile], kind: Test) {
    guard !frozen.isEmpty else { return }
    if let routes {
      Task {
        if kind == .exit {
          await routes.perform(.queryExitBatch(frozen))
        } else {
          await routes.performBatch(frozen, download: kind == .speed)
        }
      }
    } else {
      for route in frozen {
        perform(.test(route.id, kind))
      }
    }
  }
  /// 线路详情的流量信号刷新（`.routeDetail`）；nil（演示 / 测试装配）时只在打开与用户操作时读。
  @ObservationIgnored var refresh: PaxRefresh?
  init(routes: RoutesStore, connection: ConnectionStore, refresh: PaxRefresh? = nil) {
    self.refresh = refresh
    self.routes = routes
    self.connectionStore = connection
    self.session = nil
  }
  private(set) var presentations: [String: Presentation] = [:]
  private(set) var originals: [String: NodeProfile] = [:]
  private var configs: [String: String] = [:]
  private(set) var originalJSON: [String: String] = [:]
  private var demoGroups: [Group] = []
  private(set) var groups: [Group] {
    get {
      routes?.groups.map { Group(id: $0.id, name: $0.name, members: Set($0.memberIDs)) }
        ?? demoGroups
    }
    set { if routes == nil { demoGroups = newValue } }
  }
  private(set) var search = ""
  private(set) var selectedGroup = "all"
  private(set) var sort: Sort = .source
  var message = ""
  private(set) var demoRefreshing = false
  var refreshing: Bool {
    guard let routes else { return demoRefreshing }
    return currentSubscription.map { routes.isRefreshing($0.id) } ?? false
  }
  var currentSubscription: SubscriptionSummary? { subscription(selectedGroup) }
  func subscription(_ groupID: String) -> SubscriptionSummary? {
    guard groupID.hasPrefix("subscription:") else { return nil }
    return routes?.subscriptions.first { "subscription:" + $0.id == groupID }
  }
  func canRefreshSubscription(_ groupID: String) -> Bool {
    guard let routes, let source = subscription(groupID) else { return false }
    return routes.canWrite && !routes.isRefreshing(source.id)
      && routes.capabilities.contains("subscriptions.refresh")
  }
  var refreshRoute: NodeProfile? { routes?.selectedRoute }
  var canRefreshViaRoute: Bool {
    routes?.capabilities.contains("subscriptions.refresh.route") == true && refreshRoute != nil
  }
  func refreshSubscription(_ groupID: String, viaRoute: Bool) {
    guard let routes, canRefreshSubscription(groupID), let source = subscription(groupID) else {
      return
    }
    if viaRoute {
      guard canRefreshViaRoute, let route = refreshRoute else { return }
      Task { await routes.perform(.refreshVia(source, route)) }
    } else {
      Task { await routes.perform(.refresh(source)) }
    }
  }
  func refreshSavedSubscription(_ groupID: String) {
    guard let routes, canRefreshSubscription(groupID), let source = subscription(groupID) else {
      return
    }
    Task { await routes.perform(.refreshSaved(source)) }
  }
  func replaceSubscription(_ groupID: String, address: String, agent: String) {
    guard let routes, canRefreshSubscription(groupID), let source = subscription(groupID) else {
      return
    }
    Task { await routes.perform(.replaceSource(source, address, agent)) }
  }
  var canEditSubscriptionSource: Bool {
    routes?.capabilities.contains("subscriptions.source.edit") == true
  }
  func subscriptionStatus(_ groupID: String) -> String {
    guard let source = subscription(groupID) else { return "" }
    if routes?.isRefreshing(source.id) == true { return "正在后台刷新…" }
    var text = source.lastError.map(ProductErrorPresentation.message) ?? "刷新仅更新此订阅；本地覆盖与隐藏记录保留"
    if let attempt = source.lastAttempt {
      let phase =
        [
          "dns": "DNS", "connect": "连接", "tls": "TLS", "headers": "等待响应", "body": "下载正文",
          "response": "服务器响应", "parse": "解析", "complete": "完成",
        ][attempt.phase] ?? "请求"
      text +=
        " · " + MeasurementTimestamp.text(attempt.at) + " · "
        + (attempt.channel == "route" ? "指定线路" : "系统网络") + " · " + phase
      if attempt.status > 0 { text += " HTTP " + String(attempt.status) }
      text += " · " + (MeasurementFormatting.latency(Double(attempt.totalMS))?.text ?? "—")
    }
    return text
  }
  let cfEntryTests = RouteCFEntryTests()
  private var refreshTask: Task<Void, Never>?
  private var tasks: [String: Task<Void, Never>] = [:]
  private var testTokens: [String: UUID] = [:]
  init(session: WorkbenchDemoStore) {
    self.session = session
    self.routes = nil
    self.connectionStore = nil
    let fixtureItems = session.qualityItems
    nodes = []
    for (index, item) in fixtureItems.enumerated() {
      let cf = item.protocolName == "VMess"
      let node = NodeProfile(
        id: item.id, name: item.name, protocolName: item.protocolName.lowercased(),
        server: cf ? "172.64.79.122" : "198.51.100.\(30 + index)", port: 443, group: item.source,
        serverName: "edge.example.net", revision: 1,
        transport: cf ? "ws" : item.protocolName == "Hysteria2" ? "quic" : "tcp", security: "tls",
        sourceID: "rn", origin: "subscription")
      nodes.append(node)
      let transfers = demoRouteTransferSeries()
      presentations[node.id] = Presentation(
        exitIP: "198.51.100.24", cfIP: cf ? node.server : nil, cfPreferred: cf,
        latency: Measurement(
          value: item.samples.last?.milliseconds, time: "今日 14:30",
          samples: item.samples.compactMap(\.milliseconds)),
        speed: Measurement(
          value: index == 1 ? 1.9 : 4.1, time: "今日 14:18", samples: [3.4, 3.8, 3.6, 4.2, 3.9, 4.1]),
        cfLatency: cf
          ? Measurement(value: 38, time: "今日 14:20", samples: [44, 42, 47, 43, 40, 38])
          : Measurement(), uploadMB: 220, downloadMB: 2180, activeConnections: 24,
        windowConnections: 312, transferSamples: transfers)
    }
    for (id, name, proto, transport) in [
      ("hk", "香港 · 标准", "vless", "tcp"), ("jp", "东京 · 标准", "trojan", "tcp"),
      ("new", "台北 · 新增", "vless", "ws"), ("failed", "大阪 · 下载失败", "vmess", "ws"),
      ("tls-error", "首尔 · TLS 异常", "trojan", "tcp"), ("ss", "香港 · AEAD", "shadowsocks", "tcp"),
    ] {
      nodes.append(
        NodeProfile(
          id: id, name: name, protocolName: proto, server: "203.0.113.\(50 + nodes.count)",
          port: 443, group: "本地线路", serverName: "edge.example.net", revision: 1,
          transport: transport, security: proto == "shadowsocks" ? "none" : "tls", origin: "local"))
      presentations[id] =
        id == "new"
        ? Presentation()
        : Presentation(
          exitIP: "203.0.113.60",
          latency: Measurement(
            value: id == "tls-error" ? nil : 68, failure: id == "tls-error" ? "证书验证失败" : nil,
            time: "今日 14:30"),
          speed: Measurement(
            value: id == "failed" || id == "tls-error" ? nil : 6.2,
            failure: id == "failed" ? "下载请求超时" : nil, time: id == "failed" ? "今日 14:31" : nil))
    }
    groups = [
      Group(id: "work", name: "工作", members: ["vless-fast", "hk", "vmess-primary"]),
      Group(id: "media", name: "影音", members: ["vmess", "jp"]),
      Group(id: "backup", name: "备用", members: ["hy2", "new", "failed"]),
    ]
    syncSession()
  }
  /// 订阅成员由既有来源ID派生，自定义成员仍由用户维护。
  var subscriptionGroups: [Group] {
    if let routes {
      return routes.subscriptions.map { source in
        Group(
          id: "subscription:" + source.id, name: source.name,
          members: Set(nodes.filter { !$0.hidden && $0.sourceID == source.id }.map(\.id)),
          kind: .subscription)
      }
    }
    var result: [Group] = []
    for node in nodes where !node.hidden && node.origin == "subscription" && !node.sourceID.isEmpty
    {
      let id = "subscription:" + node.sourceID
      if let index = result.firstIndex(where: { $0.id == id }) {
        result[index].members.insert(node.id)
      } else {
        result.append(
          Group(
            id: id,
            name: routes.map { $0.sourceName(node.sourceID) }.flatMap { $0.isEmpty ? nil : $0 }
              ?? (node.group.isEmpty ? "订阅" : node.group), members: [node.id],
            kind: .subscription))
      }
    }
    return result
  }
  var canDeleteSubscriptions: Bool {
    routes?.canWrite == true && routes?.capabilities.contains("subscriptions.delete") == true
      && connectionStore?.serviceUpdating == false
  }
  func deleteSubscriptionGroup(_ id: String, expectedRevision: UInt64) async throws -> UInt64 {
    guard let routes,
      let source = routes.subscriptions.first(where: { "subscription:" + $0.id == id })
    else { throw EditError("订阅分组已变化，请重新打开") }
    let committedRevision = try await routes.deleteSubscription(
      source, expectedCatalogRevision: expectedRevision)
    if selectedGroup == id { selectedGroup = "all" }
    return committedRevision
  }
  var defaultMembers: Set<String> {
    let assigned = Set(displayGroups.flatMap { $0.members })
    return Set(
      nodes.filter { !$0.hidden && $0.sourceID.isEmpty && !assigned.contains($0.id) }.map(\.id))
  }
  var displayGroups: [Group] { subscriptionGroups + groups }
  var visible: [NodeProfile] {
    guard session?.scenario != .empty else { return [] }
    let selectedMembers =
      selectedGroup == "all"
      ? nil
      : selectedGroup == "default"
        ? defaultMembers : displayGroups.first { $0.id == selectedGroup }?.members
    var result = nodes.filter {
      !$0.hidden && (selectedGroup == "all" || selectedMembers?.contains($0.id) == true)
        && (search.isEmpty
          || ($0.name + $0.server + $0.protocolName).localizedCaseInsensitiveContains(search))
    }
    if session?.scenario == .few { result = Array(result.prefix(1)) }
    switch sort {
    case .source: break
    case .name, .nameDown:
      result.sort {
        sort == .name
          ? $0.name.localizedStandardCompare($1.name) == .orderedAscending
          : $0.name.localizedStandardCompare($1.name) == .orderedDescending
      }
    case .featureUp, .featureDown, .addressUp, .addressDown:
      let address = sort == .addressUp || sort == .addressDown
      let ascending = sort == .featureUp || sort == .addressUp
      func key(_ node: NodeProfile) -> String? {
        let value = info(node.id)
        if address { return value.exitIP }
        let tags = [value.cfIP == nil ? "" : "CF", value.chain == nil ? "" : "链式"].filter {
          !$0.isEmpty
        }
        return tags.isEmpty ? nil : tags.joined(separator: " · ")
      }
      result.sort { a, b in
        let left = key(a)
        let right = key(b)
        if left == nil || right == nil { return left != nil && right == nil }
        guard let left, let right else { return false }
        let comparison = left.localizedStandardCompare(right)
        if comparison == .orderedSame { return a.id < b.id }
        return ascending ? comparison == .orderedAscending : comparison == .orderedDescending
      }
    default:
      let speed = sort == .speedUp || sort == .speedDown
      let ascending = sort == .latencyUp || sort == .speedUp
      result.sort { a, b in
        let aMetric = speed ? info(a.id).speed : info(a.id).latency
        let bMetric = speed ? info(b.id).speed : info(b.id).latency
        let av = aMetric.historical ? nil : aMetric.value
        let bv = bMetric.historical ? nil : bMetric.value
        guard let av else { return false }
        guard let bv else { return true }
        return av == bv ? a.id < b.id : ascending ? av < bv : av > bv
      }
    }
    return result
  }
  func node(_ id: String) -> NodeProfile? { nodes.first { $0.id == id } }
  func certificateWarning(_ id: String) -> String? {
    if let active = connectionStore?.activeTLS, active.routeID == id, active.skipped {
      return "未校验证书"
    }
    guard let node = node(id), node.security == "tls" else { return nil }
    return node.tlsPolicy?.mode == .skipVerify ? "未校验证书" : nil
  }
  func certificateExplanation(_ id: String) -> String {
    if let active = connectionStore?.activeTLS, active.routeID == id, active.skipped {
      return "当前连接未校验证书，连接仍加密，但服务器身份未验证。"
    }
    guard let node = node(id), node.security == "tls", let policy = node.tlsPolicy else {
      return ""
    }
    if isCurrent(id), let active = connectionStore?.activeTLS, active.routeID == id,
      !active.skipped, policy.mode == .skipVerify
    {
      return "当前连接仍按默认校验策略运行。已保存跳过证书校验设置，下次使用生效；届时连接仍加密，但服务器身份未验证。"
    }
    if policy.mode == .skipVerify {
      return (policy.userOverride ? "用户设置了" : "来源设置了") + "跳过证书校验。连接仍加密，但服务器身份未验证。"
    }
    return "默认校验证书；校验失败会显示原因，不会自动跳过。"
  }
  func info(_ id: String) -> Presentation {
    guard let routes else { return presentations[id] ?? Presentation() }
    guard let node = node(id) else { return Presentation() }
    var value = Presentation()
    if let chain = node.chain { value.chain = Chain(front: chain.frontID, final: chain.finalID) }
    if let history = routes.history[id] {
      value.latency.samples = history.filter {
        $0.metric == "latency" && $0.method == "https204-median-v3" && $0.success
          && $0.configRevision == node.configRevision
          && $0.network == physicalNetwork
      }.reversed().map(\.value)
      value.speed.samples = history.filter {
        $0.metric == "download" && $0.success && $0.configRevision == node.configRevision
          && $0.network == physicalNetwork
      }.reversed().map { $0.value / 1_000_000 }
    }
    if let usage = routes.usage[id], usage.error == nil {
      if usage.todayUpload > 0 || usage.todayDownload > 0
        || usage.items.contains(where: { Calendar.current.isDateInToday($0.at) })
      {
        value.uploadMB = Double(usage.from == nil ? usage.todayUpload : usage.upload) / 1_000_000
        value.downloadMB =
          Double(usage.from == nil ? usage.todayDownload : usage.download) / 1_000_000
      }
      if routes.connected, let at = usage.sampledAt, Date().timeIntervalSince(at) >= 0,
        Date().timeIntervalSince(at) <= 3
      {
        value.activeConnections = Int(usage.connections)
      }
      value.windowConnections = usage.windowConnections
      value.transferSamples = routeUsageSeries(usage)
    }
    if node.cfEntry { value.cfIP = node.server }
    return withCFEntry(Self.measurementPresentation(node, routes: routes, initial: value), id: id)
  }

  func canQueryExit(_ id: String) -> Bool {
    guard let routes else { return true }
    return routes.canWrite && routes.capabilities.contains("routes.measure.exit")
      && !isQueryingExit(id) && node(id)?.hidden == false
      && connectionStore?.serviceUpdating == false
  }
  func isQueryingExit(_ id: String) -> Bool { routes?.exitOperationIDs[id] != nil }
  var hasExitQueries: Bool { routes?.exitOperationIDs.isEmpty == false }
  func stopExitQueries(_ id: String? = nil) {
    guard let routes else { return }
    Task { await routes.cancelExitQueries(routeID: id) }
  }
  func isTestingLatency(_ id: String) -> Bool { routes?.isTesting(id) == true }
  func isMeasuring(_ id: String) -> Bool {
    routes?.isTesting(id) == true || routes?.downloadingRouteIDs.contains(id) == true
  }
  func measurementProgressCaption(_ id: String) -> String {
    routes?.routeMeasurementProgress[id]?.phase.caption ?? "正在测量延迟"
  }
  func exitRegion(_ id: String) -> String {
    guard let value = info(id).exitLocation else { return "" }
    return ExitLocationPresentation.region(value)
  }
  func exitDetails(_ id: String) -> String {
    let p = info(id)
    if let value = p.exitLocation {
      return value.ip + "\n" + ExitLocationPresentation.details(value)
    }
    if let old = p.lastExitLocation {
      return (p.exitMessage ?? "本次出口未获取") + "\n上次结果（历史）：" + old.ip + "\n"
        + ExitLocationPresentation.details(old)
    }
    return p.exitMessage ?? "尚未获取出口信息"
  }
  func isSelected(_ id: String) -> Bool {
    (routes?.selectedRouteID ?? session?.selectedRouteID) == id
  }
  func isCurrent(_ id: String) -> Bool { isRunning && currentRouteID == id }
  func perform(_ intent: Intent) {
    message = ""
    if routes != nil {
      switch intent {
      case .search(let value): search = value
      case .group(let id): selectedGroup = id
      case .sort(let value): sort = value
      case .use(let id):
        guard canSelectRoute, let route = node(id), !route.hidden else { return }
        Task { await connectionStore?.selectRoute(route.id) }
      case .test(let id, .speed):
        guard canDownload(id), let route = node(id) else { return }
        Task { await routes?.perform(.download(route)) }
      case .test(let id, .exit):
        guard canQueryExit(id), let route = node(id) else { return }
        Task { await routes?.perform(.queryExit(route)) }
      case .test(let id, .latency):
        guard canTest(id), let route = node(id) else { return }
        Task { await routes?.perform(.test(route)) }
      case .test(let id, .cf): testCFEntry(id)
      case .remove(let id):
        guard canEdit, let route = node(id), !isCurrent(id) else { return }
        Task { await routes?.perform(.delete(route)) }
      case .refresh: refreshSubscription(selectedGroup, viaRoute: false)
      default: message = "此功能尚未接入后台"
      }
      return
    }
    switch intent {
    case .search(let value): search = value
    case .group(let id): selectedGroup = id
    case .sort(let value): sort = value
    case .use(let id):
      guard node(id) != nil, info(id).latency.failure == nil, session?.canOperate ?? false else {
        message = "线路暂不可用"
        return
      }
      if let session, session.scenario == .off {
        Task { await session.connection.selectRoute(id) }
      } else {
        session?.perform(.selectRoute(id))
      }
      message = "已使用此线路"
    case .test(let id, let kind): beginTest(id, kind)
    case .refresh: refreshSubscriptions()
    case .remove(let id):
      guard !isCurrent(id),
        !presentations.values.contains(where: { $0.chain?.front == id || $0.chain?.final == id })
      else {
        message = "当前线路或被组合引用的线路不能删除"
        return
      }
      nodes.removeAll { $0.id == id }
      presentations[id] = nil
      configs[id] = nil
      for index in groups.indices { groups[index].members.remove(id) }
      if selectedGroup != "all" && selectedGroup != "default"
        && !displayGroups.contains(where: { $0.id == selectedGroup })
      {
        selectedGroup = "all"
      }
      syncSession()
      message = "已移除线路"
    }
  }
  func draft(_ id: String) -> Draft? {
    if isLive, let node = node(id), let chain = node.chain {
      return Draft(
        id: id, revision: node.revision, name: node.name, server: node.server,
        port: String(node.port), host: "", sni: node.serverName, json: "{}", frontID: chain.frontID,
        frozenFront: self.node(chain.frontID), frozenFinal: self.node(chain.finalID))
    }
    guard !isLive else { return nil }
    guard let node = node(id) else { return nil }
    let json = configs[id] ?? defaultJSON(node)
    let object = (try? JSONSerialization.jsonObject(with: Data(json.utf8))) as? [String: Any]
    let transport = object?["transport"] as? [String: Any]
    let headers = transport?["headers"] as? [String: String]
    return Draft(
      id: id, name: node.name, server: node.server, port: String(node.port),
      host: headers?["Host"] ?? transport?["host"] as? String ?? "", sni: node.serverName,
      cf: info(id).cfIP != nil,
      recordFragment: (object?["tls"] as? [String: Any])?["record_fragment"] as? Bool ?? false,
      preferredIP: info(id).cfPreferred ? node.server : nil, json: json,
      frontID: info(id).chain?.front)
  }
  func loadDraft(_ id: String) async throws -> Draft {
    guard let routes else {
      guard let draft = draft(id) else { throw EditError("线路不存在") }
      return draft
    }
    guard canEdit else { throw EditError("当前无法编辑线路") }
    if node(id)?.chain != nil, let value = draft(id) { return value }
    let edit = try await routes.loadEditor(id)
    guard routes.connected else { throw EditError("与后台连接已断开") }
    let formatted = try encode(parse(edit.documentJSON))
    return Draft(
      id: id, revision: edit.revision, name: edit.name, server: edit.server,
      port: String(edit.port), host: edit.wsHost, sni: edit.serverName,
      cf: node(id)?.cfEntry == true,
      recordFragment: (try parse(formatted)["tls"] as? [String: Any])?["record_fragment"] as? Bool
        ?? false, json: formatted)
  }
  func saveDocument(_ draft: Draft) async throws {
    guard let routes else {
      try save(draft)
      return
    }
    guard canEdit else { throw EditError("当前无法保存，草稿仍保留") }
    if let chain = node(draft.id)?.chain, let frontID = draft.frontID,
      let front = draft.frozenFront ?? node(frontID),
      let final = draft.frozenFinal ?? node(chain.finalID), let current = node(draft.id)
    {
      try await routes.saveChain(
        ChainEdit(
          id: current.id, name: draft.name, front: front, final: final, revision: draft.revision))
      return
    }
    var edit = RouteEdit()
    edit.routeID = draft.id
    edit.revision = draft.revision
    edit.name = draft.name
    edit.documentJSON = draft.json
    edit.requestID = UUID().uuidString
    try await routes.saveDocument(edit)
  }
  func synchronizeJSON(_ draft: inout Draft) throws {
    var object = try parse(draft.json)
    object["tag"] = draft.name
    object["server"] = draft.server
    object["server_port"] = UInt32(draft.port) ?? 0
    if var tls = object["tls"] as? [String: Any] {
      if (tls["server_name"] as? String ?? "") != draft.sni { tls["server_name"] = draft.sni }
      if draft.recordFragment || tls["record_fragment"] != nil {
        tls["record_fragment"] = draft.recordFragment
      }
      object["tls"] = tls
    }
    if var transport = object["transport"] as? [String: Any], transport["type"] as? String == "ws" {
      var headers = transport["headers"] as? [String: Any] ?? [:]
      if (headers["Host"] as? String ?? "") != draft.host {
        headers["Host"] = draft.host
        transport["headers"] = headers
      }
      object["transport"] = transport
    }
    if var transport = object["transport"] as? [String: Any],
      transport["type"] as? String == "httpupgrade"
    {
      if (transport["host"] as? String ?? "") != draft.host { transport["host"] = draft.host }
      object["transport"] = transport
    }
    draft.json = try encode(object)
  }
  func acceptJSON(_ draft: inout Draft) throws {
    let object = try validated(draft)
    if let name = object["tag"] as? String { draft.name = name }
    draft.server = object["server"] as? String ?? ""
    draft.port = String((object["server_port"] as? NSNumber)?.uint32Value ?? 0)
    draft.recordFragment = (object["tls"] as? [String: Any])?["record_fragment"] as? Bool ?? false
    draft.sni = (object["tls"] as? [String: Any])?["server_name"] as? String ?? ""
    let transport = object["transport"] as? [String: Any]
    draft.host =
      (transport?["headers"] as? [String: Any])?["Host"] as? String ?? transport?["host"] as? String
      ?? ""
    draft.json = try encode(object)
    if draft.preferredIP != draft.server { draft.preferredIP = nil }
  }
  func sameDraft(_ lhs: Draft?, _ rhs: Draft?) -> Bool {
    guard var lhs, var rhs else { return lhs == rhs }
    guard let left = try? encode(parse(lhs.json)), let right = try? encode(parse(rhs.json)) else {
      return lhs == rhs
    }
    lhs.json = left
    rhs.json = right
    return lhs == rhs
  }
  func save(_ draft: Draft) throws {
    guard !isLive else { throw EditError("此功能尚未接入后台") }
    guard let old = node(draft.id),
      !draft.name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    else { throw EditError("请填写线路名称") }
    if var chain = info(draft.id).chain {
      guard let front = draft.frontID else { throw EditError("请选择前置线路") }
      try validateChain(front: front, final: chain.final)
      chain.front = front
      var presentation = Presentation(chain: chain)
      presentation.override = true
      presentations[draft.id] = presentation
      replaceNode(old, name: draft.name, server: old.server, port: old.port, sni: old.serverName)
    } else {
      let object = try validated(draft)
      if draft.cf
        && (!["ws", "httpupgrade"].contains(
          (object["transport"] as? [String: Any])?["type"] as? String ?? "")
          || !Self.validIP(draft.server))
      {
        throw EditError("CF 入口需要有效 IP 和支持的传输方式")
      }
      if old.origin == "subscription", originals[old.id] == nil {
        originals[old.id] = old
        originalJSON[old.id] = configs[old.id] ?? defaultJSON(old)
      }
      configs[old.id] = try encode(object)
      let history = info(old.id)
      presentations[old.id] = Presentation(
        cfIP: draft.cf ? draft.server : nil,
        cfPreferred: draft.cf && draft.preferredIP == draft.server,
        uploadMB: history.uploadMB, downloadMB: history.downloadMB,
        transferSamples: history.transferSamples, override: old.origin == "subscription")
      replaceNode(
        old, name: draft.name, server: draft.server, port: UInt32(draft.port) ?? old.port,
        sni: draft.sni,
        transport: (object["transport"] as? [String: Any])?["type"] as? String
          ?? (old.protocolName == "hysteria2" ? "quic" : "tcp"),
        security: ((object["tls"] as? [String: Any])?["reality"] as? [String: Any])?["enabled"]
          as? Bool == true
          ? "reality"
          : (object["tls"] as? [String: Any])?["enabled"] as? Bool == true ? "tls" : "none")
    }
    cancelTests(draft.id)
    let dependents = nodes.filter {
      info($0.id).chain.map { $0.front == draft.id || $0.final == draft.id } == true
    }
    for dependent in dependents {
      cancelTests(dependent.id)
      let oldInfo = info(dependent.id)
      presentations[dependent.id] = Presentation(
        uploadMB: oldInfo.uploadMB, downloadMB: oldInfo.downloadMB,
        transferSamples: oldInfo.transferSamples, chain: oldInfo.chain, override: oldInfo.override)
      if let final = oldInfo.chain.flatMap({ node($0.final) }) {
        replaceNode(
          dependent, name: dependent.name, server: final.server, port: final.port,
          sni: final.serverName, transport: final.transport, security: final.security)
      }
    }
    syncSession()
    message = "已保存"
  }
  func connection(_ draft: Draft) -> (transport: String, security: String, supportsCF: Bool) {
    let object = try? parse(draft.json)
    let transport =
      (object?["transport"] as? [String: Any])?["type"] as? String
      ?? (node(draft.id)?.protocolName == "hysteria2" ? "quic" : "tcp")
    let tls = object?["tls"] as? [String: Any]
    let security =
      (tls?["reality"] as? [String: Any])?["enabled"] as? Bool == true
      ? "reality" : tls?["enabled"] as? Bool == true ? "tls" : "none"
    let supported =
      ["ws", "httpupgrade"].contains(transport)
      && ["vmess", "vless", "trojan"].contains(node(draft.id)?.protocolName ?? "")
      && info(draft.id).chain == nil
    return (transport, security, supported)
  }
  func supportsCF(_ id: String) -> Bool {
    guard let n = node(id) else { return false }
    return ["ws", "httpupgrade"].contains(n.transport)
      && ["vmess", "vless", "trojan"].contains(n.protocolName) && info(id).chain == nil
  }
  func candidates(for draft: Draft) -> [Candidate] {
    [
      draft.server, "172.64.79.122", "104.16.0.1", "104.17.0.1", "162.159.36.1",
      "2606:4700::6810:1",
    ].reduce(into: []) { rows, ip in
      if Self.validIP(ip), !rows.contains(where: { $0.ip == ip }) {
        rows.append(Candidate(ip: ip, measurement: Measurement()))
      }
    }
  }
  static func validatedGroupName(_ value: String, existing: Set<String>) throws -> String {
    let name = value.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !name.isEmpty, name.unicodeScalars.count <= 24, name != "全部", !existing.contains(name),
      name.rangeOfCharacter(from: .controlCharacters) == nil
    else {
      throw EditError("分组名称需为 1–24 个字，且不能重复")
    }
    return name
  }
  func group(_ id: String, name: String, members: Set<String>) throws {
    guard !isLive else { throw EditError("此功能尚未接入后台") }
    guard !id.hasPrefix("subscription:") else { throw EditError("订阅分组由来源自动维护") }
    let name = try Self.validatedGroupName(
      name, existing: Set(groups.filter { $0.id != id }.map(\.name)))
    let value = Group(id: id, name: name, members: members.intersection(Set(nodes.map(\.id))))
    if let index = groups.firstIndex(where: { $0.id == id }) {
      groups[index] = value
    } else {
      groups.append(value)
    }
  }
  func saveGroups(_ value: [Group], expectedRevision: UInt64) async throws {
    guard let routes else {
      try replaceGroups(value)
      return
    }
    guard canManageGroups else { throw EditError("当前无法保存分组，草稿仍保留") }
    guard value.allSatisfy({ $0.kind == .custom }) else { throw EditError("订阅分组由来源自动维护") }
    let edit = RouteGroupsEdit(
      groups: value.map {
        RouteGroup(id: $0.id, name: $0.name, memberIDs: $0.members.sorted())
      }, expectedRevision: expectedRevision)
    try await routes.saveGroups(edit)
    if selectedGroup != "all" && selectedGroup != "default"
      && !displayGroups.contains(where: { $0.id == selectedGroup })
    {
      selectedGroup = "all"
    }
  }
  func replaceGroups(_ value: [Group]) throws {
    guard !isLive else { throw EditError("此功能尚未接入后台") }
    guard value.allSatisfy({ $0.kind == .custom && !$0.id.hasPrefix("subscription:") }) else {
      throw EditError("订阅分组由来源自动维护")
    }
    var seen: Set<String> = []
    let names = try value.map { group in
      let name = try Self.validatedGroupName(group.name, existing: seen)
      seen.insert(name)
      return name
    }
    let ids = Set(nodes.map(\.id))
    groups = zip(value, names).map {
      Group(id: $0.0.id, name: $0.1, members: $0.0.members.intersection(ids))
    }
    if selectedGroup != "all" && selectedGroup != "default"
      && !displayGroups.contains(where: { $0.id == selectedGroup })
    {
      selectedGroup = "all"
    }
  }
  func removeGroup(_ id: String) {
    guard !isLive else { return }
    groups.removeAll { $0.id == id }
    if selectedGroup == id { selectedGroup = "all" }

  }
  func moveGroup(_ id: String, by offset: Int) {
    guard !isLive else { return }
    guard let index = groups.firstIndex(where: { $0.id == id }),
      groups.indices.contains(index + offset)
    else { return }
    groups.swapAt(index, index + offset)
  }
  func eligibleFronts(final: String) -> [NodeProfile] {
    nodes.filter { candidate in
      candidate.id != final && !candidate.hidden && !candidate.sourceRemoved
        && info(candidate.id).chain == nil
        && (node(final)?.transport != "quic"
          || ["shadowsocks", "hysteria2"].contains(candidate.protocolName))
    }
  }
  func createChainLive(
    front: String, final: String, name: String, frozenFront: NodeProfile? = nil,
    frozenFinal: NodeProfile? = nil
  ) async throws -> String {
    guard let routes, let a = node(front), let b = node(final) else {
      return try createChain(front: front, final: final, name: name)
    }
    try await routes.saveChain(
      ChainEdit(name: name, front: frozenFront ?? a, final: frozenFinal ?? b))
    return routes.routes.first {
      $0.chain?.frontID == front && $0.chain?.finalID == final && $0.name == name
    }?.id ?? ""
  }
  func setUsageWindow(_ id: String, from: Date?) async {
    await routes?.setUsageWindow(id, from: from)
  }
  struct HistoryRow: Equatable {
    let id: String
    let time: String
    let metric: String
    let result: String
    let status: String
    let failed: Bool
    let stale: Bool
  }
  func historyRows(_ id: String) -> [HistoryRow] {
    guard let node = node(id) else { return [] }
    return (routes?.history[id] ?? []).enumerated().map { offset, sample in
      let metric =
        sample.metric == "connect"
        ? "首次连接"
        : sample.metric == "latency"
          ? (sample.method == "https204-median-v3" ? "稳定延迟" : "历史延迟")
          : sample.metric == "download" ? "下载速度" : "出口 IP"
      let result: String
      if !sample.success {
        result = sample.error.map(ProductErrorPresentation.message) ?? "测试失败"
      } else if sample.metric == "exit" {
        result =
          sample.exitIP
          + (sample.exitLocation.map { " · " + ExitLocationPresentation.region($0) } ?? "")
      } else if sample.metric == "download" {
        result = MeasurementFormatting.speed(bytesPerSecond: sample.value)?.text ?? "—"
      } else {
        result = MeasurementFormatting.latency(sample.value)?.text ?? "—"
      }
      let configChanged = sample.configRevision != node.configRevision
      let networkUnknown = sample.network.isEmpty || physicalNetwork.isEmpty
      let networkChanged = !networkUnknown && sample.network != physicalNetwork
      let stale = configChanged || networkChanged || networkUnknown
      let status =
        !sample.success
        ? (configChanged || networkChanged ? "失败 · 已过期" : "失败")
        : configChanged ? "旧配置" : networkChanged ? "其它网络" : networkUnknown ? "历史" : "成功"
      return HistoryRow(
        id: "\(sample.metric)/\(sample.at.timeIntervalSince1970)/\(offset)",
        time: MeasurementTimestamp.text(sample.at), metric: metric, result: result, status: status,
        failed: !sample.success, stale: stale)
    }
  }
  func measurementHistoryError(_ id: String) -> String? { routes?.historyError(id) }
  func loadMeasurementHistory(_ id: String, more: Bool = false) async {
    await routes?.loadMeasurementHistory(id, more: more)
  }
  func measurementHistoryCaption(_ id: String) -> String {
    if let error = routes?.historyError(id) { return error }
    return "按测试时间记录，保留失败及旧配置结果"
  }
  func loadRouteData(_ id: String, more: Bool = false) async {
    await routes?.loadRouteData(id, more: more)
  }
  func routesUsage(_ id: String) -> RouteUsagePage? { routes?.usage[id] }
  func setUsagePeriod(_ id: String, period: String) async {
    await routes?.setUsagePeriod(id, period: period)
  }
  func refreshRouteUsage(_ id: String, more: Bool = false) async {
    await routes?.refreshRouteUsage(id, more: more)
  }
  func hasMoreHistory(_ id: String) -> Bool { !(routes?.historyCursors[id] ?? "").isEmpty }
  func hasMoreUsage(_ id: String) -> Bool { !(routes?.usageCursors[id] ?? "").isEmpty }
  func dataCaption(_ id: String) -> String {
    guard let routes, routes.capabilities.contains("routes.usage") else { return "当前后台未提供使用记录" }
    if let error = routes.usageError(id) { return error }
    guard let usage = routes.usage[id] else {
      return routes.connected ? "正在读取使用记录…" : "后台未连接，暂时无法读取使用记录"
    }
    if usage.todayUpload == 0 && usage.todayDownload == 0
      && !usage.items.contains(where: { Calendar.current.isDateInToday($0.at) })
    {
      if !usage.items.isEmpty { return "今日尚无用量记录，已有历史流量可按时间范围查看" }
      return "这条线路尚无使用记录；开启代理并产生流量后开始统计，测速流量不计入。"
    }
    if usage.basis == "legacy_selected_route" { return "包含升级前按所选线路记录的用量；旧出口和对象归属无法恢复" }
    return usage.partial ? "仅显示已记录用量，未采集的历史时段无法补算" : "今日累计 · 按实际采样时间"
  }
  func createChain(front: String, final: String, name: String) throws -> String {
    guard !isLive else { throw EditError("此功能尚未接入后台") }
    try validateChain(front: front, final: final)
    guard let n = node(final), !name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
      throw EditError("请填写组合线路名称")
    }
    let id = UUID().uuidString
    nodes.append(
      NodeProfile(
        id: id, name: name, protocolName: n.protocolName, server: n.server, port: n.port,
        group: "本地线路", serverName: n.serverName, revision: 1, transport: n.transport,
        security: n.security, origin: "local"))
    presentations[id] = Presentation(chain: Chain(front: front, final: final))
    syncSession()
    message = "已创建链式代理"
    return id
  }
  func path(_ id: String, frontID: String? = nil) -> [PaxConnectionPath.Node] {
    var chain = info(id).chain
    if let frontID { chain?.front = frontID }
    var value = connectionPath(
      ids: chain.map { [$0.front, $0.final] } ?? [id], chained: chain != nil,
      current: isCurrent(id) && isRunning)
    if chain != nil, let exit = info(id).exitIP {
      value.append(.init("出口 IP", address: exit + "\n" + exitRegion(id), symbol: "globe"))
    }
    return value
  }
  func path(frontID: String, finalID: String) -> [PaxConnectionPath.Node] {
    connectionPath(ids: [frontID, finalID], chained: true, current: false)
  }
  private func connectionPath(ids: [String], chained: Bool, current: Bool)
    -> [PaxConnectionPath.Node]
  {
    var result = [
      PaxConnectionPath.Node(
        "本机出口 IP",
        address: routes.map { store in
          guard store.connected else { return "后台断开" }
          guard let egress = store.physicalEgress else { return "未检测" }
          if egress.phase == "loading" { return "检测中…" }
          guard egress.phase == "ready" else { return "未获取" }
          return !egress.ipv4.isEmpty ? egress.ipv4 : !egress.ipv6.isEmpty ? egress.ipv6 : "未获取"
        } ?? (current ? "192.0.2.10" : "未检测"),
        symbol: "desktopcomputer")
    ]
    for (index, id) in ids.enumerated() {
      guard let n = node(id) else { continue }
      if let ip = info(id).cfIP {
        result.append(
          .init("CF", address: ip, symbol: "cloud", tone: .blue)
        )
      }
      result.append(
        .init(
          chained ? n.name : "出口 IP",
          address: chained
            ? (info(id).cfIP == nil && !n.server.isEmpty ? n.server : "未获取")
            : ((info(id).exitIP ?? "未获取") + (exitRegion(id).isEmpty ? "" : "\n" + exitRegion(id))),
          symbol: chained && index == 0 ? "network" : "server.rack",
          tone: chained && index == 0 ? .purple : .neutral))
    }
    return result
  }
  func importPreview(_ text: String) throws -> [NodeProfile] {
    guard !isLive else { throw EditError("此功能尚未接入后台") }
    let lines = text.split(whereSeparator: \.isNewline).map(String.init)
    guard !lines.isEmpty else { throw EditError("请粘贴分享链接或订阅地址") }
    if lines.count == 1, let url = URL(string: lines[0]),
      ["http", "https"].contains(url.scheme ?? ""), url.host != nil
    {
      let sourceID =
        "imported-"
        + SHA256.hash(data: Data(url.absoluteString.utf8)).map { String(format: "%02x", $0) }
        .joined()
      let sourceName = url.host ?? "新订阅"
      return (0..<3).map {
        NodeProfile(
          id: UUID().uuidString, name: "订阅线路 · \($0 + 1)", protocolName: "vless",
          server: "203.0.113.\(90 + $0)", port: 443, group: sourceName, transport: "ws",
          sourceID: sourceID, origin: "subscription")
      }
    }
    return try lines.map { line in
      if line.hasPrefix("vmess://") {
        var payload = String(line.dropFirst(8)).replacingOccurrences(of: "-", with: "+")
          .replacingOccurrences(of: "_", with: "/")
        payload += String(repeating: "=", count: (4 - payload.count % 4) % 4)
        guard let data = Data(base64Encoded: payload),
          let object = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any],
          let server = object["add"] as? String, !server.isEmpty
        else { throw EditError("VMess 分享链接格式无效") }
        let port =
          (object["port"] as? String).flatMap(UInt32.init) ?? (object["port"] as? NSNumber)?
          .uint32Value ?? 443
        guard (1...65535).contains(port) else { throw EditError("VMess 端口无效") }
        return NodeProfile(
          id: UUID().uuidString, name: object["ps"] as? String ?? "VMess 线路", protocolName: "vmess",
          server: server, port: port, group: "本地线路", serverName: object["sni"] as? String ?? "",
          revision: 1, transport: object["net"] as? String ?? "tcp", origin: "local")
      }

      guard let url = URLComponents(string: line), let scheme = url.scheme,
        ["vless", "trojan", "hysteria2", "hy2", "ss", "anytls"].contains(scheme),
        let host = url.host,
        !host.isEmpty, url.user?.isEmpty == false
      else { throw EditError("分享链接格式无效；支持 VLESS、VMess、Trojan、Hysteria2、SS 链接") }
      let proto = scheme == "hy2" ? "hysteria2" : scheme == "ss" ? "shadowsocks" : scheme
      let parameters = Dictionary(
        url.queryItems?.map { ($0.name, $0.value ?? "") } ?? [],
        uniquingKeysWith: { _, last in last })
      guard (1...65535).contains(url.port ?? 443) else { throw EditError("分享链接端口无效") }
      return NodeProfile(
        id: UUID().uuidString, name: url.fragment ?? "导入线路", protocolName: proto, server: host,
        port: UInt32(url.port ?? 443), group: "本地线路", serverName: parameters["sni"] ?? "",
        revision: 1, transport: parameters["type"] ?? (proto == "hysteria2" ? "quic" : "tcp"),
        origin: "local")
    }
  }
  func commitImport(_ routes: [NodeProfile]) {
    guard !isLive else { return }
    for n in routes
    where !nodes.contains(where: {
      $0.name == n.name && $0.server == n.server && $0.port == n.port
        && $0.sourceID == n.sourceID
    }
    ) {
      nodes.append(n)
      presentations[n.id] = Presentation()
    }
    selectedGroup = "all"
    syncSession()
    message = "导入完成"
  }
  private func validateChain(front: String, final: String) throws {
    guard front != final, let a = node(front), let b = node(final), !a.hidden, !b.hidden,
      info(front).chain == nil, info(final).chain == nil
    else { throw EditError("前置与最终线路必须是不同的有效普通线路") }
  }
  private func replaceNode(
    _ old: NodeProfile, name: String, server: String, port: UInt32, sni: String,
    transport: String? = nil, security: String? = nil
  ) {
    guard let index = nodes.firstIndex(where: { $0.id == old.id }) else { return }
    nodes[index] = NodeProfile(
      id: old.id, name: name, protocolName: old.protocolName, server: server, port: port,
      group: old.group, serverName: sni, revision: old.revision + 1,
      transport: transport ?? old.transport, security: security ?? old.security,
      sourceID: old.sourceID, origin: old.origin)
  }
  private func refreshSubscriptions() {
    guard !demoRefreshing else { return }
    demoRefreshing = true
    refreshTask = Task { [weak self] in
      do { try await Task.sleep(for: PaxTheme.Routes.testDuration) } catch { return }
      guard let self else { return }
      self.demoRefreshing = false
      self.message = "订阅已刷新，保留本地覆盖与分组"
      self.refreshTask = nil
    }
  }
  private func beginTest(_ id: String, _ kind: Test) {
    guard let node = node(id) else { return }
    let key = id + "-\(kind)"
    guard tasks[key] == nil else { return }
    let token = UUID()
    testTokens[key] = token
    var info = info(id)
    switch kind {
    case .exit: info.exitMessage = "正在查询…"
    case .latency: info.latency.running = true
    case .speed: info.speed.running = true
    case .cf:
      guard info.cfIP != nil else { return }
      info.cfLatency.running = true
    }
    presentations[id] = info
    tasks[key] = Task { [weak self] in
      do { try await Task.sleep(for: PaxTheme.Routes.testDuration) } catch { return }
      guard let self, self.testTokens[key] == token, self.node(id)?.revision == node.revision else {
        return
      }
      var p = self.info(id)
      let failure = id == "tls-error" ? "证书验证失败" : id == "failed" && kind == .speed ? "下载请求超时" : nil
      let value: Double = kind == .cf ? 38 : kind == .speed ? 3.8 : 86
      var m = kind == .latency ? p.latency : kind == .speed ? p.speed : p.cfLatency
      m.running = false
      m.value = failure == nil ? value : nil
      m.failure = failure
      m.time = "刚刚"
      if failure == nil { m.samples.append(value) }
      switch kind {
      case .exit:
        p.exitIP = "203.0.113.60"
        p.exitMessage = nil
      case .latency: p.latency = m
      case .speed: p.speed = m
      case .cf: p.cfLatency = m
      }
      self.presentations[id] = p
      self.syncSession()
      self.tasks[key] = nil
      self.testTokens[key] = nil
    }
  }
  private func cancelTests(_ id: String) {
    for key in Array(tasks.keys) where key.hasPrefix(id + "-") {
      tasks[key]?.cancel()
      tasks[key] = nil
      testTokens[key] = nil
    }
  }
  private func syncSession() {
    guard !isLive else { return }
    let histories = Dictionary(
      uniqueKeysWithValues: nodes.map { node in
        (
          node.id,
          info(node.id).latency.samples.enumerated().map {
            PaxRTTSample(time: "第 \($0.offset + 1) 次", outcome: .measured($0.element))
          }
        )
      })
    session?.replaceManagedRoutes(nodes, histories: histories)
  }
  private func defaultJSON(_ n: NodeProfile) -> String {
    var o: [String: Any] = [
      "type": n.protocolName, "tag": n.id, "server": n.server, "server_port": n.port,
    ]
    if ["vless", "vmess"].contains(n.protocolName) {
      o["uuid"] = "b5d54a15-20ef-4107-a54b-936b75b1beae"
    } else {
      o["password"] = "example-password"
    }
    if n.protocolName == "vmess" { o["security"] = "auto" }
    if n.protocolName == "shadowsocks" { o["method"] = "aes-128-gcm" }
    if n.security != "none" { o["tls"] = ["enabled": true, "server_name": n.serverName] }
    if n.transport == "ws" {
      o["transport"] = ["type": "ws", "path": "/ws", "headers": ["Host": "edge.example.net"]]
    }
    return (try? encode(o)) ?? "{}"
  }
  private func parse(_ json: String) throws -> [String: Any] {
    do {
      var syntax = RouteDocumentSyntax(json)
      try syntax.validate()
      guard let result = try JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: Any]
      else { throw EditError("请输入一个节点配置对象") }
      return result
    } catch let error as EditError { throw error } catch { throw EditError("JSON 格式无效，请检查括号与逗号") }
  }
  private func encode(_ object: [String: Any]) throws -> String {
    String(
      decoding: try JSONSerialization.data(
        withJSONObject: object, options: [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]),
      as: UTF8.self)
  }
  private func validated(_ draft: Draft) throws -> [String: Any] {
    let o = try parse(draft.json)
    guard let n = node(draft.id), o["type"] as? String == n.protocolName else {
      throw EditError("请保留当前节点的协议类型")
    }
    guard let server = o["server"] as? String, !server.trimmingCharacters(in: .whitespaces).isEmpty,
      let port = o["server_port"] as? NSNumber, CFGetTypeID(port) != CFBooleanGetTypeID(),
      port.doubleValue.rounded() == port.doubleValue, (1...65535).contains(port.intValue)
    else { throw EditError("地址不能为空；端口须为 1–65535 的整数") }
    guard !["outbounds", "inbounds", "dns", "route", "detour"].contains(where: { o[$0] != nil })
    else { throw EditError("这里只接受单节点配置，链式代理在组合设置中编辑") }
    if ["socks", "http"].contains(n.protocolName) {
      let proxy = try ProxyRouteDraft(document: draft.json, name: draft.name)
      _ = try proxy.document()
      return o
    }
    let credential = ["vless", "vmess"].contains(n.protocolName) ? "uuid" : "password"
    guard (o[credential] as? String)?.isEmpty == false else {
      throw EditError("\(credential) 不能为空")
    }
    if n.protocolName == "hysteria2" {
      guard (o["tls"] as? [String: Any])?["enabled"] as? Bool == true, o["transport"] == nil else {
        throw EditError("Hysteria2 必须启用 TLS，不使用 WS/gRPC")
      }
    }
    if n.protocolName == "shadowsocks", (o["method"] as? String)?.isEmpty != false {
      throw EditError("请填写 Shadowsocks 加密方法")
    }
    return o
  }
  static func validIP(_ text: String) -> Bool {
    var v4 = in_addr()
    var v6 = in6_addr()
    return text.withCString {
      inet_pton(AF_INET, $0, &v4) == 1 || inet_pton(AF_INET6, $0, &v6) == 1
    }
  }
  struct EditError: LocalizedError {
    let message: String
    init(_ message: String) { self.message = message }
    var errorDescription: String? { message }
  }
}
