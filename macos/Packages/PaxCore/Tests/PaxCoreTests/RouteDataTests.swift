import Foundation
import PaxClient
import Synchronization
import Testing

@testable import PaxCore

@MainActor struct RouteDataTests {
  @Test("测量分页保留旧页；流量刷新保留已展开范围，断线不伪装实时")
  func paginationAndRefresh() async {
    let calls = Mutex<[String]>([])
    let store = RoutesStore(
      preview: { _, _, _ in RoutesPreview(token: "", revision: 0, items: []) }, cancel: { _ in },
      cancelOperation: { _ in }, editor: { _ in RouteEdit() }, command: { _ in "op" },
      history: { _, cursor in
        RouteHistoryPage(
          items: [
            RouteHistorySample(
              metric: "latency", at: Date(timeIntervalSince1970: cursor.isEmpty ? 100 : 50),
              configRevision: 1, value: 20, success: true)
          ], nextCursor: cursor.isEmpty ? "h2" : "")
      },
      usage: { _, cursor, _ in
        calls.withLock { $0.append(cursor) }
        return RouteUsagePage(
          items: [
            RouteUsageBucket(
              at: Date(timeIntervalSince1970: cursor.isEmpty ? 100 : 50), upload: 12, download: 34)
          ], nextCursor: cursor.isEmpty ? "u2" : "", todayUpload: 24, todayDownload: 68,
          sampledAt: Date())
      })
    store.consume(
      .ready(
        DaemonHandshake(
          daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1,
          capabilities: ["routes.history", "routes.usage"]),
        DaemonSnapshot(
          snapshotVersion: 1, status: .off, routes: [], currentRoute: nil, todayTraffic: .zero,
          settings: DaemonSettings(autoConnect: false), recentLogs: [])))
    await store.loadRouteData("a")
    await store.loadRouteData("a", more: true)
    #expect(store.history["a"]?.count == 2)
    #expect(store.usage["a"]?.items.count == 2)
    await store.refreshRouteUsage("a")
    #expect(store.usage["a"]?.items.count == 2)
    #expect(store.history["a"]?.count == 2)
    store.consume(.disconnected)
    let before = calls.withLock { $0.count }
    await store.refreshRouteUsage("a")
    #expect(calls.withLock { $0.count } == before)
  }
}

extension RouteDataTests {
  @Test("测量历史与用量的更多按钮各自分页，互不推进另一类游标")
  func independentPageQueries() async {
    let historyCalls = Mutex(0)
    let usageCalls = Mutex(0)
    let store = RoutesStore(
      preview: { _, _, _ in RoutesPreview(token: "", revision: 0, items: []) }, cancel: { _ in },
      cancelOperation: { _ in }, editor: { _ in RouteEdit() }, command: { _ in "op" },
      history: { _, cursor in
        historyCalls.withLock { $0 += 1 }
        return RouteHistoryPage(items: [], nextCursor: cursor.isEmpty ? "h2" : "")
      },
      usage: { _, cursor, _ in
        usageCalls.withLock { $0 += 1 }
        return RouteUsagePage(items: [], nextCursor: cursor.isEmpty ? "u2" : "")
      })
    store.consume(
      .ready(
        DaemonHandshake(
          daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1,
          capabilities: ["routes.history", "routes.usage"]),
        DaemonSnapshot(
          snapshotVersion: 1, status: .off, routes: [], currentRoute: nil, todayTraffic: .zero,
          settings: DaemonSettings(autoConnect: false), recentLogs: [])))
    await store.loadRouteData("r")
    let usageBefore = usageCalls.withLock { $0 }
    await store.loadMeasurementHistory("r", more: true)
    #expect(usageCalls.withLock { $0 } == usageBefore)
    let historyBefore = historyCalls.withLock { $0 }
    await store.refreshRouteUsage("r", more: true)
    #expect(historyCalls.withLock { $0 } == historyBefore)
  }
}

@MainActor struct SelectedUsageWindowTests {
  @Test("三天时间窗一次读取完整分页，不只画最后一百个桶")
  func completeWindow() async {
    let calls = Mutex(0)
    let store = RoutesStore(
      preview: { _, _, _ in .init(token: "", revision: 0, items: []) }, cancel: { _ in },
      cancelOperation: { _ in }, editor: { _ in .init() }, command: { _ in "" },
      usage: { _, cursor, _ in
        calls.withLock { $0 += 1 }
        let page = Int(cursor) ?? 0
        return .init(
          items: [.init(at: Date(timeIntervalSince1970: Double(page)), upload: 1, download: 2)],
          nextCursor: page < 8 ? String(page + 1) : "")
      })
    store.consume(
      .ready(
        .init(
          daemonVersion: "test", protocolVersion: 23, snapshotSeq: 1, capabilities: ["routes.usage"]
        ),
        .init(
          snapshotVersion: 1, status: .off, routes: [], currentRoute: nil, todayTraffic: .zero,
          settings: .init(autoConnect: false), recentLogs: [])))
    await store.setUsageWindow("fixture", from: Date().addingTimeInterval(-259200))
    #expect(calls.withLock { $0 } == 9)
    #expect(store.usage["fixture"]?.items.count == 9)
    #expect(store.usageCursors["fixture"] == "")
  }
}
