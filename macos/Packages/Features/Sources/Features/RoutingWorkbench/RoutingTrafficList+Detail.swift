import PaxCore

extension RoutingTrafficList {
  /// 读取并展示一行的实时详情，含 系统 行。两页共用读取、过期判断与维度映射；
  /// 返回的任务由调用方持有，离开页面时取消。
  func loadDetail(
    _ id: String, visibleOn page: TelemetryPage,
    present: @escaping @MainActor (WorkbenchTrafficDetailController) -> Void,
    fail: @escaping @MainActor (String) -> Void
  ) -> Task<Void, Never>? {
    guard let telemetry = store.telemetry else { return nil }
    let generation = store.liveGeneration
    var query = TrafficDetailQuery()
    query.limit = 20
    query.rowID = id
    query.readToken = store.livePage.readToken
    let dimension: TrafficDimension =
      store.dimension == .system ? .system : store.dimension == .domain ? .domain : .application
    return Task { [weak self] in
      do {
        let detail = try await telemetry.trafficDetail(query)
        guard let self, !Task.isCancelled, telemetry.isShown(page, focus: false),
          self.store.liveGeneration == generation, self.store.livePage.readToken == query.readToken
        else { return }
        present(
          WorkbenchTrafficDetailController(
            detail: detail, dimension: dimension, telemetry: telemetry, tooltip: self.tooltip,
            icons: self.icons))
      } catch {
        if !Task.isCancelled, self?.store.liveGeneration == generation {
          fail(ProductErrorPresentation.display(error))
        }
      }
    }
  }
}
