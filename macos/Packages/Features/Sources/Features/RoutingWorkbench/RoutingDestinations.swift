import PaxCore

@MainActor
extension RoutingWorkbenchStore {
  func bind(_ telemetry: TelemetryStore, _ connection: ConnectionStore?) {
    self.telemetry = telemetry
    if let connection, let routes = connection.routesStore {
      routeSelectionStore = RouteWorkbenchStore(routes: routes, connection: connection)
    }
  }
  func destinationName(_ destination: Destination, context: PresentationContext = .chinese)
    -> String
  {
    switch destination {
    case .followDefault: return context.text("routing_copy.a44f9f9fca")
    case .direct: return context.text("routing_copy.b06325c566")
    case .global: return context.text("routing_copy.1466c1f26f")
    case .block: return context.text("routing_copy.6d363f5a1d")
    case .route(let id):
      return availableRoutes.first { $0.id == id }?.name ?? context.text("routing_copy.3ba04b1a53")
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
