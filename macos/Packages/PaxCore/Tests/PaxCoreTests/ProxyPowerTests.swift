import Synchronization
import Testing

@testable import PaxCore

@MainActor struct ProxyPowerTests {
  @Test("工作台仅在能力与选路就绪时开启，启动中允许停止，断线不发送")
  func powerAdmission() async {
    let sent = Mutex<[Bool]>([])
    let routes = RoutesStore(
      preview: { _, _, _ in RoutesPreview(token: "", revision: 0, items: []) },
      cancel: { _ in }, cancelOperation: { _ in }, editor: { _ in RouteEdit() },
      command: { _ in "unused" })
    let store = ConnectionStore(
      proxyCommand: { enabled in
        sent.withLock { $0.append(enabled) }
        return "power"
      },
      routesStore: routes)
    let route = NodeProfile(
      id: "r", name: "route", protocolName: "trojan", server: "example.test", port: 443,
      group: "local", security: "tls")
    let snapshot = DaemonSnapshot(
      snapshotVersion: 1, status: .off, routes: [route], currentRoute: nil,
      todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: [],
      selectedRouteID: "r")
    store.consume(
      .ready(
        DaemonHandshake(daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1), snapshot))
    #expect(!store.canToggleProxy)
    await store.setProxyFromWorkbench(true)
    #expect(sent.withLock { $0 }.isEmpty)
    store.consume(
      .ready(
        DaemonHandshake(
          daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1,
          capabilities: ["proxy.power"]), snapshot))
    #expect(store.canToggleProxy)
    await store.setProxyFromWorkbench(true)
    #expect(store.status == .off)
    #expect(store.canToggleProxy)
    #expect(store.proxyStarting)
    store.apply(
      .snapshot(
        DaemonSnapshot(
          snapshotVersion: 2, status: .starting, routes: [route], currentRoute: nil,
          todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: [],
          selectedRouteID: "r")))
    #expect(store.canToggleProxy)
    await store.setProxyFromWorkbench(false)
    #expect(sent.withLock { $0 } == [true, false])
    store.consume(.disconnected)
    #expect(!store.canToggleProxy)
    await store.setProxyFromWorkbench(false)
    #expect(sent.withLock { $0 } == [true, false])
  }
  @Test("来源跳过建议须确认，取消不发送开启命令")
  func sourceSuggestionNeedsConsent() async {
    let sent = Mutex<[Bool]>([])
    let routes = RoutesStore(
      preview: { _, _, _ in RoutesPreview(token: "", revision: 0, items: []) },
      cancel: { _ in }, cancelOperation: { _ in }, editor: { _ in RouteEdit() },
      command: { _ in "unused" })
    let store = ConnectionStore(
      proxyCommand: { enabled in
        sent.withLock { $0.append(enabled) }
        return "power"
      },
      routesStore: routes)
    let route = NodeProfile(
      id: "r", name: "route", protocolName: "trojan", server: "example.test", port: 443,
      group: "local",
      tlsPolicy: RouteTLSPolicy(
        mode: .skipVerify, userOverride: false, needsConfirmation: true,
        identityToken: "identity", revision: 1), security: "tls")
    store.consume(
      .ready(
        DaemonHandshake(
          daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1,
          capabilities: ["proxy.power"]),
        DaemonSnapshot(
          snapshotVersion: 1, status: .off, routes: [route], currentRoute: nil,
          todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: [],
          selectedRouteID: "r")))
    #expect(store.proxyConfirmationRoute?.id == "r")
    await store.setProxyFromWorkbench(true)
    #expect(sent.withLock { $0 }.isEmpty)
    #expect(!store.proxyEnabled)
  }

}

extension ProxyPowerTests {
  @Test("开启ACK迟到时仍可停止，旧ACK不覆盖关闭请求")
  func stopBeforeEnableAck() async {
    let gate = ProxyAckGate()
    let calls = Mutex<[Bool]>([])
    let routes = RoutesStore(
      preview: { _, _, _ in RoutesPreview(token: "", revision: 0, items: []) },
      cancel: { _ in }, cancelOperation: { _ in }, editor: { _ in RouteEdit() },
      command: { _ in "unused" })
    let store = ConnectionStore(
      proxyCommand: { enabled in
        calls.withLock { $0.append(enabled) }
        if enabled { await gate.wait() }
        return enabled ? "enable-old" : "stop-new"
      }, routesStore: routes)
    let node = NodeProfile(
      id: "r", name: "fixture", protocolName: "trojan", server: "example.test", port: 443, group: ""
    )
    store.consume(
      .ready(
        DaemonHandshake(
          daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1, capabilities: ["proxy.power"]),
        DaemonSnapshot(
          snapshotVersion: 1, status: .off, routes: [node], currentRoute: nil, todayTraffic: .zero,
          settings: DaemonSettings(autoConnect: false), recentLogs: [], selectedRouteID: "r")))
    let enable = Task { await store.setProxyFromWorkbench(true) }
    while !(await gate.entered) { await Task.yield() }
    #expect(store.proxyStarting)
    #expect(store.canToggleProxy)
    await store.setProxyFromWorkbench(false)
    #expect(store.pendingOperationID == "stop-new")
    await gate.release()
    await enable.value
    #expect(calls.withLock { $0 } == [true, false])
    #expect(store.pendingOperationID == "stop-new")
    #expect(!store.proxyStarting)
  }

  @Test("未授权代理意图只请求系统引导，取消不启动")
  func authorizationAdmission() async {
    var requests = 0
    let store = ConnectionStore(proxyCommand: { _ in
      Issue.record("授权前不能启动")
      return "bad"
    })
    store.needsServiceAuthorization = true
    store.requestAuthorization = { requests += 1 }
    #expect(store.canToggleProxy)
    await store.setProxyFromWorkbench(true)
    #expect(requests == 1)
    #expect(!store.proxyEnabled)
    await store.setProxyFromWorkbench(false)
    #expect(requests == 1)
  }
}

private actor ProxyAckGate {
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
