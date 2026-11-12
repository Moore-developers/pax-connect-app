import Foundation
import Testing

@testable import PaxCore

@MainActor struct TLSFeedbackTests {
  @Test("证书保存反馈绑定线路，不被代理开关错误污染，断线清除归属")
  func feedbackOwnership() async {
    let store = ConnectionStore(proxyCommand: { _ in "proxy" }, tlsCommand: { _ in "tls" })
    let node = NodeProfile(
      id: "a", name: "A", protocolName: "trojan", server: "fixture.test", port: 443, group: "",
      tlsPolicy: RouteTLSPolicy(
        mode: .strict, userOverride: false, needsConfirmation: false, identityToken: "identity",
        revision: 1))
    store.consume(
      .ready(
        DaemonHandshake(daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1),
        DaemonSnapshot(
          snapshotVersion: 1, status: .off, routes: [node], currentRoute: nil, todayTraffic: .zero,
          settings: DaemonSettings(autoConnect: false), recentLogs: [])))
    await store.perform(
      .updateRouteTLS(RouteTLSIntent(route: node, mode: .strict, confirmSkip: false)))
    store.consume(
      .events([
        .operation(
          OperationResult(
            operationID: "tls", ok: false, error: ProductError(code: "tls_policy_stale")))
      ]))
    #expect(store.tlsSaveRouteID == "a")
    #expect(store.tlsSaveError?.contains("变化") == true)
    let detail = store.tlsSaveError
    await store.perform(.setProxyEnabled(true))
    store.consume(
      .events([
        .operation(
          OperationResult(
            operationID: "proxy", ok: false, error: ProductError(code: "core_required")))
      ]))
    #expect(store.tlsSaveRouteID == "a")
    #expect(store.tlsSaveError == detail)
    store.consume(.disconnected)
    #expect(store.tlsSaveRouteID == nil)
  }
}
