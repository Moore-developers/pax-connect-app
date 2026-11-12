import Foundation
import Observation
import PaxCore
import PaxUI

/// 隔离的 UI 演示状态：只存在于演示会话，不持久化、不创建客户端、不发 RPC。
/// 线路、流量和事件复用已有产品模型；图表/明细仅是隔离的视图输入。
@MainActor
@Observable
public final class WorkbenchDemoStore {
  public enum Scenario: String, CaseIterable {
    case normal = "正常"
    case off = "已关闭"
    case empty = "无线路"
    case starting = "连接中"
    case degraded = "保护降级"
    case disconnected = "后台断线"
    case loading = "统计加载"
    case failed = "操作失败"
    case trafficMissing = "暂无统计"
    case trafficFailed = "统计失败"
    case longText = "长文本"
    case dense = "密集数据"
    case few = "少量数据"
    case establishmentMissing = "建立耗时缺测"
    case trafficIncomplete = "流量记录不完整"
  }
  public enum Dimension: Int, CaseIterable { case application, domain, ip }
  public enum Period: Int { case today, week }

  public private(set) var scenario: Scenario = .normal
  public private(set) var dimension: Dimension = .application
  public private(set) var period: Period = .today
  public private(set) var search = ""
  public private(set) var direction = "全部"
  public private(set) var selectedRouteID = "vless-fast"
  public private(set) var currentRouteID = "vless-fast"
  private var transition: Task<Void, Never>?
  private var generation: UInt64 = 0

  public enum Intent {
    case selectScenario(Scenario)
    case selectDimension(Dimension)
    case selectPeriod(Period)
    case search(String)
    case filterDirection(String)
    case toggleProxy
    case selectRoute(String)
    case addSampleRoute
  }
  public let catalog: RoutesStore
  public let connection: ConnectionStore
  private var snapshotSequence: UInt64 = 0
  let trafficCapturedAt: Date
  public init() {
    let commands = DemoCommands()
    let catalog = RoutesStore(
      preview: { _, _, _ in RoutesPreview(token: "demo", revision: 1, items: []) },
      cancel: { _ in }, cancelOperation: { _ in }, editor: { _ in RouteEdit() },
      command: { intent in await commands.perform(intent) })
    self.catalog = catalog
    self.connection = ConnectionStore(routesStore: catalog)
    trafficCapturedAt =
      Calendar.current.date(bySettingHour: 14, minute: 32, second: 0, of: Date()) ?? Date()
    commands.owner = self
    publishSnapshot()
  }
  public func perform(_ intent: Intent) {
    defer { publishSnapshot() }
    switch intent {
    case .selectScenario(let scenario): selectScenario(scenario)
    case .selectDimension(let dimension): self.dimension = dimension
    case .selectPeriod(let period): self.period = period
    case .search(let text): search = text
    case .filterDirection(let direction): self.direction = direction
    case .toggleProxy: toggleProxy()
    case .selectRoute(let id): selectRoute(id)
    case .addSampleRoute: addSampleRoute()
    }
  }

  private var managedRoutes: [NodeProfile]?
  private var managedRouteHistories: [String: [PaxRTTSample]] = [:]
  public func replaceManagedRoutes(
    _ routes: [NodeProfile], histories: [String: [PaxRTTSample]]
  ) {
    managedRoutes = routes
    managedRouteHistories = histories
    publishSnapshot()
  }

  public var routes: [NodeProfile] { catalog.routes }
  private var fixtureRoutes: [NodeProfile] {
    if let managedRoutes { return managedRoutes }
    return qualityItems.map { item in
      NodeProfile(
        id: item.id, name: item.name, protocolName: item.protocolName.lowercased(),
        server: item.id + ".example.net", port: 443, group: item.source, revision: 1,
        testSummary: item.samples.last?.milliseconds.map { value in
          RouteTestSummary(
            at: Date(timeIntervalSince1970: 1_791_006_600), success: true,
            durationMS: UInt32(value), testedRevision: 1)
        })
    }
  }
  public var qualityItems: [PaxRouteQualityItem] {
    guard scenario != .empty else { return [] }
    // 2026-10-03 v2rayN 本地只读快照：有效 Delay 升序前五，不包含连接凭据。
    let definitions: [(String, String, String, String, [Double])] = [
      (
        "vless-fast", "洛杉矶-直连-日常快线", "VLESS", "洛杉矶节点-RN",
        [214, 208, 202, 219, 232, 210, 205, 198, 203, 200, 197, 195]
      ),
      (
        "hy2", "洛杉矶-直连-弱网加速", "Hysteria2", "洛杉矶节点-RN",
        [230, 218, 211, 207, 220, 202, 199, 208, 204, 201, 198, 196]
      ),
      (
        "vmess", "洛杉矶-CF中转-防封-备用", "VMess", "洛杉矶节点-RN",
        [244, 232, 219, 227, 212, 235, 218, 207, 215, 204, 201, 198]
      ),
      (
        "vmess-primary", "洛杉矶-CF中转-防封-首选", "VMess", "洛杉矶节点-RN",
        [218, 225, 207, 212, 230, 220, 206, 204, 211, 202, 200, 198]
      ),
      (
        "vmess-backup2", "洛杉矶-CF中转-防封-备用2", "VMess", "洛杉矶节点-RN",
        [264, 248, 232, 310, 278, 240, 224, 218, 230, 213, 208, 202]
      ),
    ]
    let visible = scenario == .few ? Array(definitions.prefix(1)) : definitions
    let base = visible.map { id, name, proto, source, values in
      // 用户授权补齐确定的演示历史；末点保留v2rayN快照测速。
      let samples = values.enumerated().map { index, value in
        let minute = 35 + index * 5
        let time = "\(13 + minute / 60):" + String(format: "%02d", minute % 60)
        return PaxRTTSample(time: time, outcome: .measured(value))
      }
      return PaxRouteQualityItem(
        id: id,
        name: scenario == .longText && id == "vless-fast" ? name + " · 用于换行检查的长名称" : name,
        protocolName: proto, source: source, samples: samples)
    }
    guard let managedRoutes else { return base }
    let managedVisible = scenario == .few ? Array(managedRoutes.prefix(1)) : managedRoutes
    return managedVisible.map { route in
      let samples = managedRouteHistories[route.id] ?? []
      return PaxRouteQualityItem(
        id: route.id,
        name: scenario == .longText && route.id == "vless-fast"
          ? route.name + " · 长名称显示测试" : route.name, protocolName: route.protocolName.uppercased(),
        source: route.group, samples: samples)
    }
  }
  public var route: NodeProfile? {
    routes.first {
      $0.id == (isRunning || scenario == .disconnected ? currentRouteID : selectedRouteID)
    }
  }
  public var isRunning: Bool { ![.off, .empty, .starting, .disconnected].contains(scenario) }
  public var canOperate: Bool { ![.empty, .disconnected].contains(scenario) }
  public var statusTitle: String {
    switch scenario {
    case .off: "已关闭"
    case .empty: "还没有线路"
    case .starting: "正在连接"
    case .degraded: "保护降级"
    case .disconnected: "后台已断开"
    default: "已保护"
    }
  }
  public var statusCaption: String {
    switch scenario {
    case .off: "未接管网络，保留今日历史"
    case .empty: "添加线路后即可开始"
    case .starting: "等待启动结果，尚未完成保护检查"
    case .degraded: "IPv6 保护检查未通过；当前保护不完整"
    case .disconnected: "最后同步 14:32；当前网络状态未知"
    case .failed: "线路切换失败，继续使用原线路"
    case .longText: "连接检查通过。长说明在紧凑窗口中完整换行，操作入口保持可达，不靠截断隐藏重要状态。"
    default: "网络接管与保护检查通过"
    }
  }
  public var tone: PaxBadge.Tone {
    switch scenario {
    case .degraded: .warning
    case .disconnected, .off, .empty, .starting: .neutral
    default: .healthy
    }
  }
  public var traffic: TrafficSnapshot {
    let scale: UInt64 = period == .today ? 1 : 7
    return TrafficSnapshot(
      uploadBytes: 220_000_000 * scale, downloadBytes: 2_180_000_000 * scale,
      proxiedBytes: 1_800_000_000 * scale, directBytes: 504_000_000 * scale,
      unclassifiedBytes: 96_000_000 * scale, activeConnections: 24)
  }
  public var samples: [PaxChartSample] {
    let totals: [Double] =
      period == .today
      ? [0.04, 0.06, 0.04, 0.04, 0.04, 0.06, 0.10, 0.16, 0.26, 0.34, 0.20, 0.18, 0.26, 0.36, 0.26]
      : [1.92, 2.40, 1.80, 2.32, 3.08, 2.56, 2.72]
    return totals.enumerated().map { index, value in
      PaxChartSample(
        period == .today
          ? "\(index):00" : ["9月26日", "9月27日", "9月28日", "9月29日", "9月30日", "10月1日", "今天"][index],
        values: [value * 0.75, value * 0.21, value * 0.04])
    }
  }

  /// 统计与不同维度使用同一周期；只展示部分明细，不把跨维度值相加。
  public var detailRows: [PaxDataTable.Row] {
    guard ![.empty, .loading, .trafficMissing, .trafficFailed].contains(scenario) else { return [] }
    let factor: UInt64 = period == .today ? 1 : 7
    let values: [(String, String, String, UInt64, UInt64)]
    switch dimension {
    case .application:
      values = [
        ("Safari", "网页浏览", "代理 · hy2-racknerd-132944b", 1_320_000_000, 84_000_000),
        ("Telegram", "消息与媒体", "代理 · hy2-racknerd-132944b", 286_000_000, 32_000_000),
        ("系统服务", "本地网络", "直连", 448_000_000, 38_000_000),
        ("未知应用", "归属信息不足", "未归类", 74_000_000, 18_000_000),
      ]
    case .domain:
      values = [
        ("example.net", "域名观测", "代理 · hy2-racknerd-132944b", 620_000_000, 48_000_000),
        ("example.cn", "域名观测", "直连", 180_000_000, 12_000_000),
        ("未识别域名", "没有域名信息", "未归类", 74_000_000, 18_000_000),
      ]
    case .ip:
      values = [
        ("203.0.113.24", "文档示例地址", "代理 · hy2-racknerd-132944b", 310_000_000, 24_000_000),
        ("192.168.1.10", "局域网", "直连", 160_000_000, 8_000_000),
        ("2001:db8::24", "文档示例地址", "未归类", 74_000_000, 18_000_000),
      ]
    }
    let count = scenario == .dense ? 40 : values.count
    return (0..<count).compactMap { index in
      let item = values[index % values.count]
      let name =
        scenario == .longText && index == 0
        ? "Safari · 一个用于检验表格截断与详情显示的较长应用名称" : item.0
      guard direction == "全部" || item.2.hasPrefix(direction),
        search.isEmpty || (name + item.1 + item.2).localizedCaseInsensitiveContains(search)
      else { return nil }
      let download = scenario == .dense ? item.3 / 10 : item.3
      let upload = scenario == .dense ? item.4 / 10 : item.4
      return PaxDataTable.Row(
        id: "\(dimension.rawValue)-\(index)",
        values: [
          name, item.2, TrafficFormatting.bytes(download * factor),
          TrafficFormatting.bytes(upload * factor),
        ], subtitle: item.1)
    }
  }
  public var events: [PaxTimeline.Entry] {
    guard scenario != .empty else { return [] }
    let heading: String
    switch scenario {
    case .degraded: heading = "IPv6 保护检查未通过"
    case .disconnected: heading = "与后台服务断开"
    case .failed: heading = "线路切换失败，保留原线路"
    case .off: heading = "代理已关闭"
    case .starting: heading = "已提交连接请求，等待结果"
    default: heading = "保护检查通过"
    }
    return [
      PaxTimeline.Entry(
        id: "status", time: "14:32", title: heading, detail: statusCaption, tone: tone),
      PaxTimeline.Entry(
        id: "test", time: "14:28", title: "手动测试完成", detail: "hy2-racknerd-132944b · 单次连接测试 196 ms"),
    ]
  }
  private func selectScenario(_ scenario: Scenario) {
    transition?.cancel()
    generation &+= 1
    self.scenario = scenario
    if let first = qualityItems.first {
      if !qualityItems.contains(where: { $0.id == selectedRouteID }) { selectedRouteID = first.id }
      if !qualityItems.contains(where: { $0.id == currentRouteID }) { currentRouteID = first.id }
    }
  }
  private func toggleProxy() {
    guard canOperate else { return }
    if scenario == .starting || isRunning {
      selectScenario(.off)
      return
    }
    selectScenario(.starting)
    let token = generation
    transition = Task { [weak self] in
      do { try await Task.sleep(for: .milliseconds(700)) } catch { return }
      guard let self, self.generation == token else { return }
      self.currentRouteID = self.selectedRouteID
      self.scenario = .normal
      self.publishSnapshot()
    }
  }
  private func selectRoute(_ id: String) {
    guard canOperate, scenario != .starting, routes.contains(where: { $0.id == id }) else { return }
    selectedRouteID = id
    if isRunning { currentRouteID = id }
  }
  private func addSampleRoute() {
    selectScenario(.off)
  }
  public func cancelPendingAction() {
    transition?.cancel()
    generation &+= 1
  }
  func replaceRouteCatalog(_ nodes: [NodeProfile]) {
    managedRoutes = nodes
    publishSnapshot()
  }
  private func publishSnapshot() {
    snapshotSequence &+= 1
    let nodes = fixtureRoutes
    let status: ProtectionStatus = scenario == .starting ? .starting : isRunning ? .protected : .off
    let snapshot = DaemonSnapshot(
      snapshotVersion: snapshotSequence, status: status, routes: nodes,
      currentRoute: isRunning ? nodes.first { $0.id == currentRouteID } : nil,
      todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: [],
      catalogRevision: snapshotSequence, selectedRouteID: selectedRouteID)
    if connection.connection == .connected {
      connection.consume(.events([.snapshot(snapshot)]))
    } else {
      connection.consume(
        .ready(
          DaemonHandshake(
            daemonVersion: "offline-demo", protocolVersion: 4,
            snapshotSeq: snapshotSequence, capabilities: ["routes.catalog", "routes.select.off"]),
          snapshot))
    }
    if scenario == .disconnected { connection.consume(.disconnected) }
  }

}

@MainActor private final class DemoCommands {
  weak var owner: WorkbenchDemoStore?
  func perform(_ intent: RoutesStore.Intent) -> String {
    if case .select(let node) = intent { owner?.perform(.selectRoute(node.id)) }
    let id = UUID().uuidString
    owner?.connection.consume(
      .events([.operation(OperationResult(operationID: id, ok: true, error: nil))]))
    return id
  }
}
