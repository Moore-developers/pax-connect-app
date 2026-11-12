import Foundation
import Testing

@testable import PaxCore

@MainActor
@Suite("ConnectionStore 状态落点")
struct ConnectionStoreTests {
  private func statusEvent(_ status: ProtectionStatus, reason: ProductError? = nil) -> DaemonEvent {
    .snapshot(
      DaemonSnapshot(
        snapshotVersion: 1, status: status, routes: [], currentRoute: nil, todayTraffic: .zero,
        settings: DaemonSettings(autoConnect: false), recentLogs: [], degradedReason: reason))
  }

  @Test("状态事件更新保护状态，DEGRADED 带原因、回到 PROTECTED 时原因清空")
  func statusTransitions() {
    let store = ConnectionStore()
    store.apply(statusEvent(.degraded, reason: ProductError(code: "tun_route_stolen")))
    #expect(store.status == .degraded)
    #expect(store.degradedReason?.code == "tun_route_stolen")

    store.apply(statusEvent(.protected))
    #expect(store.status == .protected)
    #expect(store.degradedReason == nil)
  }

  @Test("快照里的最近事件按容量截断，最旧的先丢")
  func logRingBuffer() {
    let store = ConnectionStore(logCapacity: 3)
    let logs = (1...5).map {
      ProductLog(
        at: Date(timeIntervalSince1970: 0), level: .info, message: "e\($0)", operationID: "",
        snapshotVersion: 0)
    }
    store.apply(
      .snapshot(
        DaemonSnapshot(
          snapshotVersion: 1, status: .off, routes: [], currentRoute: nil, todayTraffic: .zero,
          settings: DaemonSettings(autoConnect: false), recentLogs: logs)))
    #expect(store.recentLogs.map(\.message) == ["e3", "e4", "e5"])
  }

  @Test("断线保留最后快照，只改连接标记")
  func disconnectKeepsState() {
    let store = ConnectionStore()
    store.markHandshaked(
      DaemonHandshake(daemonVersion: "1", protocolVersion: 1, snapshotSeq: 1),
      snapshot: DaemonSnapshot(
        snapshotVersion: 1,
        status: .protected,
        routes: [],
        currentRoute: NodeProfile(
          id: "r1", name: "线路", protocolName: "vless", server: "s", port: 443, group: "默认"),
        todayTraffic: .zero,
        settings: DaemonSettings(autoConnect: false),
        recentLogs: []
      )
    )
    #expect(store.connection == .connected)
    #expect(store.daemonVersion == "1")

    store.markDisconnected()
    #expect(store.connection == .disconnected)
    #expect(store.status == .protected)
    #expect(store.currentRoute?.id == "r1")
  }

  @Test("重试和错误保留快照，重新同步后清除客户端错误")
  func retryState() {
    let store = ConnectionStore()
    store.apply(statusEvent(.protected))
    store.consume(.retrying(.unavailable, delay: .milliseconds(500)))
    #expect(store.connection == .retrying)
    #expect(store.status == .protected)
    #expect(store.clientError == .unavailable)
    store.consume(.connecting)
    #expect(store.connection == .connecting)
    store.consume(.failed(.permissionDenied))
    #expect(store.connection == .failed)
    #expect(store.status == .protected)
  }

  @Test("重连快照保留未完成操作及降级原因；更新期间禁用开关")
  func snapshotOperationAndReason() {
    let store = ConnectionStore(proxyCommand: { _ in "unused" })
    store.consume(
      .ready(
        DaemonHandshake(daemonVersion: "1", protocolVersion: 1, snapshotSeq: 10),
        DaemonSnapshot(
          snapshotVersion: 10, status: .degraded, routes: [], currentRoute: nil,
          todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: [],
          pendingOperationID: "pending", degradedReason: ProductError(code: "ipv6_unavailable"))))
    #expect(store.pendingOperationID == "pending")
    #expect(store.degradedReason?.code == "ipv6_unavailable")
    store.setServiceUpdating(true)
    #expect(!store.canOperate)
    store.setServiceUpdating(false)
    store.markDisconnected()
    #expect(!store.canOperate)
    #expect(store.status == .degraded)
  }

  @Test("操作结果早于ACK不留下永久pending")
  func earlyResult() async {
    let store = ConnectionStore(proxyCommand: { _ in "done-early" })
    store.consume(
      .ready(
        DaemonHandshake(daemonVersion: "1", protocolVersion: 1, snapshotSeq: 0),
        DaemonSnapshot(
          snapshotVersion: 0, status: .off, routes: [], currentRoute: nil,
          todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: [])))
    store.apply(.operation(OperationResult(operationID: "done-early", ok: true, error: nil)))
    await store.perform(.setProxyEnabled(true))
    #expect(store.pendingOperationID == nil)
    #expect(store.status == .off)
  }

  @Test("操作结果按 operationID 清空 pending 状态")
  func pendingOperationClearing() {
    let store = ConnectionStore()
    store.apply(.operation(OperationResult(operationID: "op-other", ok: true, error: nil)))
    #expect(store.pendingOperationID == nil)
  }
}

extension ConnectionStoreTests {
  @Test("订阅自动探测失败不污染代理状态，自己的失败仍保留")
  func routeProbeFailureDoesNotPolluteProxyError() async {
    let store = ConnectionStore(proxyCommand: { _ in "my-proxy" })
    store.consume(
      .ready(
        DaemonHandshake(daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1),
        DaemonSnapshot(
          snapshotVersion: 1, status: .off, routes: [], currentRoute: nil, todayTraffic: .zero,
          settings: DaemonSettings(autoConnect: false), recentLogs: [])))
    store.apply(
      .operation(
        OperationResult(
          operationID: "auto-probe", ok: false, error: ProductError(code: "proxy_unreachable"))))
    #expect(store.operationError == nil)
    #expect(!store.proxyMessage.contains("检查线路"))
    await store.perform(.setProxyEnabled(true))
    store.apply(
      .operation(
        OperationResult(
          operationID: "my-proxy", ok: false, error: ProductError(code: "proxy_unreachable"))))
    #expect(store.operationError?.code == "proxy_unreachable")
    store.apply(.operation(OperationResult(operationID: "another-probe", ok: true, error: nil)))
    #expect(store.operationError?.code == "proxy_unreachable")
  }
  @Test("ACK前自己的失败与无关成功不混淆")
  func ownEarlyFailureRemainsCorrelated() async {
    let store = ConnectionStore(proxyCommand: { _ in "early-own" })
    store.consume(
      .ready(
        DaemonHandshake(daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1),
        DaemonSnapshot(
          snapshotVersion: 1, status: .off, routes: [], currentRoute: nil, todayTraffic: .zero,
          settings: DaemonSettings(autoConnect: false), recentLogs: [])))
    store.apply(
      .operation(
        OperationResult(
          operationID: "early-own", ok: false, error: ProductError(code: "tun_start_failed"))))
    store.apply(.operation(OperationResult(operationID: "auto-success", ok: true, error: nil)))
    await store.perform(.setProxyEnabled(true))
    #expect(store.operationError?.code == "tun_start_failed")
    #expect(store.pendingOperationID == nil)
  }
}
