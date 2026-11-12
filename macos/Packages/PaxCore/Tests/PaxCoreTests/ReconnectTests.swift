import Synchronization
import Testing

@testable import PaxCore

@MainActor @Suite("代理重连")
struct ReconnectTests {
  @Test("重连发送独立意图，不用关闭代理代替；旧后台禁用")
  func reconnectIntent() async {
    let calls = Mutex<[String]>([])
    let routes = RoutesStore(
      preview: { _, _, _ in RoutesPreview(token: "", revision: 0, items: []) }, cancel: { _ in },
      cancelOperation: { _ in }, editor: { _ in RouteEdit() }, command: { _ in "unused" })
    let store = ConnectionStore(
      proxyCommand: { _ in
        calls.withLock { $0.append("power") }
        return "power"
      },
      routesStore: routes,
      reconnectCommand: {
        calls.withLock { $0.append("reconnect") }
        return "reconnect"
      })
    let snapshot = DaemonSnapshot(
      snapshotVersion: 1, status: .degraded, routes: [], currentRoute: nil,
      todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: [],
      degradedReason: ProductError(code: "route_lost"))
    store.consume(
      .ready(
        DaemonHandshake(
          daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1, capabilities: ["proxy.power"]),
        snapshot))
    #expect(!store.canReconnect)
    await store.reconnectProxy()
    #expect(calls.withLock { $0 }.isEmpty)
    store.consume(
      .ready(
        DaemonHandshake(
          daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1,
          capabilities: ["proxy.power", "proxy.reconnect"]), snapshot))
    #expect(store.canReconnect)
    #expect(store.feedback.contains { $0.id == "degraded" && $0.action == .retryConnection })
    await store.reconnectProxy()
    #expect(calls.withLock { $0 } == ["reconnect"])
    #expect(store.pendingOperationID == "reconnect")
    #expect(store.status == .degraded)
    store.consume(.disconnected)
    #expect(!store.canReconnect)
  }
}
