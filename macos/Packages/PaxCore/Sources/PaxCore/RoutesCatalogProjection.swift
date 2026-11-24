import Foundation
import PaxClient

extension RoutesStore {
  public var visibleRoutes: [NodeProfile] {
    routes.filter { route in
      matchesSource(route) && matchesSearch(route)
    }
  }
  private func matchesSource(_ route: NodeProfile) -> Bool {
    switch sourceFilter {
    case .all: return !route.hidden
    case .local: return !route.hidden && route.sourceID.isEmpty
    case .source(let id): return !route.hidden && route.sourceID == id
    case .hidden: return route.hidden
    }
  }
  private func matchesSearchText(_ route: NodeProfile) -> String {
    route.name + " " + route.protocolName + " " + route.server
  }
  private func matchesSearch(_ route: NodeProfile) -> Bool {
    filter.isEmpty
      || matchesSearchText(route).localizedCaseInsensitiveContains(filter)
  }
  func refreshDoneMessage(_ sourceID: String) -> String {
    guard let summary = subscriptions.first(where: { $0.id == sourceID }) else { return "订阅已刷新" }
    let report = summary.lastRefresh
    if report.partial {
      return "部分刷新失败：保留 \(report.kept) 条未确认线路"
    }
    if report.removed > 0 {
      return "刷新完成：新增 \(report.added)，来源移除 \(report.removed)"
    }
    return "订阅已刷新"
  }
}
