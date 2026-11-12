import Testing

@testable import PaxCore

@MainActor
struct RouteTLSPolicyTests {
  @Test("保存严格只改下次策略，当前跳过与多项降级不消失")
  func savedDoesNotChangeActive() async {
    let route = NodeProfile(
      id: "r", name: "测试", protocolName: "hysteria2", server: "example.com",
      port: 443, group: "默认", serverName: "example.com",
      tlsPolicy: RouteTLSPolicy(
        mode: .skipVerify,
        userOverride: true, needsConfirmation: false, identityToken: "identity", revision: 2))
    let active = ActiveTLS(
      routeID: "r", identityToken: "identity", mode: .skipVerify, verified: false, skipped: true)
    let store = ConnectionStore(tlsCommand: { _ in "save" })
    store.consume(
      .ready(
        DaemonHandshake(daemonVersion: "test", protocolVersion: 2, snapshotSeq: 1),
        DaemonSnapshot(
          snapshotVersion: 1, status: .degraded, routes: [route], currentRoute: route,
          todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: [],
          activeTLS: active,
          degradedReasons: [
            ProductError(code: "ipv6_unavailable"), ProductError(code: "tls_unverified"),
          ])))
    await store.perform(
      .updateRouteTLS(RouteTLSIntent(route: route, mode: .strict, confirmSkip: false)))
    store.apply(.operation(OperationResult(operationID: "save", ok: true, error: nil)))
    #expect(store.activeTLS?.skipped == true)
    #expect(store.degradedReasons.map(\.code) == ["ipv6_unavailable", "tls_unverified"])
    #expect(store.tlsSaveMessage == "已保存，关闭再开启以应用")
    #expect(store.pendingOperationID == nil)
  }
  @Test("策略操作早结果ACK不会悬挂，断线不能保存")
  func earlyResultAndDisconnected() async {
    let route = NodeProfile(
      id: "r", name: "测试", protocolName: "hysteria2", server: "example.com", port: 443, group: "默认",
      tlsPolicy: RouteTLSPolicy(
        mode: .strict, userOverride: false, needsConfirmation: false, identityToken: "id",
        revision: 1))
    let store = ConnectionStore(tlsCommand: { _ in "early" })
    #expect(!store.canEditTLS)
    store.consume(
      .ready(
        DaemonHandshake(daemonVersion: "test", protocolVersion: 2, snapshotSeq: 0),
        DaemonSnapshot(
          snapshotVersion: 0, status: .off, routes: [route], currentRoute: nil, todayTraffic: .zero,
          settings: DaemonSettings(autoConnect: false), recentLogs: [])))
    store.apply(.operation(OperationResult(operationID: "early", ok: true, error: nil)))
    await store.perform(
      .updateRouteTLS(RouteTLSIntent(route: route, mode: .strict, confirmSkip: false)))
    #expect(store.pendingOperationID == nil)
    #expect(store.tlsSaveMessage == "已保存，下次开启生效")
    store.markDisconnected()
    #expect(!store.canEditTLS)
  }
}
