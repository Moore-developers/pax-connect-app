import Foundation

/// 单线路出口查询的 IP 变化对比：记录发起前摘要，完成时生成消息。
struct RouteExitCompare {
  struct Baseline: Equatable {
    let routeID: String
    let before: RouteExitSummary?
  }
  static let doneText = "IP 地区查询完成"
  private var baselines: [String: Baseline] = [:]
  /// 操作结果先于含新摘要的快照到达时，等快照再补全消息。
  private var awaiting: Baseline?

  static func message(before: RouteExitSummary?, after: RouteExitSummary?) -> String? {
    guard let after, after.at > (before?.at ?? .distantPast) else { return nil }
    guard after.success, !after.ip.isEmpty else { return doneText }
    guard let old = before?.ip, !old.isEmpty else { return doneText }
    let change = old == after.ip ? " · IP 未变化（\(old)）" : " · IP 已变为 \(after.ip)"
    return doneText + change
  }

  mutating func reset() {
    baselines = [:]
    awaiting = nil
  }
  /// 发起查询前取基线；单线路查询才有基线，同时清掉上一次的等待。
  mutating func begin(_ intent: RoutesStore.Intent, _ routes: [NodeProfile]) -> Baseline? {
    awaiting = nil
    guard case .queryExit(let route) = intent else { return nil }
    return .init(routeID: route.id, before: routes.first { $0.id == route.id }?.exitSummary)
  }
  mutating func record(_ id: String, _ baseline: Baseline?) { baselines[id] = baseline }

  /// 操作结果到达（立即或稍后）时生成提示；`baseline` 为空则按操作号取已记录的基线。
  mutating func finish(
    _ result: OperationResult, _ baseline: Baseline?, _ routes: [NodeProfile]
  ) -> String {
    let baseline = baseline ?? baselines.removeValue(forKey: result.operationID)
    guard result.ok else {
      return result.error.map(ProductErrorPresentation.message) ?? "IP 地区查询失败"
    }
    guard let baseline else { return Self.doneText }
    let after = routes.first { $0.id == baseline.routeID }?.exitSummary
    if let text = Self.message(before: baseline.before, after: after) { return text }
    awaiting = baseline
    return Self.doneText
  }

  /// 新快照到达后，若在等待对比且已有新摘要，补全仍是默认文案的消息。
  mutating func resolve(_ routes: [NodeProfile], _ message: inout String) {
    guard let waiting = awaiting,
      let text = Self.message(
        before: waiting.before, after: routes.first { $0.id == waiting.routeID }?.exitSummary)
    else { return }
    awaiting = nil
    if message == Self.doneText { message = text }
  }
}
