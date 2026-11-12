import Foundation
import Testing

@testable import PaxCore

private actor TrafficRequests {
  var count = 0
  var cancelled = 0
  func load(_ q: TrafficFilter) async throws -> TrafficPage {
    count += 1
    do { try await Task.sleep(for: .milliseconds(80)) } catch {
      cancelled += 1
      throw error
    }
    var page = TrafficPage()
    page.upload = 12
    return page
  }
}
@Suite("共享历史查询") @MainActor
struct TelemetryQueryTests {
  @Test("范围证据缺失与旧协议删除仍作废；边界时刻不被前缀删除")
  func scopedRetentionBoundary() {
    let before = Date(timeIntervalSince1970: 1000)
    let previous = HistoryState()
    var next = previous
    next.logDeletionGeneration = 1
    next.logRemovalBeforeMs = 1_000_000
    #expect(!next.invalidatesLogs(since: previous, from: before))
    #expect(next.invalidatesLogs(since: previous, from: nil))
    next.logRemovalBeforeMs = 0
    #expect(next.invalidatesLogs(since: previous, from: before))
    let legacy = TelemetryStore()
    legacy.setConnection(connected: true, capabilities: ["history.logs.v1"])
    var delta = HistoryDelta()
    delta.history.clearGeneration = 1
    legacy.consume(.events([.historyDelta(delta)]))
    #expect(legacy.logsInvalidated(since: previous, from: before))
  }

  @Test("范围外淘汰保留日志缓存，受影响查询和清空仍撤销")
  func scopedLogRetention() async throws {
    let requests = LogRetentionRequests()
    let store = TelemetryStore(logs: { try await requests.load($0) })
    store.setConnection(connected: true, capabilities: ["history.logs.v1", "history.refresh.v2"])
    let now = Date()
    var recent = LogFilter()
    recent.from = now.addingTimeInterval(-1800)
    var broad = LogFilter()
    broad.from = now.addingTimeInterval(-90000)
    _ = try await store.logs(recent)
    _ = try await store.logs(broad)
    var delta = HistoryDelta()
    delta.history.logDeletionGeneration = 1
    delta.history.logRemovalBeforeMs = Int64(
      now.addingTimeInterval(-86400).timeIntervalSince1970 * 1000)
    store.consume(.events([.historyDelta(delta)]))
    _ = try await store.logs(recent)
    #expect(await requests.count == 2)
    _ = try await store.logs(broad)
    #expect(await requests.count == 3)
    delta.history.summaryGeneration = "cleared"
    store.consume(.events([.historyDelta(delta)]))
    _ = try await store.logs(recent)
    #expect(await requests.count == 4)
  }

  @Test("范围外淘汰不取消在途日志，范围内淘汰取消")
  func scopedLogFlight() async throws {
    for affected in [false, true] {
      let requests = LogRetentionRequests()
      let store = TelemetryStore(logs: { try await requests.load($0) })
      store.setConnection(connected: true, capabilities: ["history.logs.v1", "history.refresh.v2"])
      let now = Date()
      var filter = LogFilter()
      filter.from = now.addingTimeInterval(affected ? -90000 : -1800)
      let task = Task { try await store.logs(filter) }
      while await requests.count == 0 { await Task.yield() }
      var delta = HistoryDelta()
      delta.history.logDeletionGeneration = 1
      delta.history.logRemovalBeforeMs = Int64(
        now.addingTimeInterval(-86400).timeIntervalSince1970 * 1000)
      store.consume(.events([.historyDelta(delta)]))
      do {
        _ = try await task.value
        #expect(!affected)
      } catch { #expect(affected) }
    }
  }

  @Test("相同查询共用一次RPC，取消一个读者不取消另一个")
  func sharedAndCancelled() async throws {
    let requests = TrafficRequests()
    let store = TelemetryStore(traffic: { try await requests.load($0) })
    store.setConnection(connected: true, capabilities: ["history.traffic.v1"])
    let q = TrafficFilter()
    let first = Task { try await store.traffic(q) }
    let second = Task { try await store.traffic(q) }
    await Task.yield()
    first.cancel()
    let page = try await second.value
    #expect(page.upload == 12)
    #expect(await requests.count == 1)
    #expect(await requests.cancelled == 0)
    let cached = try await store.traffic(q)
    #expect(cached.upload == 12)
    #expect(await requests.count == 1)
  }
  @Test("隐藏或失焦页面不具备刷新资格")
  func visibility() {
    let store = TelemetryStore()
    store.setPresentation(page: .logs, windowVisible: true, appActive: true)
    #expect(store.isVisible(.logs))
    #expect(!store.isVisible(.dashboard))
    store.setAppActive(false)
    #expect(!store.isVisible(.logs))
  }
  @Test("最后一个消费者离开会取消底层RPC")
  func lastConsumerCancels() async throws {
    let requests = TrafficRequests()
    let store = TelemetryStore(traffic: { try await requests.load($0) })
    store.setConnection(connected: true, capabilities: ["history.traffic.v1"])
    let task = Task { try await store.traffic(TrafficFilter()) }
    while await requests.count == 0 { await Task.yield() }
    task.cancel()
    _ = try? await task.value
    #expect(await requests.cancelled == 1)
    #expect(store.queryCounts().cancellations == 1)
  }
  @Test("流量与日志删除代次均撤销复合流量缓存")
  func independentInvalidation() async throws {
    let requests = TrafficRequests()
    let store = TelemetryStore(traffic: { try await requests.load($0) })
    store.setConnection(
      connected: true, capabilities: ["history.traffic.v1", "history.refresh.v2"])
    let task = Task { try await store.traffic(TrafficFilter()) }
    while await requests.count == 0 { await Task.yield() }
    var delta = HistoryDelta()
    delta.history.trafficDeletionGeneration = 1
    store.consume(.events([.historyDelta(delta)]))
    do {
      _ = try await task.value
      Issue.record("invalidated request escaped")
    } catch {}
    _ = try await store.traffic(TrafficFilter())
    delta.history.logDeletionGeneration = 1
    store.consume(.events([.historyDelta(delta)]))
    _ = try await store.traffic(TrafficFilter())
    #expect(await requests.count == 3)
  }

  @Test("清理与重建不撤销独立详情快照的在途读取")
  func frozenDetailSurvivesInvalidation() async throws {
    for domain in ["delete", "summary", "journal", "traffic"] {
      let requests = TrafficRequests()
      let store = TelemetryStore(trafficDetail: { _ in
        _ = try await requests.load(TrafficFilter())
        var detail = TrafficDetail()
        detail.readToken = "opening-snapshot"
        return detail
      })
      store.setConnection(
        connected: true, capabilities: ["history.traffic.v1", "history.refresh.v2"])
      let task = Task { try await store.trafficDetail(TrafficDetailQuery()) }
      while await requests.count == 0 { await Task.yield() }
      var delta = HistoryDelta()
      switch domain {
      case "delete": delta.history.logDeletionGeneration = 1
      case "summary": delta.history.summaryGeneration = "next"
      case "journal": delta.history.journalGeneration = "next"
      default: delta.history.trafficDeletionGeneration = 1
      }
      store.consume(.events([.historyDelta(delta)]))
      #expect(try await task.value.readToken == "opening-snapshot")
    }
  }

  @Test("后台会话变化仍拒收旧详情响应")
  func frozenDetailRejectsPreviousSession() async throws {
    let requests = TrafficRequests()
    let store = TelemetryStore(trafficDetail: { _ in
      _ = try await requests.load(TrafficFilter())
      return TrafficDetail()
    })
    store.setConnection(connected: true, capabilities: ["history.traffic.v1"])
    let task = Task { try await store.trafficDetail(TrafficDetailQuery()) }
    while await requests.count == 0 { await Task.yield() }
    store.setConnection(connected: false, capabilities: ["history.traffic.v1"])
    store.setConnection(connected: true, capabilities: ["history.traffic.v1"])
    do {
      _ = try await task.value
      Issue.record("previous session detail escaped")
    } catch is CancellationError {}
  }

  @Test("当前通道通过只安静处理未验证，保留证书和其它真实降级")
  func quietCheckPreservesWarnings() {
    let store = ConnectionStore()
    var snapshot = DaemonSnapshot(
      snapshotVersion: 1, status: .degraded, routes: [], currentRoute: nil, todayTraffic: .zero,
      settings: DaemonSettings(autoConnect: false), recentLogs: [],
      degradedReason: .init(code: "protection_unverified"),
      activeTLS: ActiveTLS(
        routeID: "r", identityToken: "id", mode: .skipVerify, verified: false, skipped: true),
      degradedReasons: [.init(code: "protection_unverified"), .init(code: "ipv6_unavailable")])
    snapshot.currentPathCheck.state = "passed"
    store.markHandshaked(
      DaemonHandshake(
        daemonVersion: "test", protocolVersion: 6, snapshotSeq: 1,
        capabilities: ["connection.current_path.v1"]), snapshot: snapshot)
    #expect(store.status == .degraded)
    #expect(!store.feedback.contains { $0.id == "degraded" })
    #expect(store.feedback.contains { $0.id == "degraded:tls_unverified" })
    #expect(store.feedback.contains { $0.id == "degraded:ipv6_unavailable" })
  }

}

extension TelemetryQueryTests {
  @Test("日志删除不取消概览network/today首读；仍撤销分析及其令牌")
  func logDeletionKeepsIndependentTodayFlight() async throws {
    let requests = TrafficRequests()
    let store = TelemetryStore(traffic: { try await requests.load($0) })
    store.setConnection(
      connected: true, capabilities: ["history.traffic.v1", "history.refresh.v2"])
    var network = TrafficFilter()
    network.dimension = "network"
    network.window = "today"
    let today = Task { try await store.traffic(network) }
    let analysis = Task { try await store.traffic(TrafficFilter()) }
    while await requests.count < 2 { await Task.yield() }
    var delta = HistoryDelta()
    delta.history.logDeletionGeneration = 1
    store.consume(.events([.historyDelta(delta)]))
    #expect(try await today.value.upload == 12)
    do {
      _ = try await analysis.value
      Issue.record("analysis token survived log deletion")
    } catch {}
    #expect(await requests.count == 2)
    #expect(store.signals[.trafficData]?.generation == 0)
    #expect(store.signals[.traffic]?.generation == 1)
    delta.history.trafficDeletionGeneration = 1
    let another = Task { try await store.traffic(network, force: true) }
    while await requests.count < 3 { await Task.yield() }
    store.consume(.events([.historyDelta(delta)]))
    do {
      _ = try await another.value
      Issue.record("deleted traffic survived")
    } catch {}
  }
}

private actor LogRetentionRequests {
  var count = 0
  func load(_ filter: LogFilter) async throws -> HistoryLogPage {
    count += 1
    try await Task.sleep(for: .milliseconds(80))
    return HistoryLogPage()
  }
}
