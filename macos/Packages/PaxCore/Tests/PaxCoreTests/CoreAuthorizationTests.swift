import Synchronization
import Testing

@testable import PaxCore

private final class CoreCalls: Sendable { let sent = Mutex<[Bool]>([]) }

@MainActor
struct CoreAuthorizationTests {
  @Test func lateEnableFailureAfterOffDoesNotPrompt() async {
    let calls = CoreCalls()
    let store = makeStore(calls)
    var prompts = 0
    store.requestAuthorization = { prompts += 1 }
    await store.setProxyFromWorkbench(true)
    await store.setProxyFromWorkbench(false)
    store.apply(
      .operation(
        OperationResult(
          operationID: "on", ok: false,
          error: ProductError(code: "core_required"))))
    #expect(prompts == 0)
    #expect(!store.needsServiceAuthorization)
    #expect(calls.sent.withLock { $0 } == [true, false])
  }
  private func makeStore(_ calls: CoreCalls) -> ConnectionStore {
    let routes = RoutesStore(
      preview: { _, _, _ in RoutesPreview(token: "", revision: 0, items: []) },
      cancel: { _ in }, cancelOperation: { _ in }, editor: { _ in RouteEdit() },
      command: { _ in "unused" })
    let store = ConnectionStore(
      proxyCommand: { enabled in
        let count = calls.sent.withLock {
          $0.append(enabled)
          return $0.count
        }
        return enabled ? (count == 1 ? "on" : "on\(count)") : "off"
      }, routesStore: routes)
    store.consume(
      .ready(
        DaemonHandshake(
          daemonVersion: "test", protocolVersion: 4, snapshotSeq: 1,
          capabilities: ["proxy.power", "proxy.core", "paxd.user"]),
        DaemonSnapshot(
          snapshotVersion: 1, status: .off,
          routes: [
            NodeProfile(
              id: "r", name: "route", protocolName: "trojan", server: "example.test", port: 443,
              group: "local", security: "tls")
          ],
          currentRoute: nil, todayTraffic: .zero, settings: DaemonSettings(autoConnect: false),
          recentLogs: [], selectedRouteID: "r")))
    return store
  }
  @Test func missingCorePromptsOnceAndResumesOnlyAfterAuthorization() async {
    let calls = CoreCalls()
    let store = makeStore(calls)
    var prompted = 0
    store.requestAuthorization = { prompted += 1 }
    await store.setProxyFromWorkbench(true)
    let failure = OperationResult(
      operationID: "on", ok: false, error: ProductError(code: "core_required"))
    store.apply(.operation(failure))
    store.apply(.operation(failure))
    #expect(prompted == 1)
    #expect(store.needsServiceAuthorization)
    #expect(store.status == .off)
    let token = store.coreAuthorizationToken
    await store.resumeProxyAfterAuthorization(token: token)
    #expect(calls.sent.withLock { $0 } == [true, true])
    #expect(!store.needsServiceAuthorization)
    #expect(store.status == .off)
  }
  @Test func offDuringAuthorizationPreventsLateResume() async {
    let calls = CoreCalls()
    let store = makeStore(calls)
    await store.setProxyFromWorkbench(true)
    store.apply(
      .operation(
        OperationResult(operationID: "on", ok: false, error: ProductError(code: "core_required"))))
    let token = store.coreAuthorizationToken
    await store.setProxyFromWorkbench(false)
    await store.resumeProxyAfterAuthorization(token: token)
    #expect(calls.sent.withLock { $0 } == [true, false])
    #expect(!store.needsServiceAuthorization)
  }
  @Test func cancelAuthorizationKeepsProxyOff() async {
    let calls = CoreCalls()
    let store = makeStore(calls)
    await store.setProxyFromWorkbench(true)
    store.apply(
      .operation(
        OperationResult(operationID: "on", ok: false, error: ProductError(code: "core_required"))))
    let token = store.coreAuthorizationToken
    store.cancelCoreAuthorization()
    await store.resumeProxyAfterAuthorization(token: token)
    #expect(calls.sent.withLock { $0 } == [true])
    #expect(store.status == .off)
  }
}
