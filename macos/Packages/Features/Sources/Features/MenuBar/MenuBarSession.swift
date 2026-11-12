import AppKit
import Observation
import PaxCore
import PaxUI

/// One menu interaction. Product facts stay in the existing stores.
@MainActor
final class MenuBarSession {
  struct Row {
    let route: NodeProfile
    var id: String { route.id }
    let checked: Bool
    let enabled: Bool
    let latency: String
    let detail: String
  }
  let routes: RoutesStore
  let connection: ConnectionStore
  let demo: WorkbenchDemoStore?
  var onChange: (() -> Void)?
  private(set) var rows: [Row] = []
  private(set) var isOpen = false
  private var frozen: [NodeProfile] = []

  init(routes: RoutesStore, connection: ConnectionStore, demo: WorkbenchDemoStore? = nil) {
    self.routes = routes
    self.connection = connection
    self.demo = demo
    observe()
  }
  var current: NodeProfile? {
    routes.selectedRoute
  }
  var status: String {
    if !routes.connected || connection.connection != .connected { return "后台不可用" }
    if connection.proxyStarting || connection.connectionTransitioning || routes.selecting {
      return connection.proxyProgressText
    }
    if connection.proxyEnabled {
      if hasProblem { return "连接异常" }
      return connection.currentPathSupported ? connection.currentPathSummary : "代理已开启"
    }
    return "直连"
  }
  var hasProblem: Bool {
    connection.operationError != nil || connection.currentPathCheck.state == "failed"
      || connection.degradedReasons.contains {
        !["protection_unverified", "tls_unverified"].contains($0.code)
      }
      || (connection.degradedReason.map {
        !["protection_unverified", "tls_unverified"].contains($0.code)
      } ?? false)
  }
  var iconNeedsAttention: Bool {
    !routes.connected || connection.connection != .connected || hasProblem
  }
  var iconIsActive: Bool {
    connection.status == .protected || connection.status == .degraded
  }
  var statusImage: NSImage {
    PaxMenuBarIcon.image(isActive: iconIsActive, needsAttention: iconNeedsAttention)
  }
  var canToggle: Bool { demo?.canOperate ?? connection.canRequestProxy(!connection.proxyEnabled) }
  var feedback: String? {
    if let error = routes.selectionError { return ProductErrorPresentation.message(error) }
    if let error = connection.operationError { return ProductErrorPresentation.message(error) }
    return nil
  }

  func open() {
    guard !isOpen else { return }
    isOpen = true
    let selected = current
    let candidates = routes.routes.filter {
      $0.id != selected?.id && selectable($0) && recommendationLatency($0) != nil
    }.sorted { left, right in
      let a = recommendationLatency(left) ?? .max
      let b = recommendationLatency(right) ?? .max
      return a == b ? left.id < right.id : a < b
    }
    frozen = Array(candidates.prefix(3))
    refresh()
  }
  func close() { isOpen = false }
  func select(_ id: String) {
    guard let row = rows.first(where: { $0.id == id }), row.enabled,
      let live = routes.routes.first(where: { $0.id == id }), matches(live, row.route)
    else { return }
    if let demo {
      demo.perform(.selectRoute(id))
    } else {
      Task {
        guard let latest = routes.routes.first(where: { $0.id == id }),
          matches(latest, row.route), selectable(latest)
        else { return }
        await connection.selectRoute(id)
      }
    }
  }
  func refresh() {
    if isOpen {
      // The selected slot is independent of the frozen recommendation slots.
      let selected = current
      let visible = (selected.map { [$0] } ?? []) + frozen
      rows = visible.enumerated().map { index, frozenRoute in
        let live = routes.routes.first { $0.id == frozenRoute.id }
        let unchanged = live.map { matches($0, frozenRoute) } ?? false
        let checked = current?.id == frozenRoute.id
        let route = live ?? frozenRoute
        let presentation = MenuBarLatencyPresentation(route: route)
        let duplicate = checked && selected != nil && index > 0
        let latency =
          !unchanged
          ? "配置已变更"
          : duplicate
            ? "当前线路"
            : presentation.text
        return Row(
          route: frozenRoute, checked: checked && !duplicate,
          enabled: routes.connected && unchanged && !checked
            && selectable(route) && (demo?.canOperate ?? connection.canSelectRoute)
            && !routes.selecting, latency: latency, detail: presentation.detail)
      }
    }
    onChange?()
  }
  var selectedRow: Row? { current == nil ? nil : rows.first }
  var recommendationRows: [Row] { Array(rows.dropFirst(current == nil ? 0 : 1)) }
  private func selectable(_ route: NodeProfile) -> Bool {
    !route.hidden && !route.sourceRemoved && route.tlsPolicy?.needsConfirmation != true
  }
  private func matches(_ left: NodeProfile, _ right: NodeProfile) -> Bool {
    left.id == right.id && left.revision == right.revision
      && left.configRevision == right.configRevision
  }
  private func recommendationLatency(_ route: NodeProfile) -> UInt32? {
    guard let summary = route.testSummary, summary.success else { return nil }
    let matchesVersion =
      summary.testedConfigRevision == 0
      ? summary.testedRevision == route.revision
      : summary.testedConfigRevision == route.configRevision
    guard matchesVersion else { return nil }
    if summary.method == "https204-median-v3" {
      return summary.stableSamples == 3 && summary.stableError.isEmpty ? summary.stableMS : nil
    }
    return summary.durationMS
  }
  private func observe() {
    withObservationTracking {
      _ = routes.routes
      _ = routes.selectedRouteID
      _ = routes.connected
      _ = routes.selecting
      _ = routes.canWrite
      _ = connection.connection
      _ = connection.status
      _ = connection.currentRoute
      _ = connection.canSelectRoute
      _ = connection.proxyEnabled
      _ = connection.canToggleProxy
      _ = connection.currentPathCheck
      _ = connection.operationError
      _ = routes.selectionError
      _ = connection.degradedReasons
      _ = connection.degradedReason
    } onChange: { [weak self] in
      RunLoop.main.perform(inModes: [.default, .eventTracking]) { [weak self] in
        MainActor.assumeIsolated {
          self?.refresh()
          self?.observe()
        }
      }
    }
  }
}
