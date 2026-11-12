import PaxCore

@MainActor
extension RoutingWorkbenchStore {
  func bind(_ telemetry: TelemetryStore, _ connection: ConnectionStore?) {
    self.telemetry = telemetry
    if let connection, let routes = connection.routesStore {
      routeSelectionStore = RouteWorkbenchStore(routes: routes, connection: connection)
    }
  }
  func destinationName(_ destination: Destination) -> String {
    switch destination {
    case .followDefault: return "跟随默认"
    case .direct: return "直连"
    case .global: return "当前线路"
    case .block: return "阻断"
    case .route(let id): return availableRoutes.first { $0.id == id }?.name ?? "指定线路不可用（保留指定）"
    }
  }
  var destinations: [Destination] {
    [.direct, .global] + availableRoutes.map { .route($0.id) } + [.block]
  }
  var availableRoutes: [NodeProfile] {
    if let routeSelectionStore { return routeSelectionStore.nodes.filter { !$0.hidden } }
    if telemetry != nil { return libraryRoutes }
    return backendAvailable
      ? (scene == .unavailableRoute ? Array(session.routes.dropFirst(2)) : session.routes) : []
  }
  var priorityDestinations: [Destination] {
    [.followDefault, .direct] + availableRoutes.map { .route($0.id) } + [.block]
  }
  func destinationOptions(current: Destination, priority: Bool = false) -> [Destination] {
    var values = priority ? priorityDestinations : destinations
    if !values.contains(current) { values.append(current) }
    return values
  }
}
