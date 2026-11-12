import Testing

@testable import PaxCore

@MainActor @Suite("后台恢复入口")
struct BackendRecoveryTests {
  @Test("断线保留代理快照；恢复动作不发送代理命令，恢复中拒绝重复点击")
  func recoveryAdmission() {
    let store = ConnectionStore()
    store.apply(
      .snapshot(
        .init(
          snapshotVersion: 3, status: .protected, routes: [], currentRoute: nil,
          todayTraffic: .zero, settings: .init(autoConnect: false), recentLogs: [])))
    var recoveries = 0
    store.requestBackendRecovery = {
      recoveries += 1
      store.setBackendRecoveryInProgress(true)
    }
    store.consume(.failed(.serviceIdentityUnknown))
    #expect(store.status == .protected)
    #expect(store.unresolvedFeedback.first?.action == .recoverBackend)
    #expect(store.unresolvedFeedback.first?.detail.contains("当前 App") == true)
    store.recoverBackend()
    store.recoverBackend()
    #expect(recoveries == 1)
    store.setBackendRecoveryInProgress(false)
    store.consume(
      .ready(
        .init(daemonVersion: "new", protocolVersion: 40, snapshotSeq: 0),
        .init(
          snapshotVersion: 0, status: .degraded, routes: [], currentRoute: nil, todayTraffic: .zero,
          settings: .init(autoConnect: false), recentLogs: [],
          degradedReason: .init(code: "runtime_observation_unavailable"))))
    store.recoverBackend()
    #expect(recoveries == 1)
    #expect(store.connection == .connected)
    #expect(store.status == .degraded)
  }

  @Test("AI配置断线禁写；恢复首帧后重新开放读取")
  func profilesFollowBackend() async {
    let store = AIProfilesStore(connection: .init(socketPath: "/unused"))
    store.consume(.disconnected)
    #expect(!store.available)
    #expect(
      await store.save(
        .init(id: "profile", name: "test", endpoint: "https://example.invalid", multiplier: 1),
        key: "") == false)
    #expect(store.error == "后台连接已断开，请恢复后台后重试")
    let plan = await store.plan(.codex, profile: nil)
    #expect(plan == nil)
    #expect(store.error == "后台连接已断开，请恢复后台后重试")
    store.consume(
      .ready(
        .init(daemonVersion: "new", protocolVersion: 40, snapshotSeq: 0),
        .init(
          snapshotVersion: 0, status: .off, routes: [], currentRoute: nil, todayTraffic: .zero,
          settings: .init(autoConnect: false), recentLogs: [])))
    #expect(store.available)
  }
}
