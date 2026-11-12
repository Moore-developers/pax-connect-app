import Foundation
import PaxCore

extension RouteDetailController {
  /// 随流量信号（最小 60s）重读使用记录；进页那次由 PaxRefresh 触发，离页在 viewDidDisappear 停。
  func startUsageRefresh() {
    guard store.isLive, let refresh = store.refresh else { return }
    refresh.bind(.routeDetail) { [weak self] in
      guard let self else { return }
      self.usageRefreshTask?.cancel()
      self.usageRefreshTask = Task { await self.store.refreshRouteUsage(self.id) }
    }
    refresh.pageDidAppear(.routeDetail)
  }
  @objc func reloadHistory() {
    Task { [store, id] in await store.loadMeasurementHistory(id) }
  }
  @objc func moreUsage() {
    Task { [store, id] in await store.refreshRouteUsage(id, more: true) }
  }
  @objc func moreHistory() {
    Task { [store, id] in await store.loadMeasurementHistory(id, more: true) }
  }
}
