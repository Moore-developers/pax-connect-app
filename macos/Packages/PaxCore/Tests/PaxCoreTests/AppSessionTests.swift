import Testing

@testable import PaxCore

@MainActor @Suite("App 生命周期后台")
struct AppSessionTests {
  @Test("缓存有效免密启动；退出失败保留运行状态供重试")
  func cachedStartAndFailedStop() async {
    let store = AppSessionStore(
      start: { password in
        #expect(password == nil)
      }, stop: { throw AppSessionFailure.shutdownFailed })
    await store.start()
    #expect(store.state == .running)
    await store.stop()
    #expect(store.state == .running)
    #expect(store.failure == .shutdownFailed)
  }
  @Test("要求密码与错误密码不会启动代理；取消不保留续接")
  func passwordFlow() async {
    let store = AppSessionStore(
      start: { password in
        if password == nil { throw AppSessionFailure.passwordRequired }
        if password != "fixture" { throw AppSessionFailure.authenticationFailed }
      }, stop: {})
    await store.start()
    #expect(store.state == .needsPassword)
    await store.start(password: "wrong")
    #expect(store.state == .needsPassword)
    await store.start(password: "fixture")
    #expect(store.state == .running)
    await store.stop()
    #expect(store.state == .stopped)
  }
}

extension AppSessionTests {
  @Test("未就绪的启动也必须请求清理；后台消失后可重新授权")
  func incompleteStartupAndLostSession() async {
    var starts = 0
    var stops = 0
    let store = AppSessionStore(
      start: { _ in
        starts += 1
        if starts == 1 { throw AppSessionFailure.launchFailed }
      }, stop: { stops += 1 })
    await store.start()
    #expect(store.state == .stopped)
    await store.stop()
    #expect(stops == 1)
    await store.start()
    await store.start()
    #expect(starts == 2)
    store.markUnavailable()
    #expect(store.state == .stopped && store.failure == .launchFailed)
    await store.start()
    #expect(starts == 3 && store.state == .running)
  }
}

extension AppSessionTests {
  @Test("旧会话未释放不会进入运行，可清理后重新启动")
  func residueRetry() async {
    var released = false
    let store = AppSessionStore(
      start: { _ in if !released { throw AppSessionFailure.cleanupFailed } }, stop: {})
    await store.start()
    #expect(store.state == .stopped)
    #expect(store.failure == .cleanupFailed)
    released = true
    await store.start()
    #expect(store.state == .running)
    #expect(store.failure == nil)
  }
}

extension AppSessionTests {
  @Test("恢复存储失败保留具体原因，不要求反复输入密码")
  func recoveryStorageFailure() async {
    let store = AppSessionStore(
      start: { _ in throw AppSessionFailure.recoveryStorageUnavailable }, stop: {})
    await store.start()
    #expect(store.state == .stopped)
    #expect(store.failure == .recoveryStorageUnavailable)
    #expect(store.failure?.message.contains("恢复存储") == true)
  }
}
