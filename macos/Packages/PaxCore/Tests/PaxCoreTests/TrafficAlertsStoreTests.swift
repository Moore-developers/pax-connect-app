import Foundation
import Synchronization
import Testing

@testable import PaxCore

@MainActor struct TrafficAlertsStoreTests {
  private func notice(_ id: String) -> TrafficAlertNotice {
    .init(
      id: id, key: "app:test:u", source: "Test", sourceKind: "app", direction: "upload",
      windowSeconds: 300, bytes: 100, threshold: 100, until: Date())
  }
  private func state(_ id: String) -> TrafficAlertState {
    var settings = TrafficAlertSettings()
    settings.enabled = true
    return .init(
      settings: settings, monitorState: "collecting", notices: [notice(id)], activeNoticeIDs: [id])
  }
  private func ready(_ value: TrafficAlertState) -> ConnectionUpdate {
    var snapshot = DaemonSnapshot(
      snapshotVersion: 1, status: .protected, routes: [],
      currentRoute: nil, todayTraffic: .zero, settings: .init(autoConnect: false), recentLogs: [])
    snapshot.trafficAlerts = value
    return .ready(
      .init(
        daemonVersion: "test", protocolVersion: DaemonConnection.protocolVersion, snapshotSeq: 1,
        capabilities: ["traffic.alerts.v1"]), snapshot)
  }
  private func settle(_ condition: () -> Bool) async throws {
    for _ in 0..<200 where !condition() { try await Task.sleep(for: .milliseconds(2)) }
  }
  @Test("重连与App重建不重复，暂时隐藏仍保留投递ID，轮次结束清理")
  func deliveryLedger() async throws {
    let first = state("one")
    var calls: [String] = []
    var ledger: [String] = []
    let make = { (ids: [String]) in
      TrafficAlertsStore(
        read: { first }, write: { _ in first },
        deliver: { calls.append($0.id) }, deliveredIDs: ids, persist: { ledger = $0 })
    }
    let store = make([])
    store.consume(ready(first))
    try await settle { calls.count == 1 }
    #expect(calls == ["one"])
    #expect(ledger == ["one"])
    store.consume(.disconnected)
    store.consume(ready(first))
    var hidden = first
    hidden.notices = []
    store.consume(.events([.trafficAlerts(hidden)]))
    #expect(ledger == ["one"])
    store.consume(.events([.trafficAlerts(first)]))
    try await Task.sleep(for: .milliseconds(10))
    #expect(calls.count == 1)
    let restarted = make(ledger)
    restarted.consume(ready(first))
    try await Task.sleep(for: .milliseconds(10))
    #expect(calls.count == 1)
    let next = state("daily")
    restarted.consume(.events([.trafficAlerts(next)]))
    try await settle { calls.count == 2 }
    #expect(calls == ["one", "daily"])
    #expect(ledger == ["daily"])
    var ended = next
    ended.notices = []
    ended.activeNoticeIDs = []
    restarted.consume(.events([.trafficAlerts(ended)]))
    #expect(ledger.isEmpty)
  }
  @Test("权限拒绝不记投递成功，允许后投递当前通知")
  func deniedThenAllowed() async throws {
    let value = state("denied")
    var allowed = false
    var count = 0
    var ledger: [String] = []
    let store = TrafficAlertsStore(
      read: { value }, write: { _ in value },
      deliver: { _ in
        if !allowed { throw TrafficAlertDeliveryFailure.denied }
        count += 1
      }, persist: { ledger = $0 })
    store.consume(ready(value))
    try await settle { store.notificationDenied }
    #expect(count == 0)
    #expect(ledger.isEmpty)
    allowed = true
    store.setNotificationPermission(true)
    try await settle { count == 1 }
    #expect(ledger == ["denied"])
    #expect(!store.notificationDenied)
  }
  @Test("旧读取不能覆盖Watch已提交的新配置")
  func staleRead() async throws {
    let initial = state("old")
    let pending = Mutex<CheckedContinuation<TrafficAlertState, Never>?>(nil)
    let store = TrafficAlertsStore(
      read: {
        await withCheckedContinuation { continuation in pending.withLock { $0 = continuation } }
      }, write: { _ in initial })
    store.consume(ready(initial))
    let reading = Task { await store.refresh() }
    try await settle { pending.withLock { $0 != nil } }
    var current = initial
    current.settings.revision = 2
    current.settings.enabled = false
    current.notices = []
    current.activeNoticeIDs = []
    store.consume(.events([.trafficAlerts(current)]))
    pending.withLock {
      $0?.resume(returning: initial)
      $0 = nil
    }
    await reading.value
    #expect(store.state?.settings.revision == 2)
    #expect(store.state?.settings.enabled == false)
  }
}

extension TrafficAlertsStoreTests {
  @Test("同配置版本的旧读取不能重新投递已经回落的通知")
  func staleNoticeProjection() async throws {
    let initial = state("ended")
    let pending = Mutex<CheckedContinuation<TrafficAlertState, Never>?>(nil)
    var calls = 0
    let store = TrafficAlertsStore(
      read: {
        await withCheckedContinuation { continuation in pending.withLock { $0 = continuation } }
      }, write: { _ in initial }, deliver: { _ in calls += 1 })
    store.consume(ready(initial))
    try await settle { calls == 1 }
    let reading = Task { await store.refresh() }
    try await settle { pending.withLock { $0 != nil } }
    var ended = initial
    ended.revision = 2
    ended.notices = []
    ended.activeNoticeIDs = []
    store.consume(.events([.trafficAlerts(ended)]))
    pending.withLock {
      $0?.resume(returning: initial)
      $0 = nil
    }
    await reading.value
    try await Task.sleep(for: .milliseconds(10))
    #expect(calls == 1)
    #expect(store.state?.notices.isEmpty == true)
  }
}
