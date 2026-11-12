import Darwin
import Foundation
import Observation
import PaxCore
import PaxUI

/// 隔离的分流页面会话；连接事实与当前配置独立，不执行网络命令。
@MainActor @Observable
final class RoutingWorkbenchStore {
  enum Tab: Int { case traffic, rules }
  enum Scene: String, CaseIterable {
    case normal = "正常"
    case incomplete = "记录不完整"
    case unavailableRoute = "指定线路不可用"
  }
  struct Application: Equatable, Identifiable {
    let id: String
    let name: String
    let helpers: Set<String>
  }
  struct Hit: Equatable {
    let sourceID: String?
    let ruleID: String?
    let title: String
    let match: String
    var signature: String? = nil
    var basis: String {
      guard sourceID != nil else { return "无" }
      let parts = signature?.split(separator: "|", maxSplits: 1).map(String.init) ?? []
      guard parts.count == 2, let method = Method(rawValue: parts[0]) else {
        return sourceID == "geo" ? "Geo：" + match : match
      }
      switch method {
      case .application:
        return "应用：" + (match.hasPrefix("应用 ") ? String(match.dropFirst(3)) : parts[1])
      case .domain: return "域名：" + parts[1]
      case .domainSuffix: return "域名及子域：" + parts[1]
      case .ip: return "IP：" + parts[1]
      case .cidr: return "IP 网段：" + parts[1]
      case .builtin, .source: return match
      }
    }
    var category: String {
      guard sourceID != nil else { return "未命中规则" }
      if sourceID == "geo" { return "Geo 规则" }
      let method = signature?.split(separator: "|", maxSplits: 1).first.map(String.init)
      switch method.flatMap(Method.init(rawValue:)) {
      case .application: return "应用规则"
      case .domain, .domainSuffix: return "域名规则"
      case .ip, .cidr: return "IP 规则"
      case .builtin, .source: return title
      case nil: return title
      }
    }
    var basisSource: String {
      guard let sourceID else { return "" }
      if sourceID == "geo" { return title + " · " + match }
      return sourceID == "priority" ? title : "规则包：" + title
    }
  }
  enum Intent {
    case tab(Tab)
    case dimension(Dimension)
    case period(Period)
    case direction(Direction)
    case search(String)
    case selectPackage(String)
    case movePackage(String, Int)
    case enablePackage(String, Bool)
    case packageDestination(String, Destination)
    case removeRule(String, String)
    case removePackage(String)
    case locate(Hit)
    case enableRule(String, String, Bool)
    case rollback(String)
    case automatic(String, Bool)
    case scene(Scene)
  }
  enum EditIntent {
    case package(PackageDraft)
    case rule(RuleDraft)
  }
  enum UpdateIntent { case update(String, Bool) }
  func perform(_ intent: UpdateIntent) async {
    if telemetry != nil {
      switch intent {
      case .update(let id, _):
        var q = RoutingMutation()
        q.action = package(id)?.kind == .geo ? "update_geo" : "update"
        q.packageID = id
        try? await commitLibrary(q)
      }
      return
    }
    switch intent {
    case .update(let id, let succeeds): await updatePackage(id, succeeds: succeeds)
    }
  }
  @discardableResult func perform(_ intent: EditIntent) throws -> String {
    guard telemetry == nil else { throw EditError("真实规则需要等待后台确认保存。") }
    switch intent {
    case .package(let draft): return try savePackage(draft)
    case .rule(let draft): return try saveRule(draft)
    }
  }
  var telemetry: TelemetryStore?
  var libraryRevision: UInt64 = 0
  var libraryBusy = false
  var libraryTask: Task<Void, Never>?
  var libraryRoutes: [NodeProfile] = []
  var routeSelectionStore: RouteWorkbenchStore?
  var libraryDomains: [String: [String]] = [:]
  @ObservationIgnored var liveQueryTask: Task<Void, Never>?
  @ObservationIgnored var liveRefreshPending = false
  private var savedLivePage = TrafficPage()
  @ObservationIgnored private var liveRowsByID: [String: TrafficRow] = [:]
  @ObservationIgnored private var liveFactsByID: [String: PaxCore.TrafficRow] = [:]
  private var trafficGeneration: UInt64 = 0
  private var trafficDataGeneration: UInt64 = 0
  private var trafficSession: UInt64 = 0
  var livePaginated = false
  private var savedPendingLivePage: TrafficPage?
  private var pendingLiveIdentity = ""
  private var liveIdentity: String {
    guard let telemetry else { return "" }
    return
      "\(telemetry.sessionGeneration):\(telemetry.trafficInvalidationGeneration):\(Calendar.current.startOfDay(for: Date()).timeIntervalSince1970)"
  }
  var pendingLivePage: TrafficPage? {
    get { pendingLiveIdentity == liveIdentity ? savedPendingLivePage : nil }
    set {
      savedPendingLivePage = newValue
      pendingLiveIdentity = liveIdentity
    }
  }
  private var liveDayChanged: Bool {
    savedLivePage.window == "today" && !Calendar.current.isDateInToday(savedLivePage.asOf)
  }
  var liveNeedsRead: Bool {
    if liveDayChanged { return true }
    return telemetry.map {
      $0.trafficInvalidationGeneration != trafficGeneration
        || ($0.connected && $0.sessionGeneration != trafficSession)
    } ?? false
  }
  var liveDataInvalid: Bool {
    if liveDayChanged { return true }
    return telemetry.map {
      $0.trafficDataInvalidationGeneration != trafficDataGeneration
        || ($0.connected && $0.sessionGeneration != trafficSession)
    } ?? false
  }
  var livePage: TrafficPage {
    get {
      if liveDataInvalid { return TrafficPage() }
      return liveNeedsRead ? savedLivePage.withoutLogEvidence : savedLivePage
    }
    set {
      savedLivePage = newValue
      liveRowsByID.removeAll(keepingCapacity: true)
      liveFactsByID = Dictionary(
        uniqueKeysWithValues: (newValue.items
          + [newValue.unknownSource, newValue.dnsSummary].compactMap { $0 }).map { ($0.id, $0) })
      trafficGeneration = telemetry?.trafficInvalidationGeneration ?? 0
      trafficDataGeneration = telemetry?.trafficDataInvalidationGeneration ?? 0
      trafficSession = telemetry?.sessionGeneration ?? 0
    }
  }
  func liveRow(id: String) -> TrafficRow? {
    guard !liveDataInvalid else { return nil }
    if liveNeedsRead { return livePage.items.first { $0.id == id }.map(Self.liveRow) }
    if let row = liveRowsByID[id] { return row }
    guard let fact = liveFactsByID[id] else { return nil }
    let row = Self.liveRow(fact)
    liveRowsByID[id] = row
    return row
  }
  func liveFact(id: String) -> PaxCore.TrafficRow? {
    guard !liveDataInvalid else { return nil }
    if liveNeedsRead {
      let page = livePage
      return (page.items + [page.unknownSource, page.dnsSummary].compactMap { $0 }).first {
        $0.id == id
      }
    }
    return liveFactsByID[id]
  }
  var liveLoading = false
  var liveFrozen = false
  var liveError = ""
  var liveGeneration: UInt64 = 0
  let backendAvailable: Bool
  let session: WorkbenchDemoStore
  var applications: [Application]
  private(set) var tab: Tab = .traffic
  private(set) var dimension: Dimension = .application
  private(set) var period: Period = .minute
  private(set) var direction: Direction = .all
  private(set) var search = ""
  private(set) var rulesSearch = ""
  private(set) var scene: Scene = .normal
  private(set) var connections: [Connection]
  var now: Date
  var packages: [RulePackage] = []
  var selectedPackageID = "priority"
  var selectedRuleID: String?
  var packageMessage = ""

  init(
    session: WorkbenchDemoStore, now: Date = Date(), connections: [Connection]? = nil,
    applications: [Application]? = nil, backendAvailable: Bool = true,
    initialPeriod: Period = .minute
  ) {
    self.session = session
    self.backendAvailable = backendAvailable
    self.period = initialPeriod
    self.now = now
    self.applications = backendAvailable ? (applications ?? Self.demoApplications) : []
    self.connections =
      backendAvailable ? (connections ?? Self.demoConnections(session: session, now: now)) : []
    packages = backendAvailable ? Self.demoPackages(session: session) : []
  }
  func perform(_ intent: Intent) {
    if telemetry != nil, performLibraryIntent(intent) { return }
    defer {
      if let telemetry {
        switch intent {
        case .tab:
          telemetry.setTrafficTabActive(tab == .traffic)
        case .dimension, .direction, .period:
          liveFrozen = false
          livePaginated = false
          pendingLivePage = nil
          scheduleLive(force: true)
        case .search: scheduleLive(delay: true, force: true)
        default: break
        }
      }
    }
    switch intent {
    case .tab(let value): tab = value
    case .dimension(let value):
      if telemetry != nil && dimension != value {
        liveLoading = true
        livePage = TrafficPage()
      }
      dimension = value
    case .period(let value):
      if telemetry != nil && period != value {
        liveLoading = true
        livePage = TrafficPage()
      }
      period = value
    case .direction(let value):
      if telemetry != nil && direction != value {
        liveLoading = true
        livePage = TrafficPage()
      }
      direction = value
    case .search(let value):
      if tab == .traffic {
        search = value
        if telemetry != nil {
          livePage = TrafficPage()
          liveLoading = true
          liveFrozen = false
          livePaginated = false
          pendingLivePage = nil
        }
      } else {
        rulesSearch = value
      }
    case .selectPackage(let id):
      if package(id) != nil {
        selectedPackageID = id
        selectedRuleID = nil
      }
    case .movePackage(let id, let delta): movePackage(id, delta: delta)
    case .enablePackage(let id, let enabled):
      guard canEdit, let index = packages.firstIndex(where: { $0.id == id }),
        packages[index].kind != .priority
      else { return }
      packages[index].enabled = enabled
    case .packageDestination(let id, let value):
      guard canEdit, let index = packages.firstIndex(where: { $0.id == id }),
        [.local, .subscription].contains(packages[index].kind)
      else { return }
      packages[index].destination = value
    case .removeRule(let packageID, let ruleID): removeRule(packageID: packageID, ruleID: ruleID)
    case .removePackage(let id): removePackage(id)
    case .locate(let hit): locate(hit)
    case .enableRule(let packageID, let ruleID, let enabled):
      guard canEdit, let index = packages.firstIndex(where: { $0.id == packageID }),
        [.priority, .local].contains(packages[index].kind),
        let rule = packages[index].rules.firstIndex(where: { $0.id == ruleID })
      else { return }
      packages[index].rules[rule].enabled = enabled
    case .automatic(let id, let value):
      guard canEdit, let index = packages.firstIndex(where: { $0.id == id }),
        [.subscription, .geo, .adblock].contains(packages[index].kind), !packages[index].updating
      else { return }
      packages[index].automatic = value
    case .scene(let value): scene = value
    case .rollback(let id):
      guard canEdit, let index = packages.firstIndex(where: { $0.id == id }),
        let previous = packages[index].previousVersion, !packages[index].updating
      else { return }
      packages[index].version = previous
      packages[index].rules = packages[index].previousRules ?? packages[index].rules
      packages[index].previousVersion = nil
      packages[index].previousRules = nil
      packages[index].automatic = false
      packages[index].updateMessage = "已回退，自动更新暂停；可手动恢复。"
    }
  }
  var canEdit: Bool {
    if let telemetry {
      return telemetry.routingLibraryAvailable && libraryRevision > 0 && !libraryBusy
    }
    return backendAvailable && session.scenario != .disconnected
  }
  var visibleRows: [TrafficRow] {
    if telemetry != nil { return livePage.items.compactMap { liveRow(id: $0.id) } }
    guard dataMessage == nil, session.scenario != .empty else { return [] }
    let lower = now.addingTimeInterval(-period.seconds)
    var current = connections.filter { $0.started <= now && ($0.ended ?? now) >= lower }
    if session.scenario == .few { current = Array(current.prefix(2)) }
    if session.scenario == .dense {
      current += current.prefix(20).enumerated().map { index, original in
        Connection(
          id: "dense-\(index)", applicationID: nil, process: "process-\(index)",
          domain: "host-\(index).example.com", ip: "203.0.113.\(index + 1)",
          routeID: original.routeID, routeName: original.routeName, direction: original.direction,
          hit: original.hit, started: original.started, ended: original.ended,
          establishmentMS: original.establishmentMS, transfers: original.transfers)
      }
    }
    if dimension == .system {
      current = current.filter {
        $0.process.hasPrefix("/usr/libexec/") || $0.process.hasPrefix("/usr/sbin/")
      }
    }
    let groups = Dictionary(grouping: current) { connection in
      let identity: String
      switch dimension {
      case .application: identity = connection.applicationID ?? "unknown:" + connection.process
      case .domain: identity = connection.domain ?? "unknown:" + connection.ip
      case .system: identity = connection.process
      }
      return "\(dimension.rawValue)|\(identity)|\(connection.direction.rawValue)|"
        + (connection.routeID ?? "direct")
    }
    let query = search.trimmingCharacters(in: .whitespacesAndNewlines)
    return groups.compactMap { id, values -> TrafficRow? in
      let ordered = values.sorted { $0.id < $1.id }
      guard let first = ordered.first else { return nil }
      let samples = ordered.flatMap(\.transfers).filter { $0.at > lower && $0.at <= now }
      let upload = samples.reduce(UInt64(0)) { $0 + $1.upload }
      let download = samples.reduce(UInt64(0)) { $0 + $1.download }
      guard upload + download > 0 || !query.isEmpty else { return nil }
      let application = applications.first { $0.id == first.applicationID }
      let name: String
      let symbol: String
      switch dimension {
      case .application:
        name = application?.name ?? "未识别进程"
        symbol =
          ["safari": "safari", "telegram": "paperplane", "store": "bag"][application?.id ?? ""]
          ?? "app"
      case .domain:
        name = first.domain ?? "未知域名"
        symbol = "globe"
      case .system:
        name = first.process
        symbol = "gearshape"
      }
      let subtitle = dimension == .application ? first.process : application?.name ?? "应用归属未知"
      let establishment = ordered.filter {
        $0.started > lower && $0.started <= now
      }.sorted { $0.started < $1.started }.map {
        PaxTrendSample(
          value: $0.establishmentMS,
          label: TrafficFormatting.time($0.started) + " · "
            + TrafficFormatting.milliseconds($0.establishmentMS))
      }
      let row = TrafficRow(
        id: id, name: session.scenario == .longText ? name + " · 长名称与身份展示检查" : name,
        subtitle: subtitle, symbol: symbol, route: first.routeName,
        direction: first.direction, connections: ordered, upload: upload, download: download,
        establishment: establishment)
      let searchable =
        [
          name, subtitle, row.route, row.basisTitle,
          row.basisSubtitle,
        ]
        + ordered.flatMap { [$0.domain ?? "", $0.ip, $0.process] }
        + ordered.map { connection in
          applications.first { $0.id == connection.applicationID }?.name ?? ""
        }
        + row.hits.map(\.match)
        + row.hits.map(\.basis)
        + row.hits.map(\.title)
      guard direction == .all || first.direction == direction else { return nil }
      guard
        query.isEmpty || searchable.joined(separator: " ").localizedCaseInsensitiveContains(query)
      else { return nil }
      return row
    }.sorted(by: Self.newestFirst(lower: lower, now: now))
  }
}

extension RoutingWorkbenchStore {
  static func demoPackages(session: WorkbenchDemoStore) -> [RulePackage] {
    let first = session.routes.first?.id ?? "vless-fast"
    let second = session.routes.dropFirst().first?.id ?? first
    return [
      .init(
        id: "priority", name: "优先规则", kind: .priority, destination: .global,
        rules: [
          .init(
            id: "telegram", method: .application, target: "telegram", destination: .route(second)),
          .init(id: "github", method: .domain, target: "github.com", destination: .route(first)),
        ]),
      .init(
        id: "domestic", name: "国内服务直连", kind: .local, destination: .direct,
        rules: [
          .init(id: "bing", method: .domain, target: "cn.bing.com", destination: .direct),
          .init(id: "bili", method: .domainSuffix, target: "bilibili.com", destination: .direct),
        ]),
      .init(
        id: "ai", name: "AI 服务", kind: .subscription, destination: .route(second),
        rules: [
          .init(
            id: "openai", method: .domainSuffix, target: "openai.com", destination: .route(second)),
          .init(
            id: "anthropic", method: .domainSuffix, target: "anthropic.com",
            destination: .route(second)),
        ], source: "https://example.com/rules/ai.srs"),
      .init(
        id: "geo", name: "Geo 基础包", kind: .geo, destination: .direct,
        rules: [
          .init(id: "lan", method: .cidr, target: "192.168.0.0/16", destination: .direct),
          .init(id: "cn-domain", method: .domainSuffix, target: "apple.com", destination: .direct),
        ]),
    ]
  }
  var selectedPackage: RulePackage? { package(selectedPackageID) }
  func package(_ id: String) -> RulePackage? { packages.first { $0.id == id } }
  var visibleRules: [Rule] {
    (selectedPackage?.rules ?? []).filter {
      rulesSearch.isEmpty
        || (ruleName($0) + " " + $0.method.rawValue).localizedCaseInsensitiveContains(rulesSearch)
    }
  }
  func ruleName(_ rule: Rule) -> String {
    rule.method == .application
      ? applications.first { $0.id == rule.target }?.name ?? "应用已不可识别" : rule.target
  }
  func newPackageDraft() -> PackageDraft { .init() }
  func packageDraft(_ id: String) -> PackageDraft? {
    package(id).map {
      .init(id: $0.id, name: $0.name, kind: $0.kind, destination: $0.destination, source: $0.source)
    }
  }
  @discardableResult private func savePackage(_ draft: PackageDraft) throws -> String {
    guard canEdit else { throw EditError("后台已断开，暂时不能修改。") }
    let name = draft.name.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !name.isEmpty, name.count <= 48 else { throw EditError("名称需为 1–48 个字。") }
    guard !packages.contains(where: { $0.id != draft.id && $0.name == name }) else {
      throw EditError("已有同名规则包。")
    }
    guard [.local, .subscription].contains(draft.kind) else { throw EditError("固定包不可通过此入口创建。") }
    if draft.kind == .subscription {
      guard let url = URL(string: draft.source), url.scheme?.lowercased() == "https",
        url.host != nil,
        url.user == nil, url.password == nil, !draft.source.contains(where: \.isWhitespace)
      else { throw EditError("请输入不含凭据的 HTTPS 规则集地址。") }
      if session.scenario == .failed { throw EditError("首次下载或校验失败，包未启用；现有规则保留。") }
    }
    let id = draft.id ?? UUID().uuidString
    if let index = packages.firstIndex(where: { $0.id == id }) {
      guard !packages[index].updating else { throw EditError("正在更新，请完成后再修改来源。") }
      guard packages[index].kind == draft.kind else { throw EditError("不能改变已有包的维护来源类型。") }
      packages[index].name = name
      packages[index].destination = draft.destination
      packages[index].source = draft.source
    } else {
      let value = RulePackage(
        id: id, name: name, kind: .local, destination: draft.destination, rules: [])
      packages.insert(
        value,
        at: packages.firstIndex(where: { [.adblock, .geo].contains($0.kind) }) ?? packages.endIndex)
      if let index = packages.firstIndex(where: { $0.id == id }) {
        packages[index] = RulePackage(
          id: id, name: name, kind: draft.kind, destination: draft.destination,
          rules: draft.kind == .subscription
            ? [
              .init(
                id: UUID().uuidString, method: .domainSuffix,
                target: "example.com", destination: draft.destination)
            ] : [], source: draft.source)
      }
    }
    selectedPackageID = id
    packageMessage = "已保存；仅影响新连接。"
    return id
  }
  func newRuleDraft(packageID: String) -> RuleDraft { .init(packageID: packageID) }
  func ruleDraft(packageID: String, ruleID: String) -> RuleDraft? {
    package(packageID)?.rules.first { $0.id == ruleID }.map {
      .init(
        id: $0.id, packageID: packageID, method: $0.method, target: $0.target,
        destination: $0.destination)
    }
  }
  @discardableResult private func saveRule(_ draft: RuleDraft) throws -> String {
    guard canEdit, let index = packages.firstIndex(where: { $0.id == draft.packageID }) else {
      throw EditError("规则包不可编辑。")
    }
    let (candidate, id, message) = try ruleProjection(draft, in: packages[index])
    packages[index] = candidate
    selectedRuleID = id
    packageMessage = message + "；仅影响新连接。"
    return id
  }
  func ruleProjection(_ draft: RuleDraft, in package: RulePackage) throws -> (
    RulePackage, String, String
  ) {
    var result = package
    guard [.priority, .local].contains(result.kind) else {
      throw EditError("来源内容只读，请在本地包维护覆盖。")
    }
    if result.kind == .priority && draft.destination == .global {
      throw EditError("优先规则请选择跟随默认、直连、指定线路或阻断。")
    }
    let imported = draft.method == .application && draft.importDomains
    let targets: [String]
    let method: Method
    if imported {
      let observed = Set(observedDomains(draft.target))
      targets = Array(Set(draft.selectedDomains)).sorted()
      guard !targets.isEmpty, targets.allSatisfy(observed.contains) else {
        throw EditError("请选择已观测到的域名。")
      }
      method = draft.domainMethod == .domainSuffix ? .domainSuffix : .domain
    } else {
      targets = [draft.target]
      method = draft.method
    }
    let normalized = try targets.map { try validate(method: method, target: $0) }
    let duplicate = result.rules.first {
      $0.id != draft.id && $0.method == method && normalized.contains($0.target)
    }
    if let duplicate, !imported {
      if result.kind == .priority {
        guard let existing = result.rules.firstIndex(where: { $0.id == duplicate.id })
        else { throw EditError("规则已失效。") }
        result.rules[existing].destination = draft.destination
        return (result, duplicate.id, "相同条件已更新原规则，没有重复新增。")
      }
      throw EditError("同包已有相同条目，请编辑原规则。")
    }
    if let id = draft.id, !result.rules.contains(where: { $0.id == id }) {
      throw EditError("原规则已不存在。")
    }
    let additions = normalized.filter { target in
      !imported
        || !result.rules.contains {
          $0.id != draft.id && $0.method == method && $0.target == target
        }
    }
    let originalIndex = result.rules.firstIndex { $0.id == draft.id }
    let wasEnabled = originalIndex.map { result.rules[$0].enabled } ?? true
    if let id = draft.id { result.rules.removeAll { $0.id == id } }
    let rows = additions.enumerated().map { offset, target in
      Rule(
        id: offset == 0 ? draft.id ?? UUID().uuidString : UUID().uuidString,
        method: method, target: target, destination: draft.destination, enabled: wasEnabled)
    }
    if result.kind == .priority {
      result.rules.insert(
        contentsOf: rows, at: min(originalIndex ?? 0, result.rules.count))
    } else {
      result.rules.append(contentsOf: rows)
    }
    return (
      result, rows.first?.id ?? draft.id ?? "",
      "保存 \(rows.count) 条，跳过重复 \(normalized.count - rows.count) 条"
    )
  }
  func validate(method: Method, target: String) throws -> String {
    let text = target.trimmingCharacters(in: .whitespacesAndNewlines)
    switch method {
    case .application:
      guard applications.contains(where: { $0.id == text }) else { throw EditError("请选择可确认身份的应用。") }
      return text
    case .domain, .domainSuffix:
      let value =
        text.lowercased().hasSuffix(".") ? String(text.lowercased().dropLast()) : text.lowercased()
      guard value.count <= 253, value.contains("."),
        value.split(separator: ".", omittingEmptySubsequences: false).allSatisfy({ label in
          !label.isEmpty && label.count <= 63 && label.first != "-" && label.last != "-"
            && label.utf8.allSatisfy {
              (97...122).contains($0) || (48...57).contains($0) || $0 == 45
            }
        })
      else { throw EditError("请输入有效域名，不含协议或路径。") }
      return value
    case .ip:
      guard let bytes = Self.address(text) else { throw EditError("请输入有效 IPv4 或 IPv6。") }
      return Self.addressText(bytes)
    case .builtin, .source: throw EditError("来源内容只读。")
    case .cidr:
      let parts = text.split(separator: "/", omittingEmptySubsequences: false)
      guard parts.count == 2, let bytes = Self.address(String(parts[0])), let bits = Int(parts[1]),
        bits >= 0, bits <= bytes.count * 8
      else { throw EditError("请输入有效 IP 网段和前缀。") }
      let normalized = bytes.enumerated().map { index, value -> UInt8 in
        let remaining = min(8, max(0, bits - index * 8))
        let mask: UInt8 = remaining == 0 ? 0 : UInt8(0xff << (8 - remaining) & 0xff)
        return value & mask
      }
      return Self.addressText(normalized) + "/" + String(bits)
    }
  }
  static func address(_ text: String) -> [UInt8]? {
    var v4 = in_addr()
    var v6 = in6_addr()
    if inet_pton(AF_INET, text, &v4) == 1 { return withUnsafeBytes(of: &v4) { Array($0) } }
    if inet_pton(AF_INET6, text, &v6) == 1 { return withUnsafeBytes(of: &v6) { Array($0) } }
    return nil
  }
  static func addressText(_ bytes: [UInt8]) -> String {
    var output = [CChar](repeating: 0, count: Int(INET6_ADDRSTRLEN))
    let family = bytes.count == 4 ? AF_INET : AF_INET6
    return bytes.withUnsafeBytes { input in
      guard inet_ntop(family, input.baseAddress, &output, socklen_t(output.count)) != nil else {
        return ""
      }
      return String(
        decoding: output.prefix { $0 != 0 }.map { UInt8(bitPattern: $0) }, as: UTF8.self)
    }
  }
  func observedDomains(_ applicationID: String) -> [String] {
    if telemetry != nil { return libraryDomains[applicationID] ?? [] }
    return Array(Set(connections.filter { $0.applicationID == applicationID }.compactMap(\.domain)))
      .sorted()
  }
  func matches(_ rule: Rule, applicationID: String?, domain: String?, ip: String) -> Bool {
    switch rule.method {
    case .application: return applicationID == rule.target
    case .domain: return domain?.lowercased() == rule.target
    case .domainSuffix:
      return domain?.lowercased() == rule.target
        || domain?.lowercased().hasSuffix("." + rule.target) == true
    case .ip: return Self.address(ip) == Self.address(rule.target) && Self.address(ip) != nil
    case .builtin, .source: return false
    case .cidr:
      let parts = rule.target.split(separator: "/")
      guard parts.count == 2, let prefix = Int(parts[1]),
        let network = Self.address(String(parts[0])),
        let address = Self.address(ip), network.count == address.count
      else { return false }
      return zip(network, address).enumerated().allSatisfy { index, pair in
        let bits = min(8, max(0, prefix - index * 8))
        let mask: UInt8 = bits == 0 ? 0 : UInt8(0xff << (8 - bits) & 0xff)
        return pair.0 & mask == pair.1 & mask
      }
    }
  }
  func preview(applicationID: String?, domain: String?, ip: String) -> Preview {
    decision(in: packages, applicationID: applicationID, domain: domain, ip: ip)
  }
  private func decision(
    in packages: [RulePackage], applicationID: String?, domain: String?, ip: String
  ) -> Preview {
    if let pack = packages.first, pack.kind == .priority,
      let rule = pack.rules.first(where: {
        $0.enabled && matches($0, applicationID: applicationID, domain: domain, ip: ip)
      }), rule.destination != .followDefault
    {
      return .init(
        destination: rule.destination, sourceID: pack.id, ruleID: rule.id, reason: pack.name)
    }
    for pack in packages where pack.kind != .priority && pack.enabled {
      if let rule = pack.rules.first(where: {
        $0.enabled && matches($0, applicationID: applicationID, domain: domain, ip: ip)
      }) {
        return .init(
          destination: pack.destination, sourceID: pack.id, ruleID: rule.id, reason: pack.name)
      }
    }
    return .init(destination: .global, sourceID: nil, ruleID: nil, reason: "未命中规则")
  }
  func draftSummary(_ draft: RuleDraft) -> String {
    let destination =
      package(draft.packageID)?.kind == .priority
      ? draft.destination : package(draft.packageID)?.destination ?? .global
    guard let index = packages.firstIndex(where: { $0.id == draft.packageID }) else {
      return "规则包已不存在。"
    }
    let candidate: RulePackage
    do { candidate = try ruleProjection(draft, in: packages[index]).0 } catch {
      return (error as? EditError)?.message ?? "请输入有效目标。"
    }
    var proposed = packages
    proposed[index] = candidate
    let original = packages[index].rules.first { $0.id == draft.id }
    let draftMethod =
      draft.method == .application && draft.importDomains ? draft.domainMethod : draft.method
    let selectedTargets =
      (draft.method == .application && draft.importDomains ? draft.selectedDomains : [draft.target])
      .compactMap { try? validate(method: draftMethod, target: $0) }
    let affected = candidate.rules.filter {
      $0.method == draftMethod && selectedTargets.contains($0.target)
    }
    let targets = connections.filter { connection in
      affected.contains { rule in
        matches(
          rule, applicationID: connection.applicationID, domain: connection.domain,
          ip: connection.ip)
      }
        || original.map {
          matches(
            $0, applicationID: connection.applicationID, domain: connection.domain,
            ip: connection.ip)
        } == true
    }
    let exited = targets.filter { connection in
      original.map {
        matches(
          $0, applicationID: connection.applicationID, domain: connection.domain, ip: connection.ip)
      } == true
        && !affected.contains {
          matches(
            $0, applicationID: connection.applicationID, domain: connection.domain,
            ip: connection.ip)
        }
    }.count
    let sources = Set(
      targets.map { connection in
        let result = decision(
          in: proposed, applicationID: connection.applicationID, domain: connection.domain,
          ip: connection.ip)
        return result.reason + " → " + destinationName(result.destination)
      }
    ).sorted()
    let scope =
      draft.method == .application && draft.importDomains
      ? "保存为 \(draft.selectedDomains.count) 个选中域名；在其他应用也生效。\n" : ""
    let unavailable: String
    if case .route(let id) = destination, !availableRoutes.contains(where: { $0.id == id }) {
      unavailable = "指定线路不可用；保持指定并报错，不自动直连。\n"
    } else {
      unavailable = ""
    }
    if telemetry != nil {
      return scope + unavailable
        + "条目去向：\(destinationName(destination))\n按优先规则、自定义包、广告拦截、Geo 的顺序匹配；仅影响新连接。当前界面不估算历史连接受影响数量。"
    }
    return scope + unavailable
      + "条目去向：\(destinationName(destination))\n已观测 \(targets.count) 条连接，原范围退出 \(exited) 条；保存后匹配：\(sources.joined(separator: "、"))\n未观测影响未知；当前连接与历史保留。"
  }
  func movePackage(_ id: String, delta: Int) {
    guard canEdit, let index = packages.firstIndex(where: { $0.id == id }),
      [.local, .subscription].contains(packages[index].kind)
    else { return }
    let next = index + delta
    guard packages.indices.contains(next), [.local, .subscription].contains(packages[next].kind)
    else { return }
    packages.swapAt(index, next)
  }
  func removePackage(_ id: String) {
    guard canEdit, let pack = package(id), [.local, .subscription].contains(pack.kind),
      !pack.updating
    else { return }
    packages.removeAll { $0.id == id }
    selectedPackageID = packages.first?.id ?? "priority"
  }
  func removeRule(packageID: String, ruleID: String) {
    guard canEdit, let index = packages.firstIndex(where: { $0.id == packageID }),
      [.priority, .local].contains(packages[index].kind)
    else { return }
    packages[index].rules.removeAll { $0.id == ruleID }
  }
  func locate(_ hit: Hit) {
    guard let id = hit.sourceID, let pack = package(id),
      hit.ruleID == nil
        || pack.rules.contains(where: {
          $0.id == hit.ruleID && (hit.signature == nil || $0.signature == hit.signature)
        })
    else {
      packageMessage = "记录中的规则来源已不存在；实际命中记录保留。"
      return
    }
    selectedPackageID = id
    selectedRuleID = hit.ruleID
    perform(.tab(.rules))
    rulesSearch = ""
  }
  private func updatePackage(_ id: String, succeeds: Bool) async {
    guard canEdit, let index = packages.firstIndex(where: { $0.id == id }),
      [.geo, .subscription, .adblock].contains(packages[index].kind), !packages[index].updating
    else { return }
    packages[index].updating = true
    packages[index].updateMessage = "正在检查与校验新版本…"
    await Task.yield()
    guard let current = packages.firstIndex(where: { $0.id == id }) else { return }
    defer { packages[current].updating = false }
    guard canEdit, !Task.isCancelled else {
      packages[current].updateMessage = "操作中断，保留上一有效版本。"
      return
    }
    if succeeds {
      if packages[current].version == "2026.10.08" {
        packages[current].updateMessage = "已是最新有效版本；去向和覆盖保留。"
        return
      }
      let old = packages[current].version
      packages[current].previousVersion = old
      packages[current].previousRules = packages[current].rules
      let suffix = packages[current].kind == .geo ? "updated.cn" : "updated.example.com"
      packages[current].rules.append(
        .init(
          id: id + "-2026.10.08", method: .domainSuffix,
          target: suffix, destination: packages[current].destination))
      packages[current].version = "2026.10.08"
      packages[current].updateMessage = "已更新；去向和本地覆盖保留。"
    } else {
      packages[current].updateMessage = "更新失败，保留上一有效版本。"
    }
  }
}
