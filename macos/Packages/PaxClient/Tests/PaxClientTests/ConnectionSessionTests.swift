import Foundation
import Testing

@testable import PaxClient

@Suite("连接会话")
struct ConnectionSessionTests {
  @Test("暂时断线后重试并以同步快照恢复；权限错误停止重试")
  func reconnects() async {
    let transport = RecoveryTransport()
    let recorder = SessionRecorder()
    let session = ConnectionSession(
      transport: transport,
      retrySleep: { delay in
        await recorder.recordDelay(delay)
      }, jitter: { 1 })
    await session.run { update in await recorder.record(update) }
    let values = await recorder.values
    #expect(values.contains("retry"))
    #expect(values.contains("ready:0"))
    #expect(values.last == "failed")
    #expect(await recorder.delays == [.milliseconds(500)])
  }
  @Test("重复和旧版本事件被丢弃，缺口拉全量；新daemon从0恢复")
  func orderingAndRestart() async {
    let recorder = SessionRecorder()
    let source = ScriptedTransport(scripts: [
      Script(
        snapshot: 5,
        events: [
          envelope(6, 5, .aiUsage(usage(1))),
          envelope(6, 5, .aiUsage(usage(999))),
          envelope(7, 4, .aiUsage(usage(999))),
          envelope(7, 5, .aiUsage(usage(3))),
          envelope(9, 5, .aiUsage(usage(999))),
        ], ending: .permissionDenied),
      Script(
        snapshot: 0, events: [envelope(1, 0, .aiUsage(usage(8)))], ending: .permissionDenied),
    ])
    let session = ConnectionSession(transport: source, retrySleep: { _ in }, jitter: { 1 })
    await session.run { await recorder.record($0) }
    #expect(await recorder.usageRevisions == [1, 3, 8])
    #expect(await recorder.values.filter { $0.hasPrefix("ready:") } == ["ready:5", "ready:0"])
  }

  @Test("批次满200条立即提交，顺序保留")
  func boundedBatch() async {
    let recorder = SessionRecorder()
    let logs = (1...201).map { index in
      envelope(UInt64(index), 0, .aiUsage(usage(UInt64(index))))
    }
    let source = ScriptedTransport(scripts: [
      Script(snapshot: 0, events: logs, ending: .permissionDenied)
    ])
    let session = ConnectionSession(
      transport: source,
      batchSleep: { _ in
        try await Task.sleep(for: .seconds(60))
      })
    await session.run { await recorder.record($0) }
    #expect(await recorder.batchSizes == [200, 1])
    #expect(await recorder.usageRevisions.first == 1)
    #expect(await recorder.usageRevisions.last == 201)
    #expect(await recorder.values.last == "failed")
  }

  @Test("完整快照跨越缺口，之后旧版本数据不再覆盖状态")
  func fullSnapshotBoundary() async {
    let recorder = SessionRecorder()
    let full = DaemonSnapshot(
      snapshotVersion: 20, status: .off, routes: [], currentRoute: nil,
      todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: [])
    let source = ScriptedTransport(scripts: [
      Script(
        snapshot: 0,
        events: [
          envelope(20, 20, .snapshot(full)), envelope(21, 19, .aiUsage(usage(999))),
          envelope(21, 20, .aiUsage(usage(4))),
        ], ending: .permissionDenied)
    ])
    let session = ConnectionSession(transport: source)
    await session.run { await recorder.record($0) }
    #expect(await recorder.usageRevisions == [4])
    #expect(await recorder.values.filter { $0 == "retry" }.isEmpty)
  }

  @Test("退避递增至8秒，成功后从半秒重新开始")
  func boundedBackoff() async {
    let recorder = SessionRecorder()
    let source = BackoffTransport()
    let session = ConnectionSession(
      transport: source, retrySleep: { await recorder.recordDelay($0) }, jitter: { 1 })
    await session.run { await recorder.record($0) }
    #expect(
      await recorder.delays == [
        .milliseconds(500), .seconds(1), .seconds(2), .seconds(4),
        .seconds(8), .seconds(8), .milliseconds(500),
      ])
  }

  @Test("取消正在等待的连接后不重连；同一会话只有一条订阅")
  func cancellation() async throws {
    let recorder = SessionRecorder()
    let source = HoldingTransport()
    let session = ConnectionSession(transport: source)
    let running = Task { await session.run { await recorder.record($0) } }
    await source.waitUntilReady()
    // A second run cannot open another stream on the same session.
    await session.run { await recorder.record($0) }
    #expect(await source.opened == 1)
    running.cancel()
    await running.value
    #expect(await recorder.values.last == "disconnected")
    #expect(await source.active == 0)
  }

  @Test("未满缓冲按250ms提交，早于连接结束")
  func timedBatch() async {
    let recorder = SessionRecorder()
    let source = TimedTransport(recorder: recorder)
    let session = ConnectionSession(
      transport: source,
      batchSleep: { delay in
        await recorder.recordDelay(delay)
        try await Task.sleep(for: delay)
      })
    await session.run { await recorder.record($0) }
    #expect(await recorder.delays == [.milliseconds(250)])
    #expect(await source.deliveredBeforeEnd)
  }

  @Test("历史通知合并保留可选投影，删除与检查结果立即交付")
  func historyNotifications() async {
    let recorder = SessionRecorder()
    var first = HistoryDelta()
    first.history.logsRevision = 1
    first.traffic = traffic(12)
    first.replaceRecentLogs = true
    var second = HistoryDelta()
    second.history.logsRevision = 2
    var cleared = HistoryDelta()
    cleared.history.logsRevision = 3
    cleared.history.logDeletionGeneration = 1
    let source = ScriptedTransport(scripts: [
      Script(
        snapshot: 0,
        events: [
          envelope(1, 1, .historyDelta(first)), envelope(2, 2, .historyDelta(second)),
          envelope(3, 3, .historyDelta(cleared)),
          envelope(4, 4, .currentPathCheck(CurrentPathCheck())),
        ], ending: .permissionDenied)
    ])
    let session = ConnectionSession(
      transport: source, batchSleep: { _ in try await Task.sleep(for: .seconds(60)) })
    await session.run { await recorder.record($0) }
    #expect(await recorder.batchSizes == [1, 1, 1])
    #expect(await recorder.historyRevisions == [2, 3])
    #expect(await recorder.historyTraffic == [12])
  }
  private func envelope(_ seq: UInt64, _ version: UInt64, _ event: DaemonEvent)
    -> DaemonEventEnvelope
  {
    DaemonEventEnvelope(seq: seq, snapshotVersion: version, event: event)
  }
  private func traffic(_ uploaded: UInt64) -> TrafficSnapshot {
    TrafficSnapshot(
      uploadBytes: uploaded, downloadBytes: 0, proxiedBytes: uploaded,
      directBytes: 0, unclassifiedBytes: 0, activeConnections: 0)
  }
  private func usage(_ revision: UInt64) -> AIUsageCollectionState {
    var state = AIUsageCollectionState()
    state.revision = revision
    return state
  }

}

actor RecoveryTransport: DaemonTransport {
  private var attempts = 0
  func run(
    onReady: @escaping @Sendable (DaemonHandshake, DaemonSnapshot) async throws -> Void,
    onEvent: @escaping @Sendable (DaemonEventEnvelope) async throws -> Void
  ) async throws {
    attempts += 1
    if attempts == 1 { throw ClientError.unavailable }
    try await onReady(
      DaemonHandshake(daemonVersion: "test", protocolVersion: 1, snapshotSeq: 0),
      DaemonSnapshot(
        snapshotVersion: 0, status: .off, routes: [], currentRoute: nil,
        todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: []))
    throw ClientError.permissionDenied
  }
}

actor SessionRecorder {
  private(set) var values: [String] = []
  private(set) var delays: [Duration] = []
  private(set) var usageRevisions: [UInt64] = []
  private(set) var batchSizes: [Int] = []
  private(set) var historyRevisions: [UInt64] = []
  private(set) var historyTraffic: [UInt64] = []
  func recordDelay(_ delay: Duration) { delays.append(delay) }
  func record(_ update: ConnectionUpdate) {
    switch update {
    case .connecting: values.append("connecting")
    case .ready(_, let snapshot): values.append("ready:\(snapshot.snapshotVersion)")
    case .events(let events):
      values.append("events")
      batchSizes.append(events.count)
      for event in events {
        if case .historyDelta(let delta) = event {
          historyRevisions.append(delta.history.logsRevision)
          if let traffic = delta.traffic { historyTraffic.append(traffic.uploadBytes) }
        }
        if case .aiUsage(let state) = event { usageRevisions.append(state.revision) }
      }
    case .retrying: values.append("retry")
    case .failed: values.append("failed")
    case .disconnected: values.append("disconnected")
    }
  }
}

struct Script: Sendable {
  let snapshot: UInt64
  let events: [DaemonEventEnvelope]
  let ending: ClientError
}
actor ScriptedTransport: DaemonTransport {
  private var scripts: [Script]
  init(scripts: [Script]) { self.scripts = scripts }
  func run(
    onReady: @escaping @Sendable (DaemonHandshake, DaemonSnapshot) async throws -> Void,
    onEvent: @escaping @Sendable (DaemonEventEnvelope) async throws -> Void
  ) async throws {
    guard !scripts.isEmpty else { throw ClientError.permissionDenied }
    let script = scripts.removeFirst()
    try await onReady(
      DaemonHandshake(daemonVersion: "test", protocolVersion: 1, snapshotSeq: script.snapshot),
      DaemonSnapshot(
        snapshotVersion: script.snapshot, status: .off, routes: [], currentRoute: nil,
        todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: []))
    for event in script.events { try await onEvent(event) }
    throw script.ending
  }
}

actor BackoffTransport: DaemonTransport {
  private var attempts = 0
  func run(
    onReady: @escaping @Sendable (DaemonHandshake, DaemonSnapshot) async throws -> Void,
    onEvent: @escaping @Sendable (DaemonEventEnvelope) async throws -> Void
  ) async throws {
    attempts += 1
    if attempts <= 6 { throw ClientError.unavailable }
    if attempts == 8 { throw ClientError.permissionDenied }
    try await onReady(
      DaemonHandshake(daemonVersion: "test", protocolVersion: 1, snapshotSeq: 0),
      DaemonSnapshot(
        snapshotVersion: 0, status: .off, routes: [], currentRoute: nil,
        todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: []))
    throw ClientError.unavailable
  }
}
actor HoldingTransport: DaemonTransport {
  private(set) var opened = 0
  private(set) var active = 0
  private var ready = false
  private var waiter: CheckedContinuation<Void, Never>?
  func waitUntilReady() async {
    if ready { return }
    await withCheckedContinuation { waiter = $0 }
  }
  func run(
    onReady: @escaping @Sendable (DaemonHandshake, DaemonSnapshot) async throws -> Void,
    onEvent: @escaping @Sendable (DaemonEventEnvelope) async throws -> Void
  ) async throws {
    opened += 1
    active += 1
    defer { active -= 1 }
    try await onReady(
      DaemonHandshake(daemonVersion: "test", protocolVersion: 1, snapshotSeq: 0),
      DaemonSnapshot(
        snapshotVersion: 0, status: .off, routes: [], currentRoute: nil,
        todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: []))
    ready = true
    waiter?.resume()
    waiter = nil
    try await Task.sleep(for: .seconds(60))
  }
}

actor TimedTransport: DaemonTransport {
  let recorder: SessionRecorder
  private(set) var deliveredBeforeEnd = false
  init(recorder: SessionRecorder) { self.recorder = recorder }
  func run(
    onReady: @escaping @Sendable (DaemonHandshake, DaemonSnapshot) async throws -> Void,
    onEvent: @escaping @Sendable (DaemonEventEnvelope) async throws -> Void
  ) async throws {
    try await onReady(
      DaemonHandshake(daemonVersion: "test", protocolVersion: 1, snapshotSeq: 0),
      DaemonSnapshot(
        snapshotVersion: 0, status: .off, routes: [], currentRoute: nil,
        todayTraffic: .zero, settings: DaemonSettings(autoConnect: false), recentLogs: []))
    try await onEvent(
      DaemonEventEnvelope(
        seq: 1, snapshotVersion: 0,
        event: .aiUsage(AIUsageCollectionState())))
    try await Task.sleep(for: .milliseconds(500))
    deliveredBeforeEnd = await recorder.usageRevisions == [0]
    throw ClientError.permissionDenied
  }
}
