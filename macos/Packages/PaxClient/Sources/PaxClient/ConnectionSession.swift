import Foundation

/// 会话消费的最小传输边界；实现负责握手、全量快照和订阅首帧确认。
public protocol DaemonTransport: Sendable {
  func run(
    onReady: @escaping @Sendable (DaemonHandshake, DaemonSnapshot) async throws -> Void,
    onEvent: @escaping @Sendable (DaemonEventEnvelope) async throws -> Void
  ) async throws
}

/// 提交给 MainActor Store 的有序连接与数据更新。
public enum ConnectionUpdate: Sendable {
  case connecting
  case ready(DaemonHandshake, DaemonSnapshot)
  case events([DaemonEvent])
  case retrying(ClientError, delay: Duration)
  case failed(ClientError)
  case disconnected
}

/// 唯一连接会话：退避、游标、批次与取消在同一 actor 中隔离。
public actor ConnectionSession {
  private let transport: any DaemonTransport
  private let retrySleep: @Sendable (Duration) async throws -> Void
  private let batchSleep: @Sendable (Duration) async throws -> Void
  private let jitter: @Sendable () -> Double
  private var running = false
  private var runID: UInt64 = 0
  private var generation: UInt64 = 0
  private var attempt: Task<Void, any Error>?
  private var batchTask: Task<Void, Never>?
  private var pending: [DaemonEvent] = []
  private var cursor: UInt64?
  private var version: UInt64 = 0
  private var history = HistoryState()
  private var ready = false
  private var sink: (@Sendable (ConnectionUpdate) async -> Void)?

  public init(
    transport: any DaemonTransport,
    retrySleep: @escaping @Sendable (Duration) async throws -> Void = {
      try await Task.sleep(for: $0)
    },
    batchSleep: @escaping @Sendable (Duration) async throws -> Void = {
      try await Task.sleep(for: $0)
    },
    jitter: @escaping @Sendable () -> Double = { Double.random(in: 0.8...1.2) }
  ) {
    self.transport = transport
    self.retrySleep = retrySleep
    self.batchSleep = batchSleep
    self.jitter = jitter
  }

  /// 持续恢复直到取消或遇到需用户处理的错误；同一实例禁止并行 run。
  public func run(onUpdate: @escaping @Sendable (ConnectionUpdate) async -> Void) async {
    guard !running, !Task.isCancelled else { return }
    running = true
    runID &+= 1
    let owner = runID
    sink = onUpdate
    defer {
      running = false
      sink = nil
      attempt = nil
      batchTask?.cancel()
      batchTask = nil
      pending.removeAll()
    }
    await withTaskCancellationHandler {
      await loop()
    } onCancel: {
      Task { await self.cancelAttempt(owner: owner) }
    }
  }

  private func loop() async {
    var failures = 0
    while !Task.isCancelled {
      generation &+= 1
      let token = generation
      cursor = nil
      version = 0
      ready = false
      await sink?(.connecting)
      if Task.isCancelled { break }
      let transport = self.transport
      let task = Task {
        try Task.checkCancellation()
        try await transport.run(
          onReady: { handshake, snapshot in
            try await self.acceptReady(handshake, snapshot: snapshot, token: token)
          },
          onEvent: { event in try await self.accept(event, token: token) }
        )
      }
      attempt = task
      let failure: ClientError
      do {
        try await task.value
        failure = .unavailable
      } catch {
        failure = ClientError.classify(error)
      }
      attempt = nil
      if Task.isCancelled { break }
      await flush(token: token)
      generation &+= 1
      if ready { failures = 0 }
      ready = false
      guard failure.isRetryable else {
        await sink?(.failed(failure))
        return
      }
      let base = [0.5, 1.0, 2.0, 4.0, 8.0][min(failures, 4)]
      let delay = Duration.seconds(base * min(1.2, max(0.8, jitter())))
      failures += 1
      await sink?(.retrying(failure, delay: delay))
      if Task.isCancelled { break }
      let sleep = retrySleep
      let waiting = Task {
        try Task.checkCancellation()
        try await sleep(delay)
      }
      attempt = waiting
      do { try await waiting.value } catch {
        if !Task.isCancelled {
          await sink?(.failed(.unknown))
          return
        }
      }
      attempt = nil
    }
    pending.removeAll()
    await sink?(.disconnected)
  }

  private func cancelAttempt(owner: UInt64) {
    guard running, runID == owner else { return }
    attempt?.cancel()
    batchTask?.cancel()
    generation &+= 1
    pending.removeAll()
  }

  private func acceptReady(
    _ handshake: DaemonHandshake, snapshot: DaemonSnapshot, token: UInt64
  ) async throws {
    guard token == generation, !Task.isCancelled else { throw CancellationError() }
    cursor = snapshot.snapshotVersion
    version = snapshot.snapshotVersion
    ready = true
    history = snapshot.history
    await sink?(.ready(handshake, snapshot))
  }

  private func accept(_ envelope: DaemonEventEnvelope, token: UInt64) async throws {
    guard token == generation, !Task.isCancelled else { throw CancellationError() }
    guard ready, let last = cursor else { throw ClientError.invalidEvent }
    if case .snapshot(let snapshot) = envelope.event {
      guard envelope.seq == snapshot.snapshotVersion,
        envelope.snapshotVersion == snapshot.snapshotVersion
      else { throw ClientError.invalidEvent }
      guard envelope.seq > last else { return }
      await flush(token: token)
      cursor = envelope.seq
      version = envelope.snapshotVersion
      history = snapshot.history
      await sink?(.events([envelope.event]))
      return
    }
    guard envelope.seq > last, envelope.snapshotVersion >= version else { return }
    guard last != UInt64.max, envelope.seq == last + 1 else { throw ClientError.eventGap }
    cursor = envelope.seq
    version = envelope.snapshotVersion
    let urgent: Bool
    switch envelope.event {
    case .operation, .currentPathCheck: urgent = true
    case .historyDelta(let delta):
      urgent =
        delta.history.logDeletionGeneration != history.logDeletionGeneration
        || delta.history.trafficDeletionGeneration != history.trafficDeletionGeneration
        || delta.history.journalGeneration != history.journalGeneration
        || delta.history.errorCode != history.errorCode
      history = delta.history
    default: urgent = false
    }
    if urgent {
      await flush(token: token)
      await sink?(.events([envelope.event]))
      return
    }
    // Full metadata travels in every delta. Merge only adjacent notifications,
    // retaining the last optional projection when the next delta omits it.
    if case .historyDelta(var next) = envelope.event,
      let last = pending.last, case .historyDelta(let previous) = last
    {
      if next.traffic == nil { next.traffic = previous.traffic }
      if !next.replaceRecentLogs && previous.replaceRecentLogs {
        next.recentLogs = previous.recentLogs
        next.replaceRecentLogs = true
      }
      pending[pending.count - 1] = .historyDelta(next)
    } else {
      pending.append(envelope.event)
    }
    if pending.count >= 200 {
      await flush(token: token)
    } else if batchTask == nil {
      let sleep = batchSleep
      batchTask = Task { [weak self] in
        do {
          try await sleep(.milliseconds(250))
          try Task.checkCancellation()
          await self?.flush(token: token)
        } catch {}
      }
    }
  }

  private func flush(token: UInt64) async {
    guard token == generation else { return }
    batchTask?.cancel()
    batchTask = nil
    guard !pending.isEmpty else { return }
    let events = pending
    pending.removeAll(keepingCapacity: true)
    await sink?(.events(events))
  }
}
