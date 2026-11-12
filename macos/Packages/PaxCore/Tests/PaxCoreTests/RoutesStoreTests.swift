import Foundation
import Synchronization
import Testing

@testable import PaxCore

@MainActor struct RoutesStoreTests {
  private func makeStore(
    preview: @escaping @Sendable (String, Bool, String) async throws -> RoutesPreview = {
      _, _, _ in RoutesPreview(token: "", revision: 0, items: [])
    },
    cancelOperation: @escaping @Sendable (String) async throws -> Void = { _ in },
    command: @escaping @Sendable (RoutesStore.Intent) async throws -> String = { _ in "unused" }
  ) -> RoutesStore {
    RoutesStore(
      preview: preview, cancel: { _ in }, cancelOperation: cancelOperation,
      editor: { _ in RouteEdit() }, command: command)
  }
  @Test("目录与当前线路分离，浏览不发选路命令")
  func catalogState() {
    let store = makeStore()
    let route = NodeProfile(
      id: "r", name: "fixture", protocolName: "hysteria2", server: "example.com", port: 443,
      group: "本地")
    store.consume(
      .ready(
        DaemonHandshake(daemonVersion: "test", protocolVersion: 3, snapshotSeq: 1),
        DaemonSnapshot(
          snapshotVersion: 1, status: .off, routes: [route], currentRoute: nil, todayTraffic: .zero,
          settings: DaemonSettings(autoConnect: false), recentLogs: [], catalogRevision: 2,
          selectedRouteID: "")))
    store.browsedID = "r"
    #expect(store.browsedRoute?.id == "r")
    #expect(store.selectedRouteID.isEmpty)
    #expect(store.routes.count == 1)
    store.consume(.disconnected)
    #expect(!store.canWrite)
    #expect(store.routes.count == 1)
  }
}

extension RoutesStoreTests {
  @Test("pending清空快照先于失败结果仍保留失败反馈")
  func resultAfterClearedSnapshot() async {
    let store = makeStore(command: { _ in "op" })
    let snapshot = DaemonSnapshot(
      snapshotVersion: 1, status: .off, routes: [], currentRoute: nil, todayTraffic: .zero,
      settings: DaemonSettings(autoConnect: false), recentLogs: [])
    store.consume(
      .ready(DaemonHandshake(daemonVersion: "test", protocolVersion: 3, snapshotSeq: 1), snapshot))
    await store.perform(.save(RouteEdit()))
    store.consume(
      .events([
        .snapshot(snapshot),
        .operation(
          OperationResult(operationID: "op", ok: false, error: ProductError(code: "route_invalid"))),
      ]))
    #expect(store.pendingID == nil)
    #expect(store.message.contains("无效"))
  }

  private func subscriptionSnapshot() -> DaemonSnapshot {
    let summary = SubscriptionSummary(
      id: "sub1", name: "机场一", host: "sub.example.com", revision: 3, routeCount: 1,
      hiddenCount: 1)
    let local = NodeProfile(
      id: "local", name: "本地线路", protocolName: "hysteria2", server: "a.example.com", port: 443,
      group: "默认")
    let subscription = NodeProfile(
      id: "sub-node", name: "香港01", protocolName: "vless", server: "hk.example.com", port: 443,
      group: "默认", revision: 5, transport: "ws", security: "tls", sourceID: "sub1",
      origin: "subscription")
    let hidden = NodeProfile(
      id: "hidden-node", name: "日本01", protocolName: "vless", server: "jp.example.com", port: 443,
      group: "默认", revision: 2, transport: "ws", security: "tls", sourceID: "sub1",
      origin: "subscription", hidden: true)
    let removed = NodeProfile(
      id: "removed-node", name: "旧节点", protocolName: "vmess", server: "old.example.com",
      port: 443, group: "默认", revision: 4, transport: "ws", security: "tls", sourceID: "sub1",
      origin: "subscription", sourceRemoved: true)
    return DaemonSnapshot(
      snapshotVersion: 1, status: .off, routes: [local, subscription, hidden, removed],
      currentRoute: nil, todayTraffic: .zero, settings: DaemonSettings(autoConnect: false),
      recentLogs: [], catalogRevision: 9, selectedRouteID: "",
      subscriptions: [summary])
  }

  @Test("来源过滤与已隐藏入口互不混淆")
  func sourceFiltering() {
    let store = makeStore()
    store.consume(
      .ready(
        DaemonHandshake(daemonVersion: "test", protocolVersion: 3, snapshotSeq: 1),
        subscriptionSnapshot()))
    #expect(store.visibleRoutes.map(\.id) == ["local", "sub-node", "removed-node"])
    store.sourceFilter = .local
    #expect(store.visibleRoutes.map(\.id) == ["local"])
    store.sourceFilter = .source("sub1")
    #expect(store.visibleRoutes.map(\.id) == ["sub-node", "removed-node"])
    store.sourceFilter = .hidden
    #expect(store.visibleRoutes.map(\.id) == ["hidden-node"])
    #expect(store.hasHidden)
    #expect(store.sourceName("sub1") == "机场一")
    store.filter = "香港"
    store.sourceFilter = .all
    #expect(store.visibleRoutes.map(\.id) == ["sub-node"])
  }

  @Test("手刷只占用自己的来源，结果经操作事件回到来源消息")
  func refreshLifecycle() async {
    let sequence = Mutex(0)
    let cancelled = Mutex<[String]>([])
    let store = makeStore(
      cancelOperation: { id in cancelled.withLock { $0.append(id) } },
      command: { intent in
        guard case .refresh(let source) = intent else { return "unused" }
        let id = sequence.withLock { value in
          value += 1
          return value
        }
        return "refresh-\(source.id)-\(id)"
      })
    store.consume(
      .ready(
        DaemonHandshake(daemonVersion: "test", protocolVersion: 3, snapshotSeq: 1),
        subscriptionSnapshot()))
    let summary = SubscriptionSummary(
      id: "sub1", name: "机场一", host: "sub.example.com", revision: 3, routeCount: 1,
      hiddenCount: 1)
    await store.perform(.refresh(summary))
    #expect(store.isRefreshing("sub1"))
    #expect(store.canWrite)
    store.consume(
      .events([
        .operation(OperationResult(operationID: "refresh-sub1-1", ok: true, error: nil))
      ]))
    #expect(!store.isRefreshing("sub1"))
    #expect(store.message == "订阅已刷新")

    // 失败结果给出产品文案；取消走明确的取消命令。
    await store.perform(.refresh(summary))
    #expect(store.isRefreshing("sub1"))
    await store.cancelRefresh("sub1")
    #expect(cancelled.withLock { $0 } == ["refresh-sub1-2"])
    store.consume(
      .events([
        .operation(
          OperationResult(
            operationID: "refresh-sub1-2", ok: false,
            error: ProductError(code: "fetch_failed")))
      ]))
    #expect(!store.isRefreshing("sub1"))
    #expect(store.message == "订阅下载失败，请检查网络与来源")
  }

  @Test("连通性测试状态随快照推进，过期由 revision 对比判定，断线清空进行中")
  func testStateLifecycle() async throws {
    let store = makeStore(command: { intent in
      guard case .test = intent else { return "unused" }
      return "test-op"
    })
    var summary = subscriptionSnapshot()
    store.consume(
      .ready(
        DaemonHandshake(daemonVersion: "test", protocolVersion: 3, snapshotSeq: 1), summary))
    let route = NodeProfile(
      id: "local", name: "本地线路", protocolName: "hysteria2", server: "a.example.com", port: 443,
      group: "默认", revision: 7)
    #expect(store.testState(route) == .untested)

    store.consume(
      .events([.snapshot(subscriptionSnapshot().with(testingRouteIDs: ["local"]))]))
    #expect(store.isTesting("local"))
    #expect(store.testState(route) == .running)

    let finished = RouteTestSummary(
      at: Date(timeIntervalSince1970: 100), success: true, durationMS: 240, testedRevision: 7)
    var withSummary = subscriptionSnapshot()
    withSummary = withSummary.with(testingRouteIDs: []).withRoutes([
      NodeProfile(
        id: "local", name: "本地线路", protocolName: "hysteria2", server: "a.example.com",
        port: 443, group: "默认", revision: 7, testSummary: finished)
    ])
    store.consume(.events([.snapshot(withSummary)]))
    #expect(!store.isTesting("local"))
    // 界面用的是快照里的线路；快照携带测量结果与 revision。
    let tested = try #require(store.routes.first { $0.id == "local" })
    #expect(store.testState(tested) == .finished(finished))

    // 配置 revision 变化后旧测量标过期，不伪造新结果。
    let edited = NodeProfile(
      id: "local", name: "本地线路", protocolName: "hysteria2", server: "a.example.com",
      port: 443, group: "默认", revision: 8, testSummary: finished)
    #expect(store.testState(edited) == .stale(finished))

    await store.perform(.test(route))
    store.consume(
      .events([.operation(OperationResult(operationID: "test-op", ok: true, error: nil))]))
    #expect(store.message == "测试完成")
    store.consume(.disconnected)
    #expect(!store.isTesting("local"))
  }

  @Test("部分刷新与来源移除都有可读反馈，断线清空刷新状态")
  func refreshReporting() async {
    let sequence = Mutex(0)
    let store = makeStore(command: { intent in
      guard case .refresh = intent else { return "unused" }
      let id = sequence.withLock { value in
        value += 1
        return value
      }
      return "refresh-op-\(id)"
    })
    store.consume(
      .ready(
        DaemonHandshake(daemonVersion: "test", protocolVersion: 3, snapshotSeq: 1),
        subscriptionSnapshot()))
    let partial = SubscriptionSummary(
      id: "sub1", name: "机场一", host: "sub.example.com", revision: 4, routeCount: 1,
      hiddenCount: 1, lastRefresh: RefreshReport(partial: true, added: 1, kept: 2))
    let removal = SubscriptionSummary(
      id: "sub1", name: "机场一", host: "sub.example.com", revision: 5, routeCount: 1,
      hiddenCount: 1, lastRefresh: RefreshReport(removed: 3))
    let summary = SubscriptionSummary(
      id: "sub1", name: "机场一", host: "sub.example.com", revision: 3, routeCount: 1,
      hiddenCount: 1)
    await store.perform(.refresh(summary))
    store.consume(
      .events([
        .snapshot(subscriptionSnapshot().with(subscriptions: [partial])),
        .operation(OperationResult(operationID: "refresh-op-1", ok: true, error: nil)),
      ]))
    #expect(store.message.contains("部分刷新失败"))
    await store.perform(.refresh(summary))
    store.consume(
      .events([
        .snapshot(subscriptionSnapshot().with(subscriptions: [removal])),
        .operation(OperationResult(operationID: "refresh-op-2", ok: true, error: nil)),
      ]))
    #expect(store.message.contains("来源移除"))
    store.consume(.disconnected)
    #expect(!store.isRefreshing("sub1"))
  }
}

extension DaemonSnapshot {
  /// 测试替身：只替换订阅摘要，其余字段保持不变。
  func with(subscriptions: [SubscriptionSummary]) -> DaemonSnapshot {
    DaemonSnapshot(
      snapshotVersion: snapshotVersion, status: status, routes: routes, currentRoute: currentRoute,
      todayTraffic: todayTraffic, settings: settings, recentLogs: recentLogs,
      pendingOperationID: pendingOperationID, degradedReason: degradedReason, activeTLS: activeTLS,
      degradedReasons: degradedReasons, catalogRevision: catalogRevision,
      selectedRouteID: selectedRouteID, pendingTargetRouteID: pendingTargetRouteID,
      subscriptions: subscriptions, testingRouteIDs: testingRouteIDs)
  }
  /// 测试替身：只替换进行中测试与线路，其余字段保持不变。
  func withRoutes(_ routes: [NodeProfile]) -> DaemonSnapshot {
    DaemonSnapshot(
      snapshotVersion: snapshotVersion, status: status, routes: routes, currentRoute: currentRoute,
      todayTraffic: todayTraffic, settings: settings, recentLogs: recentLogs,
      pendingOperationID: pendingOperationID, degradedReason: degradedReason, activeTLS: activeTLS,
      degradedReasons: degradedReasons, catalogRevision: catalogRevision,
      selectedRouteID: selectedRouteID, pendingTargetRouteID: pendingTargetRouteID,
      subscriptions: subscriptions, testingRouteIDs: testingRouteIDs)
  }
  /// 测试替身：只替换进行中测试，其余字段保持不变。
  func with(testingRouteIDs: [String]) -> DaemonSnapshot {
    DaemonSnapshot(
      snapshotVersion: snapshotVersion, status: status, routes: routes, currentRoute: currentRoute,
      todayTraffic: todayTraffic, settings: settings, recentLogs: recentLogs,
      pendingOperationID: pendingOperationID, degradedReason: degradedReason, activeTLS: activeTLS,
      degradedReasons: degradedReasons, catalogRevision: catalogRevision,
      selectedRouteID: selectedRouteID, pendingTargetRouteID: pendingTargetRouteID,
      subscriptions: subscriptions, testingRouteIDs: testingRouteIDs)
  }
}

extension RoutesStoreTests {
  @Test("旧会话迟到ACK不能污染重连后的选择和忙碌状态")
  func selectionAckAfterReconnect() async {
    let gate = CommandGate()
    let store = makeStore(command: { _ in
      await gate.wait()
      return "old-operation"
    })
    let handshake = DaemonHandshake(daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1)
    let snapshot = subscriptionSnapshot()
    store.consume(.ready(handshake, snapshot))
    let task = Task { await store.perform(.select(snapshot.routes[0])) }
    while !(await gate.entered) { await Task.yield() }
    #expect(store.busy)
    #expect(store.selectedRouteID.isEmpty)
    store.consume(.disconnected)
    store.consume(.ready(handshake, snapshot))
    await gate.release()
    await task.value
    #expect(store.canWrite)
    #expect(store.pendingID == nil)
    #expect(store.message.isEmpty)
    #expect(store.selectedRouteID.isEmpty)
  }
}

private actor CommandGate {
  private var continuation: CheckedContinuation<Void, Never>?
  private(set) var entered = false
  func wait() async {
    await withCheckedContinuation { continuation in
      self.continuation = continuation
      entered = true
    }
  }
  func release() {
    continuation?.resume()
    continuation = nil
  }
}

extension RoutesStoreTests {
  @Test("保存等待最终结果，失败抛出且不污染目录")
  func saveWaitsForResult() async throws {
    let store = makeStore(command: { _ in "save-result" })
    store.consume(
      .ready(
        DaemonHandshake(daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1),
        subscriptionSnapshot()))
    var edit = RouteEdit()
    edit.routeID = "local"
    edit.documentJSON = "private draft"
    let task = Task { try await store.saveDocument(edit) }
    for _ in 0..<100 where store.pendingID == nil { await Task.yield() }
    #expect(store.pendingID == "save-result")
    store.consume(
      .events([
        .operation(
          OperationResult(
            operationID: "save-result", ok: false, error: ProductError(code: "route_stale")))
      ]))
    do {
      try await task.value
      Issue.record("失败保存不能成功返回")
    } catch {}
    #expect(store.routes[0].name == "本地线路")
    #expect(store.canWrite)
  }
}

extension RoutesStoreTests {
  @Test("重连后拒绝旧会话迟到的私有编辑文档")
  func editorAfterReconnect() async {
    let gate = CommandGate()
    let store = RoutesStore(
      preview: { _, _, _ in RoutesPreview(token: "", revision: 0, items: []) }, cancel: { _ in },
      cancelOperation: { _ in },
      editor: { _ in
        await gate.wait()
        return RouteEdit()
      }, command: { _ in "unused" })
    let handshake = DaemonHandshake(daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1)
    store.consume(.ready(handshake, subscriptionSnapshot()))
    let task = Task { try await store.loadEditor("local") }
    while !(await gate.entered) { await Task.yield() }
    store.consume(.disconnected)
    store.consume(.ready(handshake, subscriptionSnapshot()))
    await gate.release()
    do {
      _ = try await task.value
      Issue.record("旧会话私有文档不应进入新草稿")
    } catch {}
  }
}

extension RoutesStoreTests {
  @Test("导入结果先于ACK仍成功对账，空选择不发命令")
  func importWaitsForFinalResult() async throws {
    let gate = CommandGate()
    let calls = Mutex(0)
    let store = makeStore(command: { _ in
      calls.withLock { $0 += 1 }
      await gate.wait()
      return "import-op"
    })
    store.consume(
      .ready(
        DaemonHandshake(daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1),
        subscriptionSnapshot()))
    let preview = RoutesPreview(token: "preview", revision: 9, items: [])
    do {
      try await store.confirmImport(preview, items: [])
      Issue.record("空选择不应成功")
    } catch {}
    #expect(calls.withLock { $0 } == 0)
    let task = Task { try await store.confirmImport(preview, items: ["selected"]) }
    while !(await gate.entered) { await Task.yield() }
    store.consume(
      .events([.operation(OperationResult(operationID: "import-op", ok: true, error: nil))]))
    await gate.release()
    try await task.value
    #expect(store.canWrite)
    #expect(store.routes.count == 4)
  }
}

extension RoutesStoreTests {
  @Test("批量延迟与下载共用后台受理路径并冻结输入顺序")
  func visibleBatchUsesBackgroundCommands() async {
    let calls = Mutex<[RoutesStore.Intent]>([])
    let sequence = Mutex(0)
    let routes = makeStore(command: { intent in
      calls.withLock { $0.append(intent) }
      let id = sequence.withLock {
        $0 += 1
        return $0
      }
      return "batch-\(id)"
    })
    let handshake = DaemonHandshake(
      daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1, capabilities: ["routes.test.batch"]
    )
    routes.consume(
      .ready(
        handshake,
        DaemonSnapshot(
          snapshotVersion: 1, status: .off, routes: [], currentRoute: nil, todayTraffic: .zero,
          settings: DaemonSettings(autoConnect: false), recentLogs: [])))
    let nodes = (1...3).map {
      NodeProfile(
        id: "r\($0)", name: "Fixture \($0)", protocolName: "trojan", server: "example.test",
        port: 443, group: "")
    }
    await routes.performBatch(nodes, download: true)
    let intents = calls.withLock { $0 }
    #expect(intents.count == 1)
    if case .testBatch(let frozen, let download) = intents.first {
      #expect(frozen.map(\.id) == nodes.map(\.id))
      #expect(download)
    } else {
      Issue.record("整组必须一次受理")
    }
    #expect(routes.canWrite)
    #expect(routes.message.contains("后台"))
  }

  @Test("下载后台受理后不占目录写入，重连由快照恢复并更新成功结果")
  func backgroundDownloadResumesFromSnapshot() async {
    let cancellations = Mutex<[String]>([])
    let routes = makeStore(
      cancelOperation: { id in cancellations.withLock { $0.append(id) } },
      command: { _ in "download-task" })
    let node = NodeProfile(
      id: "r", name: "Fixture", protocolName: "trojan", server: "example.test", port: 443,
      group: "", configRevision: 1)
    let handshake = DaemonHandshake(daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1)
    func snapshot(_ running: Bool, _ summary: DownloadTestSummary? = nil) -> DaemonSnapshot {
      let route = NodeProfile(
        id: node.id, name: node.name, protocolName: node.protocolName, server: node.server,
        port: node.port, group: "", configRevision: 1, downloadSummary: summary)
      return DaemonSnapshot(
        snapshotVersion: running ? 2 : 3, status: .off, routes: [route], currentRoute: nil,
        todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: [],
        downloadingRouteIDs: running ? ["r"] : [],
        downloadOperationID: running ? "download-task" : "")
    }
    routes.consume(.ready(handshake, snapshot(false)))
    await routes.perform(.download(node))
    #expect(routes.canWrite)
    #expect(routes.message.contains("后台"))
    routes.consume(.events([.snapshot(snapshot(true))]))
    routes.consume(.disconnected)
    #expect(cancellations.withLock { $0 }.isEmpty)
    routes.consume(.ready(handshake, snapshot(true)))
    #expect(routes.downloadOperationID == "download-task")
    let success = DownloadTestSummary(
      at: Date(), success: true, bytesPerSecond: 1_000_000, testedConfigRevision: 1,
      bytes: 8 * 1024 * 1024)
    routes.consume(
      .events([
        .snapshot(snapshot(false, success)),
        .operation(OperationResult(operationID: "download-task", ok: true, error: nil)),
      ]))
    #expect(routes.routes[0].downloadSummary?.bytesPerSecond == 1_000_000)
    #expect(routes.downloadOperationID.isEmpty)
  }
  @Test("测速终态早于ACK也会显示完成，而不永久等待")
  func earlyDownloadCompletionIsCorrelated() async {
    let routes = makeStore(command: { _ in "early-speed" })
    let node = NodeProfile(
      id: "r", name: "Fixture", protocolName: "trojan", server: "example.test", port: 443, group: ""
    )
    routes.consume(
      .ready(
        DaemonHandshake(daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1),
        DaemonSnapshot(
          snapshotVersion: 1, status: .off, routes: [node], currentRoute: nil, todayTraffic: .zero,
          settings: DaemonSettings(autoConnect: false), recentLogs: [])))
    routes.consume(
      .events([.operation(OperationResult(operationID: "early-speed", ok: true, error: nil))]))
    await routes.perform(.download(node))
    #expect(routes.message == "下载测速完成")
  }
}

extension RoutesStoreTests {
  @Test("41条下载一次交给daemon续批，不由App逐批续命")
  func downloadQueueTransfersEntireScope() async {
    let calls = Mutex<[RoutesStore.Intent]>([])
    let routes = makeStore(command: { intent in
      calls.withLock { $0.append(intent) }
      return "queue"
    })
    routes.consume(
      .ready(
        DaemonHandshake(
          daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1,
          capabilities: ["routes.test.batch", "routes.download.queue"]),
        DaemonSnapshot(
          snapshotVersion: 1, status: .off, routes: [], currentRoute: nil,
          todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: [])))
    let nodes = (1...41).map {
      NodeProfile(
        id: "r\($0)", name: "Fixture", protocolName: "trojan", server: "fixture.test", port: 443,
        group: "")
    }
    await routes.performBatch(nodes, download: true)
    #expect(calls.withLock { $0.count } == 1)
    if case .testBatch(let targets, let download) = calls.withLock({ $0.first }) {
      #expect(targets.count == 41 && download)
    } else {
      Issue.record("整组应由daemon持有")
    }
  }
}

extension RoutesStoreTests {
  @Test("刷新终态早于ACK仍结束，不残留刷新中")
  func refreshEarlyTerminal() async {
    let routes = makeStore(command: { _ in "refresh-early" })
    routes.consume(
      .ready(
        DaemonHandshake(daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1),
        DaemonSnapshot(
          snapshotVersion: 1, status: .off, routes: [], currentRoute: nil, todayTraffic: .zero,
          settings: DaemonSettings(autoConnect: false), recentLogs: [])))
    routes.consume(
      .events([
        .operation(
          OperationResult(
            operationID: "refresh-early", ok: false, error: ProductError(code: "fetch_forbidden")))
      ]))
    await routes.perform(
      .refresh(
        SubscriptionSummary(
          id: "source", name: "Fixture", host: "fixture.test", revision: 1, routeCount: 0,
          hiddenCount: 0)))
    #expect(!routes.isRefreshing("source"))
    #expect(routes.message.contains("403"))
  }
}

extension RoutesStoreTests {
  @Test("订阅刷新快照在App重连后恢复来源级进度")
  func refreshRestoresFromSnapshot() {
    let routes = makeStore()
    let source = SubscriptionSummary(
      id: "source", name: "Fixture", host: "fixture.test", revision: 1, routeCount: 0,
      hiddenCount: 0, refreshOperationID: "daemon-refresh")
    routes.consume(
      .ready(
        DaemonHandshake(
          daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1,
          capabilities: ["subscriptions.refresh"]),
        DaemonSnapshot(
          snapshotVersion: 1, status: .off, routes: [], currentRoute: nil, todayTraffic: .zero,
          settings: DaemonSettings(autoConnect: false), recentLogs: [], subscriptions: [source])))
    #expect(routes.isRefreshing("source"))
    routes.consume(.disconnected)
    #expect(!routes.isRefreshing("source"))
    routes.consume(
      .ready(
        DaemonHandshake(
          daemonVersion: "test", protocolVersion: 4, snapshotSeq: 2,
          capabilities: ["subscriptions.refresh"]),
        DaemonSnapshot(
          snapshotVersion: 2, status: .off, routes: [], currentRoute: nil, todayTraffic: .zero,
          settings: DaemonSettings(autoConnect: false), recentLogs: [], subscriptions: [source])))
    #expect(routes.refreshing["source"] == "daemon-refresh")
  }
}
