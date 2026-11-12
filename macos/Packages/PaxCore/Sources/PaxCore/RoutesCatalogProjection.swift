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
}
