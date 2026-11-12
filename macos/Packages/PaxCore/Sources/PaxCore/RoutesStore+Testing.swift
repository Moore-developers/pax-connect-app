import Foundation

extension RoutesStore {
  public func cancelMeasurementOperation(_ id: String) async throws {
    try await cancelTestCommand(id)
  }
  /// 一条线路的测试展示状态：未测 / 进行中 / 过期 / 结果。
  public func testState(_ route: NodeProfile) -> RouteTestState {
    if testingRouteIDs.contains(route.id) {
      if let progress = routeMeasurementProgress[route.id], progress.phase != .latency,
        let summary = progress.latencyResult
      {
        return .finished(summary)
      }
      return .running
    }
    guard let summary = route.testSummary else { return .untested }
    let stale =
      summary.testedConfigRevision == 0
      ? summary.testedRevision != route.revision
      : summary.testedConfigRevision != route.configRevision
    if stale { return .stale(summary) }
    return .finished(summary)
  }
  public enum RouteTestState: Equatable {
    case untested
    case running
    case stale(RouteTestSummary)
    case finished(RouteTestSummary)

    public var isUntested: Bool {
      if case .untested = self { return true }
      return false
    }
  }
}
