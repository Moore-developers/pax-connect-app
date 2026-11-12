import Testing

@testable import PaxCore

@MainActor
struct DaemonSetupTests {
  @Test("注册抛错但系统已待批准时，展示待批准而不是失败")
  func pendingApprovalWins() async {
    let service = SetupServiceFake()
    let store = DaemonSetupStore(service: service, validate: { nil })
    await store.enable()
    #expect(store.state == .requiresApproval)
    #expect(store.error == nil)
    #expect(!store.canConnect)
  }
  @Test("注册失败可见且重新检查不会清掉失败，撤销停止连接")
  func failureAndRevocation() async {
    let service = SetupServiceFake()
    service.failWithoutRegistration = true
    let store = DaemonSetupStore(service: service, validate: { nil })
    await store.enable()
    store.refresh()
    #expect(store.error == .registrationFailed)
    service.state = .enabled
    store.refresh()
    #expect(store.canConnect)
    service.state = .requiresApproval
    store.refresh()
    #expect(!store.canConnect)
  }
  @Test("安装位置校验失败不调用注册")
  func invalidInstallation() async {
    let service = SetupServiceFake()
    let store = DaemonSetupStore(service: service, validate: { .installationRequired })
    await store.enable()
    #expect(service.state == .notRegistered)
    #expect(store.error == .installationRequired)
  }

}
@MainActor
final class SetupServiceFake: BackgroundService {
  var unregisterThrowsAfterRemoval = false
  var unregisterFails = false
  var registerCount = 0
  var unregisterCount = 0
  var failWithoutRegistration = false
  var state: BackgroundServiceState = .notRegistered
  func register() throws {
    registerCount += 1
    if failWithoutRegistration { throw ClientError.registrationFailed }
    state = .requiresApproval
    throw ClientError.registrationFailed
  }
  func unregister() async throws {
    unregisterCount += 1
    if unregisterFails { throw ClientError.updateFailed }
    state = .notRegistered
    if unregisterThrowsAfterRemoval { throw ClientError.updateFailed }
  }
  func openSettings() {}
}

@MainActor
struct DaemonUpdateTests {
  @Test("相同版本但指纹变化会更新，注册失败保留原指纹")
  func changedFingerprint() async {
    let service = SetupServiceFake()
    service.state = .enabled
    service.failWithoutRegistration = true
    var saved: String? = "old"
    let store = DaemonSetupStore(
      service: service, validate: { nil }, fingerprint: { "new" },
      registeredFingerprint: { saved }, saveFingerprint: { saved = $0 })
    await store.reconcileUpdate()
    #expect(saved == "old")
    #expect(store.error == .updateFailed)
    #expect(!store.canConnect)
  }
  @Test("相同指纹不重复登记，未知指纹在握手证明身份后才采纳")
  func sameAndUnknown() async {
    let service = SetupServiceFake()
    service.state = .enabled
    var saved: String?
    let store = DaemonSetupStore(
      service: service, validate: { nil }, fingerprint: { "same" },
      registeredFingerprint: { saved }, saveFingerprint: { saved = $0 })
    await store.reconcileUpdate()
    #expect(saved == nil)
    #expect(service.unregisterCount == 0)
    await store.confirmIdentity(
      DaemonHandshake(
        daemonVersion: "1", protocolVersion: 1,
        snapshotSeq: 0, serviceFingerprint: "same"))
    #expect(saved == "same")
    await store.reconcileUpdate()
    #expect(service.unregisterCount == 0)
  }

  @Test("更新登记待批准即成功，重复触发不再次更新")
  func updatePendingApproval() async {
    let service = SetupServiceFake()
    service.state = .enabled
    var saved: String? = "old"
    var stopped = false
    let store = DaemonSetupStore(
      service: service, validate: { nil }, fingerprint: { "new" },
      registeredFingerprint: { saved }, saveFingerprint: { saved = $0 },
      beforeUpdate: { stopped = true })
    await store.reconcileUpdate()
    #expect(stopped)
    #expect(saved == "new")
    #expect(store.state == .requiresApproval)
    #expect(store.error == nil)
    await store.reconcileUpdate()
    #expect(service.unregisterCount == 1)
    #expect(service.registerCount == 1)
  }

  @Test("注销失败保留旧指纹，刷新不清掉更新失败，明确重试才能再尝试")
  func unregisterFailure() async {
    let service = SetupServiceFake()
    service.state = .enabled
    service.unregisterFails = true
    var saved: String? = "old"
    let store = DaemonSetupStore(
      service: service, validate: { nil }, fingerprint: { "new" },
      registeredFingerprint: { saved }, saveFingerprint: { saved = $0 })
    await store.reconcileUpdate()
    store.refresh()
    #expect(saved == "old")
    #expect(store.error == .updateFailed)
    await store.reconcileUpdate()
    #expect(service.unregisterCount == 1)
    service.unregisterFails = false
    await store.enable()
    #expect(service.unregisterCount == 2)
    #expect(saved == "new")
  }

  @Test("同版本且身份未知不自动更新，不兼容协议由连接层停止")
  func unknownIdentity() async {
    let service = SetupServiceFake()
    service.state = .enabled
    var saved: String?
    let store = DaemonSetupStore(
      service: service, validate: { nil }, fingerprint: { "new" },
      expectedDaemonVersion: { "1" }, registeredFingerprint: { saved },
      saveFingerprint: { saved = $0 })
    await store.confirmIdentity(
      DaemonHandshake(daemonVersion: "1", protocolVersion: 1, snapshotSeq: 0))
    #expect(service.unregisterCount == 0)
    #expect(saved == nil)
    #expect(store.error == .serviceIdentityUnknown)
    store.refresh()
    #expect(!store.canConnect)
  }

  @Test("运行服务指纹确实不同只更新一次")
  func mismatchedIdentity() async {
    let service = SetupServiceFake()
    service.state = .enabled
    var saved: String?
    let store = DaemonSetupStore(
      service: service, validate: { nil }, fingerprint: { "new" },
      registeredFingerprint: { saved }, saveFingerprint: { saved = $0 })
    let identity = DaemonHandshake(
      daemonVersion: "1", protocolVersion: 1, snapshotSeq: 0, serviceFingerprint: "old")
    await store.confirmIdentity(identity)
    await store.confirmIdentity(identity)
    #expect(service.unregisterCount == 1)
    #expect(saved == "new")
  }

  @Test("更新中重复触发去重，取消旧连接等待后不继续登记")
  func cancelUpdate() async {
    let service = SetupServiceFake()
    service.state = .enabled
    var stopped = false
    var saved: String? = "old"
    let store = DaemonSetupStore(
      service: service, validate: { nil }, fingerprint: { "new" },
      registeredFingerprint: { saved }, saveFingerprint: { saved = $0 },
      beforeUpdate: {
        stopped = true
        do { try await Task.sleep(for: .seconds(60)) } catch {}
      })
    let task = Task { await store.reconcileUpdate() }
    while !stopped { await Task.yield() }
    await store.reconcileUpdate()
    #expect(service.unregisterCount == 0)
    task.cancel()
    await task.value
    #expect(saved == "old")
    #expect(store.error == .updateFailed)
    #expect(service.registerCount == 0)
  }

  @Test("注销报错但系统已移除服务时继续登记")
  func removedDespiteError() async {
    let service = SetupServiceFake()
    service.state = .enabled
    service.unregisterThrowsAfterRemoval = true
    var saved: String? = "old"
    let store = DaemonSetupStore(
      service: service, validate: { nil }, fingerprint: { "new" },
      registeredFingerprint: { saved }, saveFingerprint: { saved = $0 })
    await store.reconcileUpdate()
    #expect(saved == "new")
    #expect(store.state == .requiresApproval)
    #expect(store.error == nil)
  }

  @Test("代理运行时阻止更新仍保留连接与关闭能力")
  func runningProxyBlocksUpdateWithoutBlockingConnection() async {
    let service = SetupServiceFake()
    service.state = .enabled
    let store = DaemonSetupStore(
      service: service, validate: { nil }, fingerprint: { "new" },
      registeredFingerprint: { "old" }, beforeUpdate: { throw ClientError.proxyMustBeOff })
    await store.reconcileUpdate()
    #expect(store.error == .proxyMustBeOff)
    #expect(store.canConnect)
    #expect(service.unregisterCount == 0)
  }

}
