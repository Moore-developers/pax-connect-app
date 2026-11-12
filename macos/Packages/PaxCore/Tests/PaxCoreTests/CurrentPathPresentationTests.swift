import Testing

@testable import PaxCore

@Suite("当前通道恢复状态") @MainActor
struct CurrentPathPresentationTests {
  private func store(state: String, cause: String = "") -> ConnectionStore {
    let store = ConnectionStore(currentPathCommand: { _ in "check" })
    var snapshot = DaemonSnapshot(
      snapshotVersion: 1, status: .degraded, routes: [], currentRoute: nil,
      todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: [],
      degradedReason: .init(code: "protection_unverified"))
    snapshot.currentPathCheck.state = state
    snapshot.currentPathCheck.cause = cause
    store.markHandshaked(
      DaemonHandshake(
        daemonVersion: "test", protocolVersion: 22, snapshotSeq: 1,
        capabilities: ["connection.current_path.v1", "connection.current_path.wake.v1"]),
      snapshot: snapshot)
    return store
  }

  @Test("网络等待不冒充已连接，手动操作合并当前等待")
  func waiting() {
    let value = store(state: "waiting_network")
    #expect(value.currentPathSummary == "代理已开启 · 等待网络恢复")
    #expect(value.canCheckCurrentPath)
  }

  @Test("休眠证据不请求公网，等待超时不声称全网断开")
  func sleepingAndTimeout() {
    let sleeping = store(state: "stale", cause: "device_sleeping")
    #expect(sleeping.currentPathSummary == "系统休眠 · 通道尚未验证")
    #expect(!sleeping.canCheckCurrentPath)
    let timeout = store(state: "inconclusive", cause: "physical_network_wait")
    #expect(timeout.currentPathSummary == "代理已开启 · 等待网络恢复")
    #expect(!timeout.feedback.contains { $0.id == "current-path" })
  }
}
