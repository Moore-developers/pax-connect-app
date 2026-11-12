import Synchronization
import Testing

@testable import PaxCore

@MainActor struct RouteGroupsSaveTests {
  @Test("分组保存等待最终结果；冲突与断线不把草稿当作已保存")
  func finalResultAndDisconnect() async throws {
    let submitted = Mutex<[RouteGroupsEdit]>([])
    let routes = RoutesStore(
      preview: { _, _, _ in RoutesPreview(token: "", revision: 0, items: []) },
      cancel: { _ in }, cancelOperation: { _ in }, editor: { _ in RouteEdit() },
      command: { intent in
        guard case .saveGroups(let edit) = intent else { return "unused" }
        submitted.withLock { $0.append(edit) }
        return edit.requestID
      })
    let original = [RouteGroup(id: "g", name: "工作", memberIDs: [])]
    routes.consume(
      .ready(
        DaemonHandshake(
          daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1,
          capabilities: ["routes.groups"]),
        DaemonSnapshot(
          snapshotVersion: 1, status: .off, routes: [], currentRoute: nil,
          todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: [],
          catalogRevision: 9, routeGroups: original)))
    let draft = [RouteGroup(id: "g", name: "修改后", memberIDs: [])]
    let edit = RouteGroupsEdit(groups: draft, expectedRevision: 9, requestID: "save1")
    let task = Task { try await routes.saveGroups(edit) }
    for _ in 0..<100 {
      if routes.pendingID == "save1" { break }
      await Task.yield()
    }
    #expect(routes.pendingID == "save1")
    #expect(routes.groups == original)
    routes.consume(
      .events([
        .operation(
          OperationResult(
            operationID: "save1", ok: false,
            error: ProductError(code: "catalog_stale", messageParams: ["10"])))
      ]))
    do {
      try await task.value
      Issue.record("conflict accepted")
    } catch {}
    #expect(routes.groups == original)
    #expect(draft[0].name == "修改后")
    #expect(submitted.withLock { $0.first?.expectedRevision } == 9)
    let disconnected = Task {
      try await routes.saveGroups(
        RouteGroupsEdit(groups: draft, expectedRevision: 9, requestID: "save2"))
    }
    for _ in 0..<100 {
      if routes.pendingID == "save2" { break }
      await Task.yield()
    }
    routes.consume(.disconnected)
    do {
      try await disconnected.value
      Issue.record("disconnect accepted")
    } catch {}
    #expect(routes.groups == original)
  }
}

extension RouteGroupsSaveTests {
  @Test("删除来源后的草稿基准不跟随并发快照")
  func subscriptionDeleteKeepsTransactionRevision() async throws {
    let routes = RoutesStore(
      preview: { _, _, _ in RoutesPreview(token: "", revision: 0, items: []) }, cancel: { _ in },
      cancelOperation: { _ in }, editor: { _ in RouteEdit() }, command: { _ in "delete-source" })
    let source = SubscriptionSummary(
      id: "s", name: "Source", host: "example.test", revision: 1, routeCount: 1, hiddenCount: 0)
    routes.consume(
      .ready(
        DaemonHandshake(
          daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1,
          capabilities: ["subscriptions.delete"]),
        DaemonSnapshot(
          snapshotVersion: 1, status: .off, routes: [], currentRoute: nil, todayTraffic: .zero,
          settings: DaemonSettings(autoConnect: false), recentLogs: [], catalogRevision: 10,
          subscriptions: [source])))
    let task = Task { try await routes.deleteSubscription(source, expectedCatalogRevision: 10) }
    for _ in 0..<100 {
      if routes.pendingID == "delete-source" { break }
      await Task.yield()
    }
    #expect(routes.pendingID == "delete-source")
    routes.consume(
      .events([
        .snapshot(
          DaemonSnapshot(
            snapshotVersion: 3, status: .off, routes: [], currentRoute: nil, todayTraffic: .zero,
            settings: DaemonSettings(autoConnect: false), recentLogs: [], catalogRevision: 12,
            routeGroups: [RouteGroup(id: "new", name: "Other client", memberIDs: [])])),
        .operation(OperationResult(operationID: "delete-source", ok: true, error: nil)),
      ]))
    let committed = try await task.value
    #expect(committed == 11)
    #expect(routes.catalogRevision == 12)
    #expect(routes.groups[0].name == "Other client")
  }
}
