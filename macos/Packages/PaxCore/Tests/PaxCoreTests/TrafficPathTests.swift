import Foundation
import Testing

@testable import PaxCore

@Suite("流量路径诊断") @MainActor
struct TrafficPathTests {
  @Test("端口、浏览器覆盖和TUN路由不被混成全部接管")
  func evidenceLevels() {
    let route = TrafficPathFinding(kind: "route", subject: "1.1.1.1", state: "pax", value: "utun7")
    let report = TrafficPathDiagnostics(at: Date(), findings: [route], tunConnections: 3)
    #expect(TrafficPathPresentation.summary(report).contains("不代表所有应用"))
    let failure = TrafficPathFinding(
      kind: "proxy_port", subject: "HTTPS", state: "unreachable", value: "127.0.0.1:10808")
    #expect(
      TrafficPathPresentation.summary(.init(at: Date(), findings: [route, failure])).contains(
        "进入 Pax 前失败"))
    #expect(
      TrafficPathPresentation.summary(
        .init(at: Date(), changedDuringCapture: true, findings: [route])
      ).contains("重新检测"))
  }

  @Test("打开只读快照，显式重测才请求补测且原操作仍保留")
  func passiveAndRecheck() async {
    let store = TrafficPathStore(
      load: {
        var value = TrafficPathDiagnostics(at: Date(), findings: [])
        value.operationID = "failed-operation"
        value.attemptSteps = [.init(kind: "core", state: "fail", code: "route_selection_failed")]
        return value
      },
      recheck: {
        var value = TrafficPathDiagnostics(at: Date(), findings: [])
        value.rechecked = true
        value.attemptSteps = [.init(kind: "core", state: "fail", code: "route_selection_failed")]
        value.steps = [.init(kind: "https", state: "pass")]
        return value
      })
    await store.refresh()
    #expect(store.report?.rechecked == false)
    #expect(store.report?.operationID == "failed-operation")
    await store.refresh(recheck: true)
    #expect(store.report?.rechecked == true)
    #expect(store.report?.attemptSteps.first?.state == "fail")
    #expect(store.report?.steps.first?.state == "pass")
  }

  @Test("简洁诊断不重复时间与耗时，失败有原因和一句建议")
  func conciseResults() {
    let pass = ConnectionDiagnosticStep(kind: "node", state: "pass", durationMS: 1364, at: Date())
    let fail = ConnectionDiagnosticStep(
      kind: "core", state: "fail", code: "core_required", durationMS: 1, at: Date())
    let unknown = ConnectionDiagnosticStep(
      kind: "takeover", state: "not_checked", code: "proxy_off")
    #expect(TrafficPathPresentation.conciseStep(pass) == "通过")
    #expect(TrafficPathPresentation.conciseStep(fail).contains("系统授权"))
    #expect(!TrafficPathPresentation.conciseStep(fail).contains("ms"))
    #expect(TrafficPathPresentation.conciseStep(unknown).contains("当前为直连"))
    var report = TrafficPathDiagnostics(at: Date(), findings: [])
    report.steps = [pass, fail, unknown]
    #expect(TrafficPathPresentation.recommendation(report).contains("系统授权"))
    #expect(TrafficPathPresentation.stepDetail(pass).contains("1364 ms"))
  }

  @Test("关闭诊断后迟到结果不回填")
  func lateReply() async {
    let gate = DiagnosticGate()
    let store = TrafficPathStore(load: { await gate.wait() })
    let task = Task { await store.refresh() }
    while !store.busy { await Task.yield() }
    store.clear()
    await gate.finish()
    await task.value
    #expect(store.report == nil)
    #expect(!store.busy)
  }
}

private actor DiagnosticGate {
  private var continuation: CheckedContinuation<TrafficPathDiagnostics, Never>?
  private var finished = false
  func wait() async -> TrafficPathDiagnostics {
    if finished { return .init(at: Date(), findings: []) }
    return await withCheckedContinuation { continuation = $0 }
  }
  func finish() {
    finished = true
    continuation?.resume(returning: .init(at: Date(), findings: []))
    continuation = nil
  }
}
