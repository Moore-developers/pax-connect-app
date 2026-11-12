import Foundation
import PaxClient

// 开发用探针：直接连 daemon（不经 App），打印握手结果与前几条事件。
//
//   swift run PaxProbe [socket 路径]
//
// 用途：daemon 单独调试（sudo ./pax-connectd --socket /tmp/pax.sock）时验证协议链路，
// 也是 T1「App ↔ daemon 能通」这条 DoD 在 App 之外的独立证据。

let socketPath =
  CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : DaemonConnection.defaultSocketPath
print("probe: socket=\(socketPath)")

let connection = DaemonConnection(socketPath: socketPath)

do {
  let handshake = try await connection.handshake()
  print(
    "handshake: daemon=\(handshake.daemonVersion) protocol=\(handshake.protocolVersion) snapshotSeq=\(handshake.snapshotSeq)"
  )
  print("serviceFingerprint: \(handshake.serviceFingerprint)")
  print("RESULT PASS handshake")
} catch {
  print("RESULT FAIL handshake: \(error)")
  exit(1)
}

let counter = EventCounter()
do {
  try await withThrowingTaskGroup(of: Void.self) { group in
    group.addTask {
      try await connection.run(
        onReady: { _, snapshot in await counter.record(.snapshot(snapshot)) },
        onEvent: { envelope in await counter.record(envelope.event) }
      )
    }
    group.addTask {
      let seconds =
        ProcessInfo.processInfo.environment["PAX_PROBE_SECONDS"].flatMap(Double.init) ?? 3
      try await Task.sleep(for: .seconds(seconds))
    }
    defer { group.cancelAll() }
    try await group.next()

  }
} catch is CancellationError {
  // 预期路径：超时取消
} catch {
  print("RESULT FAIL watch: \(error)")
  exit(1)
}

let counts = await counter.counts
print("events: snapshot=\(counts.snapshots) other=\(counts.others)")
if counts.snapshots > 0 {
  print("RESULT PASS watch: 收到快照事件")
} else {
  print("RESULT FAIL watch: 未收到任何快照事件")
  exit(1)
}

actor EventCounter {
  private(set) var snapshots = 0
  private(set) var others = 0

  var counts: (snapshots: Int, others: Int) { (snapshots, others) }

  func record(_ event: DaemonEvent) {
    switch event {
    case .snapshot(let snapshot):
      snapshots += 1
      print(
        "  · snapshot v\(snapshot.snapshotVersion) status=\(snapshot.status) routes=\(snapshot.routes.count)"
      )
    default:
      others += 1
    }
  }
}
