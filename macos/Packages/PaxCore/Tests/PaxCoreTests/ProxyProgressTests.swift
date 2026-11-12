import Testing

@testable import PaxCore

@MainActor @Suite("代理进度线路名称")
struct ProxyProgressTests {
  @Test("开启显示已选线路，切线显示后台目标而非旧线路")
  func targetName() {
    let routes = RoutesStore(
      preview: { _, _, _ in RoutesPreview(token: "", revision: 0, items: []) }, cancel: { _ in },
      cancelOperation: { _ in }, editor: { _ in RouteEdit() }, command: { _ in "unused" })
    let store = ConnectionStore(routesStore: routes)
    let old = NodeProfile(
      id: "a", name: "香港线路", protocolName: "trojan", server: "a.test", port: 443, group: "本地")
    let next = NodeProfile(
      id: "b", name: "日本线路", protocolName: "trojan", server: "b.test", port: 443, group: "本地")
    func apply(
      _ target: String, current: NodeProfile?, selected: String, state: ProtectionStatus = .starting
    ) {
      store.consume(
        .events([
          .snapshot(
            DaemonSnapshot(
              snapshotVersion: 1, status: state, routes: [old, next], currentRoute: current,
              todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: [],
              pendingOperationID: "op",
              selectedRouteID: selected, pendingTargetRouteID: target))
        ]))
    }
    apply("", current: nil, selected: old.id)
    #expect(store.proxyProgressText == "正在连接：香港线路…")
    apply(next.id, current: old, selected: old.id, state: .degraded)
    #expect(store.proxyProgressText == "正在切换到：日本线路…")
    apply("missing", current: old, selected: old.id, state: .degraded)
    #expect(store.proxyProgressText == "正在切换线路…")
    #expect(!store.proxyProgressText.contains("香港"))
  }
}

extension ProxyProgressTests {
  @Test("后台状态显示线路名，第三方接管只提示手动处理")
  func recoveryAndExternalOwner() async {
    let store = ConnectionStore(proxyCommand: { _ in "stop-external" })
    let route = NodeProfile(
      id: "a", name: "香港线路", protocolName: "trojan", server: "a.test", port: 443, group: "本地")
    store.consume(
      .ready(
        DaemonHandshake(daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1),
        DaemonSnapshot(
          snapshotVersion: 1, status: .degraded, routes: [route], currentRoute: route,
          todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: [],
          degradedReason: ProductError(code: "network_recovering"))))
    #expect(store.connectionTransitioning)
    #expect(store.proxyProgressText == "网络恢复中 · 香港线路")
    store.apply(
      .snapshot(
        DaemonSnapshot(
          snapshotVersion: 2, status: .off, routes: [route], currentRoute: nil, todayTraffic: .zero,
          settings: DaemonSettings(autoConnect: false), recentLogs: [],
          degradedReason: ProductError(code: "tun_taken_over"))))
    #expect(!store.proxyEnabled)
    #expect(store.externalTUNActive)
    #expect(store.feedback.contains { $0.id == "external-tun" && $0.action == .details })
    store.consume(.disconnected)
    #expect(!store.canRequestProxy(false))
  }
}
