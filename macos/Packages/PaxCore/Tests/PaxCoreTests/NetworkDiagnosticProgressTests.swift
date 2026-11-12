import Foundation
import PaxProto
import Testing

@testable import PaxClient
@testable import PaxCore

@MainActor @Suite("诊断实际进度") struct NetworkDiagnosticProgressTests {
  @Test("线路请求未完成时显示实际计数，停止后迟到结果不覆盖状态")
  func liveProgressAndCancellation() async {
    actor Gate {
      var pending: CheckedContinuation<NetworkDiagnosticReport, Never>?
      func run() async -> NetworkDiagnosticReport {
        await withCheckedContinuation { pending = $0 }
      }
      func release() {
        pending?.resume(returning: NetworkDiagnosticReport(.init()))
        pending = nil
      }
      var started: Bool { pending != nil }
    }
    let gate = Gate()
    let store = NetworkDiagnosticStore(
      readProgress: { id in
        NetworkDiagnosticProgress(
          .with {
            $0.operationID = id
            $0.probesCompleted = 5
            $0.probesTotal = 22
            $0.exitState = "complete"
            $0.sourcesState = "running"
          })
      }, run: { _, _ in await gate.run() })
    store.startAll()
    for _ in 0..<500 {
      if store.progress != nil, await gate.started { break }
      await Task.yield()
    }
    #expect(store.busy)
    #expect(store.report == nil)
    #expect(store.activities[0].detail.contains("5/22"))
    #expect(store.activities[0].detail.contains("IP 资料查询中"))
    store.cancel()
    await gate.release()
    for _ in 0..<20 { await Task.yield() }
    #expect(store.runState == .stopped)
    #expect(store.activities[0].state == .stopped)
    #expect(store.report == nil)
    store.clear()
  }

  @Test("环境采样返回但资料未结束时显示阶段，不借用旧结果作为完成")
  func environmentPhases() async {
    let sample = EnvironmentDiagnosticReport(
      .with {
        $0.sessionID = "round"
        $0.mac.checks = [
          .with {
            $0.kind = "ipv4"
            $0.state = "observed"
          }
        ]
      })
    let store = EnvironmentDiagnosticStore(request: { _, _ in sample }, openBrowser: { _ in true })
    store.start()
    for _ in 0..<200 where store.progressReport == nil { await Task.yield() }
    #expect(store.activity(browser: false).state == .running)
    #expect(store.activity(browser: false).detail.contains("正在查询 IP 资料"))
    #expect(store.activity(browser: true).detail.contains("等待浏览器"))
    #expect(!store.activity(browser: false).settled)
    store.cancel()
    #expect(store.activity(browser: true).state == .stopped)
    store.clear()
  }
}
