import Foundation
import PaxClient
import PaxCore

/// 线路详情里「测试 CF 入口」的会话状态：只测这条线路已配置的 CF IP，结果不依赖 CF 工作台选择。
@MainActor @Observable
final class RouteCFEntryTests {
  private(set) var running: Set<String> = []
  private(set) var results: [String: RouteWorkbenchStore.Measurement] = [:]
  private var tasks: [String: Task<Void, Never>] = [:]

  func start(_ id: String, ip: String, routes: RoutesStore) {
    guard !running.contains(id) else { return }
    running.insert(id)
    tasks[id] = Task { [weak self] in
      let value = await Self.measure(ip: ip, routes: routes)
      guard let self, !Task.isCancelled else { return }
      results[id] = value
      running.remove(id)
      tasks[id] = nil
    }
  }

  /// 任何失败都落到可见的失败文案，不静默。
  private static func measure(ip: String, routes: RoutesStore) async
    -> RouteWorkbenchStore.Measurement
  {
    var value = RouteWorkbenchStore.Measurement(time: MeasurementTimestamp.text(Date()))
    do {
      guard
        case .operation(let operation) = try await routes.measurement(.cfTask("latency_one", [ip]))
      else {
        value.failure = "CF 入口测试未完成"
        return value
      }
      var result = routes.measurementResult(operation)
      for _ in 0..<120 where result == nil {
        try await Task.sleep(for: .milliseconds(500))
        result = routes.measurementResult(operation)
      }
      guard let result else {
        value.failure = "CF 入口测试超时，未收到后台结果"
        return value
      }
      if !result.ok {
        value.failure = result.error.map(ProductErrorPresentation.message) ?? "CF 入口测试失败"
        return value
      }
      let query = CFWorkspaceQuery(routeID: "", revision: 0, document: "")
      guard case .workspace(let workspace) = try await routes.measurement(.cfWorkspace(query)),
        let record = workspace.records.first(where: {
          $0.ip == ip && $0.network == workspace.currentNetwork
        })
      else {
        value.failure = "测试已完成，但未读到本次结果（测试期间网络可能变化），请重试"
        return value
      }
      if record.tcp.errorCode.isEmpty, record.tcp.value > 0 {
        value.value = record.tcp.value
        value.time = MeasurementTimestamp.text(record.tcp.measuredAt ?? Date())
        value.samples = [record.tcp.value]
      } else {
        value.failure = ProductErrorPresentation.message(
          ProductError(
            code: record.tcp.errorCode.isEmpty ? "cf_tcp_unreachable" : record.tcp.errorCode))
      }
    } catch is CancellationError {
      value.failure = "测试已取消"
    } catch {
      value.failure = ProductErrorPresentation.display(error)
    }
    return value
  }
}

extension RouteWorkbenchStore {
  /// 需要后台连接与 `routes.cf.single`；链式线路没有单一 CF 入口。
  func canTestCF(_ id: String) -> Bool {
    guard let routes, hasCFSingleTests, routes.connected, connectionStore?.serviceUpdating == false,
      let node = node(id), node.chain == nil, node.cfEntry
    else { return false }
    return !isTestingCF(id)
  }
  func isTestingCF(_ id: String) -> Bool { cfEntryTests.running.contains(id) }
  func testCFEntry(_ id: String) {
    guard let routes, let node = node(id), node.cfEntry, node.chain == nil else { return }
    guard hasCFSingleTests, routes.connected else {
      message = "当前后台不支持测试 CF 入口，请更新后台"
      return
    }
    guard canTestCF(id) else { return }
    message = ""
    cfEntryTests.start(id, ip: node.server, routes: routes)
  }
  /// 把 CF 入口测试结果并入详情展示；未测试时保持原值。
  func withCFEntry(_ value: Presentation, id: String) -> Presentation {
    var out = value
    if isTestingCF(id) {
      out.cfLatency.running = true
    } else if let result = cfEntryTests.results[id], result.failure != nil || result.value != nil {
      out.cfLatency = result
    }
    return out
  }
}

extension RouteWorkbenchStore {
  func fetchCFCandidates() async throws -> [Candidate] {
    guard let routes else { return [] }
    guard case .candidates(let values) = try await routes.measurement(.candidates) else {
      return []
    }
    return values.map { value in
      Candidate(
        ip: value.ip,
        measurement: Measurement(
          value: value.latencyMS > 0 ? Double(value.latencyMS) : nil,
          failure: value.error.map(ProductErrorPresentation.message),
          time: value.at.map { MeasurementTimestamp.text($0) }), network: value.network,
        validUntil: value.at?.addingTimeInterval(300))
    }
  }
  func validateCF(_ draft: Draft, ip: String) async throws -> Candidate {
    guard let routes else { throw EditError("CF 测试尚未接入后台") }
    guard
      case .candidate(let value) = try await routes.measurement(
        draft.chainContext.isEmpty
          ? .validateCF(draft.id, draft.revision, draft.json, ip)
          : .validateChainCF(
            draft.id, draft.revision, draft.json, ip, draft.chainContext, draft.chainRevision))
    else { throw EditError("CF 测试未完成") }
    return Candidate(
      ip: value.ip,
      measurement: Measurement(
        value: value.latencyMS > 0 ? Double(value.latencyMS) : nil,
        failure: value.error.map(ProductErrorPresentation.message),
        time: value.at.map { MeasurementTimestamp.text($0) }), network: value.network,
      validUntil: value.at?.addingTimeInterval(300))
  }
}
