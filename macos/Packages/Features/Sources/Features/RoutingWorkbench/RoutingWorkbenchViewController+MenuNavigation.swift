import AppKit

extension RoutingWorkbenchViewController {
  func showTrafficOverview() {
    loadViewIfNeeded()
    store.perform(.tab(.traffic))
  }
}
