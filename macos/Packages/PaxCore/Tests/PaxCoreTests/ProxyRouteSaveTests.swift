import PaxClient
import Testing

@testable import PaxCore

@MainActor struct ProxyRouteSaveTests {
  @Test func outcomeWaitsForFinalResultAndDoesNotSelect() async throws {
    let routes = RoutesStore(
      preview: { _, _, _ in RoutesPreview(token: "", revision: 0, items: []) },
      cancel: { _ in }, cancelOperation: { _ in }, editor: { _ in RouteEdit() },
      command: { _ in "proxy-save" })
    routes.consume(
      .ready(
        DaemonHandshake(
          daemonVersion: "fixture", protocolVersion: 4, snapshotSeq: 1,
          capabilities: ["routes.documents", "routes.proxy.create"]),
        DaemonSnapshot(
          snapshotVersion: 1, status: .off, routes: [], currentRoute: nil,
          todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: [])))
    var edit = RouteEdit()
    edit.requestID = "stable-request"
    edit.documentJSON = "{}"
    let saving = Task { try await routes.saveDocument(edit) }
    for _ in 0..<100 {
      if routes.pendingID == "proxy-save" { break }
      await Task.yield()
    }
    #expect(routes.pendingID == "proxy-save")
    routes.consume(
      .events([
        .operation(
          OperationResult(
            operationID: "proxy-save", ok: true,
            error: nil, routeSave: RouteSaveResult(routeID: "existing", created: false)))
      ]))
    #expect(try await saving.value == RouteSaveResult(routeID: "existing", created: false))
    #expect(routes.selectedRouteID.isEmpty)
    #expect(routes.pendingID == nil)
  }
}
