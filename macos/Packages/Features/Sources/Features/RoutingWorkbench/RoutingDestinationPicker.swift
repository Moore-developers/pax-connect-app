import AppKit
import Observation
import PaxCore
import PaxUI

/// Reads the route page's catalog, groups and existing measurements. It never
/// invokes route selection, import, subscription refresh, or measurement intents.
@MainActor
final class RoutingDestinationPicker: NSStackView, NSSearchFieldDelegate {
  let context: PresentationContext

  private let browser: RouteWorkbenchStore?
  private let modes: PaxTabs
  private let groups = PaxTabBar()
  private let search = PaxRouteSearchField()
  private let status = PaxText.label(
    "", font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  private let table: PaxDataTable
  private let onChange: () -> Void
  private let onResize: () -> Void
  private var selectedRoute: String?
  private var height: NSLayoutConstraint?
  private var observationGeneration: UInt64 = 0

  var selection: RoutingWorkbenchStore.Destination? {
    switch modes.selectedIndex {
    case 0:
      guard let id = selectedRoute, let browser,
        browser.nodes.contains(where: { $0.id == id && !$0.hidden }),
        browser.info(id).latency.failure == nil
      else { return nil }
      return .route(id)
    case 1: return .direct
    default: return .block
    }
  }

  init(
    browser: RouteWorkbenchStore?, current: RoutingWorkbenchStore.Destination,
    requiresRoute: Bool = false,
    onChange: @escaping () -> Void, onResize: @escaping () -> Void,
    context: PresentationContext = .chinese
  ) {
    self.context = context

    self.browser = browser
    self.onChange = onChange
    self.onResize = onResize
    modes = PaxTabs([
      context.text("network_controls.bd170d0c23"), context.text("network_controls.b06325c566"),
      context.text("network_controls.6d363f5a1d"),
    ])
    table = PaxDataTable(
      columns: [
        .init(
          context.text("network_controls.223b24b821"), width: 0,
          growth: PaxTheme.Routing.destinationColumns[0], symbol: "network",
          sortable: false),
        .init(
          context.text("network_controls.18045b8c40"), width: 0,
          growth: PaxTheme.Routing.destinationColumns[1], symbol: "timer",
          sortable: false),
        .init(
          context.text("network_controls.56f0864c08"), width: 0,
          growth: PaxTheme.Routing.destinationColumns[2], symbol: "arrow.down",
          sortable: false),
      ],
      metrics: .init(
        rowHeight: PaxTheme.Table.memberRow, headerHeight: PaxTheme.Table.compactHeader),
      selection: .single)
    super.init(frame: .zero)
    orientation = .vertical
    alignment = .leading
    spacing = PaxTheme.Spacing.tight
    switch current {
    case .followDefault, .global: modes.selectedIndex = 0
    case .route(let id):
      modes.selectedIndex = 0
      selectedRoute = id
    case .direct: modes.selectedIndex = 1
    case .block: modes.selectedIndex = 2
    }
    if requiresRoute { modes.selectedIndex = 0 }
    if let browser {
      let group = selectedRoute.flatMap { id in
        browser.displayGroups.first { $0.members.contains(id) }?.id
      }
      browser.perform(.group(group ?? "default"))
    }
    modes.setAccessibilityIdentifier("routing-destination-mode")
    modes.onSelect = { [weak self] _ in
      self?.render()
      self?.onChange()
    }
    groups.onSelect = { [weak self] id in
      self?.browser?.perform(.group(id))
      self?.render()
    }
    search.placeholderString = context.text("network_controls.8653cc47e1")
    search.stringValue = browser?.search ?? ""
    search.delegate = self
    search.setAccessibilityIdentifier("routing-destination-search")
    table.setTableIdentifier("routing-destination-routes")
    table.cellProvider = { [weak self] id, column in self?.cell(id, column: column) }
    table.onSelect = { [weak self] id in
      guard let self, let id, browser?.visible.contains(where: { $0.id == id }) == true,
        browser?.info(id).latency.failure == nil
      else {
        return
      }
      selectedRoute = id
      updateStatus()
      onChange()
    }
    let tools = NSStackView(views: [modes, groups, search])
    tools.orientation = .horizontal
    tools.alignment = .centerY
    tools.spacing = PaxTheme.Spacing.tight
    modes.setContentHuggingPriority(.required, for: .horizontal)
    groups.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    groups.widthAnchor.constraint(
      greaterThanOrEqualToConstant: PaxTheme.Routing.destinationGroupMinimum
    ).isActive = true
    search.widthAnchor.constraint(equalToConstant: PaxTheme.Routing.destinationSearchWidth)
      .isActive = true
    for view in [tools, table, status] {
      addArrangedSubview(view)
      view.widthAnchor.constraint(equalTo: widthAnchor).isActive = true
    }
    render()
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }
  func controlTextDidChange(_ notification: Notification) {
    browser?.perform(.search(search.stringValue))
    render()
  }
  private func render() {
    observationGeneration &+= 1
    let generation = observationGeneration
    guard let browser else {
      for view in [groups, search, table] { view.isHidden = true }
      status.stringValue =
        modes.selectedIndex == 0
        ? context.text("network_controls.38fd56e3a3") : context.text("network_controls.3ec030ac79")
      onResize()
      return
    }
    withObservationTracking {
      let choosing = modes.selectedIndex == 0
      for view in [groups, search, table] { view.isHidden = !choosing }
      let available = browser.nodes.filter {
        !$0.hidden && browser.info($0.id).latency.failure == nil
      }
      let eligible = Set(available.map(\.id))
      groups.apply(
        [
          .init(
            id: "default", title: context.text("network_controls.844b8cc8df"),
            count: browser.defaultMembers.intersection(eligible).count)
        ]
          + browser.displayGroups.map {
            .init(id: $0.id, title: $0.name, count: $0.members.intersection(eligible).count)
          },
        selected: browser.selectedGroup)
      let rows = browser.visible.filter { eligible.contains($0.id) }
      height?.isActive = false
      height = table.heightAnchor.constraint(
        equalToConstant:
          PaxTheme.Table.compactHeader + CGFloat(max(1, rows.count)) * PaxTheme.Table.memberRow)
      height?.isActive = true
      // Read measurement revisions inside tracking so asynchronous route results
      // update the choice without polling or automatically starting a new test.
      for row in rows { _ = browser.info(row.id) }
      table.apply(
        rows: rows.map { .init(id: $0.id, values: []) },
        emptyMessage: context.text("network_controls.a058369d32"), selectedID: selectedRoute)
      updateStatus()
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in
        guard let self, observationGeneration == generation else { return }
        render()
        onChange()
      }
    }
    onResize()
  }
  private func updateStatus() {
    switch modes.selectedIndex {
    case 0:
      if let id = selectedRoute, let browser, selection != nil {
        status.stringValue =
          context.text("network_controls.d4102e5d06")
          + (browser.nodes.first { $0.id == id }?.name ?? id)
      } else if selectedRoute != nil {
        status.stringValue = context.text("network_controls.1f6e6cfe35")
      } else {
        status.stringValue = context.text("network_controls.ea8550a7c0")
      }
    case 1: status.stringValue = context.text("network_controls.d55eb98f86")
    default: status.stringValue = context.text("network_controls.eebf793db3")
    }
  }

  private func cell(_ id: String, column: Int) -> NSView? {
    guard let browser, let node = browser.nodes.first(where: { $0.id == id }) else { return nil }
    let info = browser.info(id, context: context)
    let value =
      column == 0
      ? node.name
      : column == 1
        ? info.latency.display(.latency, context: context)
        : info.speed.display(.speed, context: context)
    let label = PaxText.label(value, font: PaxTheme.Typography.caption())
    label.lineBreakMode = .byTruncatingTail
    label.toolTip =
      column == 0
      ? node.name + " · " + node.protocolName
      : value + "\n"
        + ((column == 1 ? info.latency.time : info.speed.time)
          ?? context.text("network_controls.d5c29499c6"))
    if column == 1 { label.textColor = info.latency.latencyColor }
    return label
  }
}
