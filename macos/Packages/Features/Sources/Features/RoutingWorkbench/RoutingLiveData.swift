import Foundation
import PaxCore
import PaxUI

extension RoutingWorkbenchStore {
  convenience init(telemetry: TelemetryStore) {
    self.init(session: WorkbenchDemoStore(), backendAvailable: false, initialPeriod: .day)
    self.telemetry = telemetry
  }
  static func liveRow(_ row: PaxCore.TrafficRow) -> TrafficRow {
    var value = TrafficRow(
      id: row.id, name: row.name,
      subtitle: row.direction == "direct" ? "直连" : row.direction == "proxy" ? "代理" : "去向未采集",
      symbol: "network", route: row.routeName,
      direction: row.direction == "direct" ? .direct : row.direction == "proxy" ? .proxy : .unknown,
      connections: [], upload: row.upload, download: row.download,
      establishment: workbenchLatencySamples(row))
    value.aggregateCount = Int(row.connections)
    value.aggregateBasis = TrafficRuleFormatting.latestTitle(row.latestMatch)
    value.latestLatency = TrafficFormatting.latestLatency(row)
    value.aggregatePeak = row.latencyKnown ? Double(row.slowestLatencyMS) : nil
    return value
  }
  var dataMessage: String? {
    if let telemetry {
      if !liveError.isEmpty && (liveDataInvalid || livePage.items.isEmpty) {
        return "流量读取失败，请重试"
      }
      if liveDataInvalid || (liveLoading && livePage.items.isEmpty) { return "正在读取流量" }
      return telemetry.trafficAvailable ? nil : "后台流量暂不可用"
    }
    if !backendAvailable { return "流量数据尚未接入后台" }
    switch session.scenario {
    case .loading: return "正在加载流量记录…"
    case .trafficMissing: return "暂无统计；不能将缺失数据表示为零。"
    case .trafficFailed: return "统计读取失败，请重试。"
    default: return nil
    }
  }
  var stateMessage: String {
    if let telemetry { return liveError.isEmpty ? telemetry.qualityMessage : liveError }
    if !backendAvailable { return "流量与规则尚未接入后台，暂不可更改。" }
    var messages: [String] = []
    if session.scenario == .disconnected {
      messages.append("后台服务断开，保留最后记录 · " + TrafficFormatting.time(now))
    }
    if scene == .incomplete { messages.append("记录不完整；采集曾中断，只包含已记录字节。") }
    if !messages.isEmpty { return messages.joined(separator: "\n") }
    if session.scenario == .degraded { return "保护降级；分流记录不代表保护检查已通过。" }
    return ""
  }
  func reloadLive(more: Bool = false, force: Bool = true, automatic: Bool = false) async {
    guard let telemetry, telemetry.trafficAvailable, tab == .traffic else { return }
    if more && (liveNeedsRead || livePage.readToken.isEmpty || livePage.nextCursor.isEmpty) {
      return
    }
    let rebuilding = liveNeedsRead
    let previous = livePage
    if rebuilding { pendingLivePage = nil }
    liveGeneration &+= 1
    let generation = liveGeneration
    liveLoading = true
    if rebuilding || previous.items.isEmpty { liveError = "" }
    let session = telemetry.sessionGeneration
    let invalidation = telemetry.trafficInvalidationGeneration
    let day = Calendar.current.startOfDay(for: Date())
    defer {
      if generation == liveGeneration {
        liveLoading = false
        let followUp = liveRefreshPending
        liveRefreshPending = false
        // PaxRefresh has already admitted this read. Finish the current request first,
        // then fulfill its one coalesced successor, including after a read failure.
        if followUp && !Task.isCancelled, session == telemetry.sessionGeneration,
          invalidation == telemetry.trafficInvalidationGeneration,
          telemetry.isShown(.traffic, focus: false), tab == .traffic
        {
          scheduleLive(force: true, automatic: true)
        }
      }
    }
    var filter = TrafficFilter()
    filter.limit = 15
    filter.dimension = ["application", "domain", "system"][dimension.rawValue]
    filter.direction = ["all", "proxy", "direct"][direction.rawValue]
    filter.window = period.window
    filter.search = search
    if more {
      filter.cursor = livePage.nextCursor
      filter.readToken = livePage.readToken
    }
    do {
      var page = try await telemetry.traffic(filter, force: force)
      guard !Task.isCancelled, generation == liveGeneration, session == telemetry.sessionGeneration,
        telemetry.connected, invalidation == telemetry.trafficInvalidationGeneration,
        period != .day || day == Calendar.current.startOfDay(for: Date())
      else { return }
      if more {
        guard previous.readToken == page.readToken, previous.asOf == page.asOf else {
          liveError = "流量快照已变化，请重新读取"
          return
        }
        page.items = previous.items + page.items
        livePaginated = true
        liveFrozen = true
      } else if automatic && !rebuilding && livePaginated {
        // Keep the complete old token/cursor/count snapshot. Never splice new first-page rows into it.
        pendingLivePage = page
        liveError = ""
        return
      } else if automatic && !rebuilding && liveFrozen {
        // A single read contains at most 15 rows. If a displayed identity falls out
        // of it, retain the complete valid old snapshot rather than scan an unbounded
        // ranking or attach old values to a new token. Resume adopts this candidate.
        pendingLivePage = page
        let wanted = Set(previous.items.map(\.id))
        let latest = Dictionary(uniqueKeysWithValues: page.items.map { ($0.id, $0) })
        guard wanted.isSubset(of: Set(latest.keys)) else {
          liveError = ""
          return
        }
        page.items = previous.items.compactMap { latest[$0.id] }
        // Resume to the complete first page before allowing cursor pagination.
        page.nextCursor = ""
      } else {
        livePaginated = false
        pendingLivePage = nil
      }
      livePage = page
      liveError = ""
      now = page.asOf
    } catch {
      if !Task.isCancelled && generation == liveGeneration {
        liveError = ProductErrorPresentation.display(error)
      }
    }
  }
  func scheduleLive(
    delay: Bool = false, force: Bool = false, more: Bool = false, automatic: Bool = false
  ) {
    if automatic && liveLoading && !liveNeedsRead {
      liveRefreshPending = true
      return
    }
    liveRefreshPending = false
    liveQueryTask?.cancel()
    liveGeneration &+= 1
    liveQueryTask = Task { [weak self] in
      if delay { do { try await Task.sleep(for: .milliseconds(300)) } catch { return } }
      guard let self, self.telemetry?.isShown(.traffic, focus: false) == true else { return }
      await self.reloadLive(more: more, force: force, automatic: automatic)
    }
  }
  func resumeLiveStructure() {
    liveFrozen = false
    guard !livePaginated, !liveNeedsRead, let page = pendingLivePage else { return }
    livePage = page
    pendingLivePage = nil
  }
  func stopLive() {
    liveRefreshPending = false
    liveLoading = false
    liveQueryTask?.cancel()
    liveQueryTask = nil
    liveGeneration &+= 1
  }

}
