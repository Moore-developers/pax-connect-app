import Synchronization
import Testing

@testable import PaxCore

@MainActor struct RuntimeSelectionTests {
  @Test func runtimeCapabilityEnablesSwitchAndCancelKeepsAuthoritativeSelection() async throws {
    let cancelled = Mutex<[String]>([])
    let routes = RoutesStore(
      preview: { _, _, _ in RoutesPreview(token: "", revision: 0, items: []) },
      cancel: { _ in }, cancelOperation: { id in cancelled.withLock { $0.append(id) } },
      editor: { _ in RouteEdit() }, command: { _ in "switch" })
    let store = ConnectionStore(routesStore: routes)
    let a = NodeProfile(
      id: "a", name: "A", protocolName: "trojan", server: "example.test", port: 443, group: "local")
    let b = NodeProfile(
      id: "b", name: "B", protocolName: "trojan", server: "example.test", port: 443, group: "local")
    let snapshot = DaemonSnapshot(
      snapshotVersion: 1, status: .degraded, routes: [a, b], currentRoute: a,
      todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: [],
      selectedRouteID: "a")
    store.consume(
      .ready(
        DaemonHandshake(
          daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1,
          capabilities: ["routes.select.off"]), snapshot))
    #expect(!store.canSelectRoute)
    store.consume(
      .ready(
        DaemonHandshake(
          daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1,
          capabilities: ["routes.select.off", "routes.select.running"]), snapshot))
    #expect(store.canSelectRoute)
    await store.selectRoute("b")
    #expect(routes.selectedRouteID == "a")
    try await routes.cancelSelection()
    #expect(cancelled.withLock { $0 } == ["switch"])
    #expect(routes.selectedRouteID == "a")
    store.consume(
      .events([
        .operation(
          OperationResult(
            operationID: "switch", ok: false, error: ProductError(code: "operation_cancelled")))
      ]))
    #expect(routes.selectedRouteID == "a")
    #expect(routes.selectionError?.code == "operation_cancelled")
  }
}
