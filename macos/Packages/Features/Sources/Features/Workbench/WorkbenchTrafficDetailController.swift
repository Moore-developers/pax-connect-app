import AppKit
import Observation
import PaxCore
import PaxUI

/// 概览与流量页共用的只读详情；页面只提供快照及可选的规则定位动作。
@MainActor
final class WorkbenchTrafficDetailController: WorkbenchModalController {
  var snapshot: WorkbenchTrafficDetail
  private let tooltip: PaxChartTooltipCoordinator
  private var liveTelemetry: TelemetryStore?
  private let icons: WorkbenchObjectIcons?
  private var session: UInt64 = 0
  private var pagingUnavailable: String?
  private var detailTask: Task<Void, Never>?
  private let onLocate: ((String) -> String?)?
  private let tabs = PaxTabBar(style: .detail)
  private(set) var selectedTab = "overview"
  private(set) var relatedID: String?
  private var connectionListState = WorkbenchConnectionListState()
  private(set) var selectedConnectionID: String?
  override var maximumPresentationWidth: CGFloat? { PaxTheme.TrafficDetail.size.width }

  init(
    snapshot: WorkbenchTrafficDetail, tooltip: PaxChartTooltipCoordinator,
    icons: WorkbenchObjectIcons? = nil, onLocate: ((String) -> String?)? = nil
  ) {
    self.snapshot = snapshot
    self.icons = icons
    self.tooltip = tooltip
    self.onLocate = onLocate
    super.init(
      title: snapshot.name, subtitle: snapshot.identity, size: PaxTheme.TrafficDetail.size,
      style: .detail, symbol: snapshot.symbol)
  }
  convenience init(
    detail: TrafficDetail, dimension: TrafficDimension, telemetry: TelemetryStore,
    tooltip: PaxChartTooltipCoordinator, icons: WorkbenchObjectIcons? = nil
  ) {
    self.init(
      snapshot: WorkbenchTrafficDetail(detail: detail, dimension: dimension), tooltip: tooltip,
      icons: icons)
    liveTelemetry = telemetry
    session = telemetry.sessionGeneration
  }
  @objc private func loadMoreConnections() {
    guard pagingUnavailable == nil, let telemetry = liveTelemetry, let current = snapshot.aggregate,
      !current.nextCursor.isEmpty
    else { return }
    var q = TrafficDetailQuery()
    q.limit = 20
    q.rowID = current.summary.id
    q.readToken = current.readToken
    q.cursor = current.nextCursor
    q.dnsGroupID = current.dnsGroupID
    detailTask = Task { [weak self] in
      do {
        var next = try await telemetry.trafficDetail(q)
        guard let self, !Task.isCancelled else { return }
        next.items =
          current.items
          + next.items.filter { item in !current.items.contains(where: { $0.key == item.key }) }
        next.dnsGroups =
          current.dnsGroups
          + next.dnsGroups.filter { group in !current.dnsGroups.contains { $0.id == group.id } }
        self.snapshot = WorkbenchTrafficDetail(
          detail: next, dimension: self.snapshot.dimension, context: self.interfaceCopy.context)
        self.render()
      } catch {
        if !Task.isCancelled { self?.showReadError(error) }
      }
    }
  }
  func showDNSGroup(_ id: String?) {
    guard pagingUnavailable == nil, let telemetry = liveTelemetry, let current = snapshot.aggregate
    else { return }
    detailTask?.cancel()
    var query = TrafficDetailQuery()
    query.rowID = current.summary.id
    query.readToken = current.readToken
    query.dnsGroupID = id ?? ""
    detailTask = Task { [weak self] in
      do {
        let next = try await telemetry.trafficDetail(query)
        guard let self, !Task.isCancelled else { return }
        self.snapshot = WorkbenchTrafficDetail(
          detail: next, dimension: self.snapshot.dimension, context: self.interfaceCopy.context)
        self.selectedTab = "connections"
        self.render()
      } catch {
        if !Task.isCancelled {
          self?.showReadError(error)
        }
      }
    }
  }
  override func loadView() {
    super.loadView()
    if let detail = snapshot.aggregate {
      snapshot = WorkbenchTrafficDetail(
        detail: detail, dimension: snapshot.dimension, context: interfaceCopy.context)
    } else {
      snapshot.context = interfaceCopy.context
    }
    pane.setAccessibilityIdentifier("traffic-detail")
    if let icons {
      let object = snapshot.iconObject
      pane.replaceIcon(
        icons.identityView(
          kind: object.kind, id: object.id, name: snapshot.name, content: NSView(),
          size: PaxTheme.Routes.headerIcon, gap: 0, tint: PaxTheme.Color.textSecondary))
    }
    pane.identity.toolTip = snapshot.name + "\n" + snapshot.identity
    let context = WorkbenchTrafficExitTags(snapshot: snapshot)
    context.isHidden = snapshot.aggregate?.summary.kind == "dns"
    pane.header.addArrangedSubview(context)
    pane.header.addArrangedSubview(tabs)
    context.widthAnchor.constraint(equalTo: pane.header.widthAnchor).isActive = true
    tabs.widthAnchor.constraint(equalTo: pane.header.widthAnchor).isActive = true
    tabs.onSelect = { [weak self] in self?.selectTab($0) }
    render()
    observeSession()
  }
  // Reading owns the opening snapshot. Retention and new records never replace or close it.
  // A new daemon cannot serve the old token, but already loaded content remains readable.
  private func observeSession() {
    guard let telemetry = liveTelemetry else { return }
    withObservationTracking {
      if session != telemetry.sessionGeneration {
        detailTask?.cancel()
        pagingUnavailable = self.interfaceCopy.text("interface_detail.701e08d7ec")
        updateActions()
        return
      }
    } onChange: { [weak self] in
      Task { @MainActor in self?.observeSession() }
    }
  }
  private func showReadError(_ error: any Error) {
    if let product = error as? ProductError,
      ["history_cursor_expired", "history_expired", "history_cleared"].contains(product.code)
    {
      pagingUnavailable = self.interfaceCopy.text("interface_detail.bdd9bd695f")
      updateActions()
    } else {
      pane.status.stringValue = ProductErrorPresentation.display(
        error, context: self.interfaceCopy.context)
    }
  }
  func selectTab(_ tab: String) {
    selectedTab = tab == "connections" ? tab : "overview"
    relatedID = nil
    selectedConnectionID = nil
    render()
  }
  func selectRelated(_ id: String) {
    guard snapshot.related.contains(where: { $0.id == id }) else { return }
    selectedTab = "connections"
    relatedID = id
    selectedConnectionID = nil
    render()
  }
  var visibleConnections: [WorkbenchTrafficRecord] {
    let ids = snapshot.related.first { $0.id == relatedID }?.connectionIDs
    return snapshot.connections.filter { ids?.contains($0.id) ?? true }.sorted {
      $0.started == $1.started ? $0.id < $1.id : $0.started > $1.started
    }
  }
  private func render() {
    tooltip.close()
    pane.clear()
    let items = [
      PaxTabBar.Item(id: "overview", title: self.interfaceCopy.text("ai_page.overview")),
      .init(
        id: "connections",
        title: snapshot.aggregate?.summary.kind == "dns"
          ? self.interfaceCopy.text("interface_detail.66966ac9ee")
          : self.interfaceCopy.text("interface_detail.2b39320577"),
        count: snapshot.aggregate?.summary.kind == "dns"
          ? snapshot.connectionCount : snapshot.retainedConnectionCount),
    ]
    tabs.apply(items, selected: selectedTab)
    if selectedTab == "overview" {
      let range =
        TrafficFormatting.dateMinute(snapshot.lowerBound) + " — "
        + TrafficFormatting.dateMinute(snapshot.capturedAt)
      pane.add(PaxRouteSectionHeading(snapshot.periodTitle, symbol: "clock", detail: range))
    }
    if let summary = snapshot.aggregate?.summary, TrafficFormatting.isIPv6LimitedPath(summary) {
      pane.add(
        PaxNotice(
          title: self.interfaceCopy.text("action.traffic.ipv6_limit"),
          detail: TrafficRuleFormatting.ipv6Limitation, tone: .neutral))
    }
    if !snapshot.quality.isEmpty {
      pane.add(
        PaxNotice(
          title: self.interfaceCopy.text("action.logs.recorded_status"), detail: snapshot.quality,
          tone: .warning))
    }
    if snapshot.aggregate?.summary.kind == "dns" {
      pane.add(
        dnsTrafficContent(
          snapshot, overview: selectedTab == "overview", tooltip: tooltip,
          onGroup: { [weak self] in self?.showDNSGroup($0) }))
    } else if selectedTab == "overview" {
      overview()
    } else {
      connections()
    }
    updateActions()
  }
  private func updateActions() {
    pane.status.stringValue =
      pagingUnavailable
      ?? self.interfaceCopy.text("dashboard_value.as_of")
      + TrafficFormatting.dateMinute(snapshot.capturedAt)
      + self.interfaceCopy.text("interface_detail.61167ae318")
    var actions = [button(self.interfaceCopy.text("reprice.finish"), #selector(close))]
    if snapshot.aggregate?.nextCursor.isEmpty == false
      && (snapshot.aggregate?.summary.kind != "dns" || selectedTab == "connections")
    {
      let more = button(
        snapshot.aggregate?.summary.kind == "dns"
          ? self.interfaceCopy.text("interface_detail.4afd712c66")
          : self.interfaceCopy.text("interface_detail.0824383726"),
        #selector(loadMoreConnections))
      more.isEnabled = pagingUnavailable == nil
      actions.insert(more, at: 0)
    }
    pane.setActions(actions)
  }
  private func overview() {
    if let source = snapshot.aggregate?.summary.sourceEvidence, !source.isEmpty {
      pane.add(
        PaxText.label(source, font: PaxTheme.Typography.caption(), secondary: true, wrapping: true))
    }
    let metrics = PaxRouteMetricRow([
      byteMetric(
        self.interfaceCopy.text("dashboard.upload"), symbol: "arrow.up.right",
        bytes: snapshot.upload, color: PaxTheme.DashboardColor.amber
      ),
      byteMetric(
        self.interfaceCopy.text("dashboard.download"), symbol: "arrow.down.left",
        bytes: snapshot.download,
        color: PaxTheme.DashboardColor.green),
      PaxRouteMetric(
        title: snapshot.aggregate?.aggregateOnly == true
          || ["hour", "day"].contains(snapshot.aggregate?.window ?? "")
          ? self.interfaceCopy.text("interface_detail.2cb204a578")
          : self.interfaceCopy.text("interface_detail.80a532ef96"),
        symbol: "network",
        value: String(snapshot.connectionCount),
        unit: self.interfaceCopy.text("interface_detail.f004f1d84c"),
        color: PaxTheme.Color.textSecondary, style: .usage),
    ])
    let divider = NSBox()
    divider.boxType = .separator
    let summary = vertical([metrics, divider])
    summary.addArrangedSubview(
      PaxRouteSectionHeading(
        snapshot.aggregate?.summary.kind == "dns"
          ? self.interfaceCopy.text("log_detail.resolution_latency")
          : self.interfaceCopy.text("log_detail.response_latency"), symbol: "timer",
        detail: snapshot.latencyRangeText))
    let latency = snapshot.latencyView(tooltip: tooltip)
    summary.addArrangedSubview(latency)
    summary.addArrangedSubview(
      PaxText.label(
        snapshot.sampleSummary, font: PaxTheme.Typography.caption(), secondary: true, wrapping: true
      ))
    for view in summary.arrangedSubviews {
      view.widthAnchor.constraint(equalTo: summary.widthAnchor).isActive = true
    }
    let card = PaxCard(style: .routesSection)
    card.contentView = summary
    card.heightAnchor.constraint(
      equalTo: summary.heightAnchor, constant: PaxTheme.Routes.sectionTop * 2
    ).isActive = true
    pane.add(card)
    if snapshot.aggregate?.aggregateOnly != true {
      related()
    }
    basis()
  }
  private func byteMetric(_ title: String, symbol: String, bytes: UInt64, color: NSColor) -> NSView
  {
    let formatted = TrafficFormatting.bytes(bytes).split(separator: " ")
    return PaxRouteMetric(
      title: title, symbol: symbol, value: formatted.dropLast().joined(separator: " "),
      unit: formatted.last.map(String.init) ?? "", color: color, style: .usage)
  }
  private func related() {
    let entries = snapshot.related
    let title =
      snapshot.dimension == .application
      ? self.interfaceCopy.text("interface_detail.4fb87823fc")
      : self.interfaceCopy.text("interface_detail.f3716a751b")
    let table = makeTable(
      titles: [
        title, self.interfaceCopy.text("interface_detail.3669d1a3f0"),
        self.interfaceCopy.text("dashboard.download"),
      ], fractions: PaxTheme.TrafficDetail.relatedColumns,
      count: entries.count, limit: PaxTheme.TrafficDetail.visibleRelated,
      identifier: "traffic-detail-related")
    table.cellProvider = { [weak self] id, column in
      guard let self, column == 0, let entry = entries.first(where: { $0.id == id }) else {
        return nil
      }
      let button = PaxTrafficCells.objectButton(entry.name) { [weak self] in
        self?.selectRelated(entry.id)
      }
      button.setAccessibilityLabel(
        self.interfaceCopy.text("interface_detail.9ae8f7e304") + entry.name
          + self.interfaceCopy.text("interface_detail.6abcf44f14"))
      return button
    }
    table.onActivate = { [weak self] in self?.selectRelated($0) }
    table.apply(
      rows: entries.map {
        .init(
          id: $0.id,
          values: [$0.name, String($0.connectionIDs.count), TrafficFormatting.bytes($0.download)])
      })
    pane.add(table)
  }
  private func basis() {
    pane.add(
      PaxRouteSectionHeading(
        self.interfaceCopy.text("event.category.routing"), symbol: "arrow.triangle.branch",
        detail: self.interfaceCopy.text("action.routing.matches_note")))
    let entries = snapshot.basis
    let table = makeTable(
      titles: [
        self.interfaceCopy.text("log_detail.rule_package"),
        self.interfaceCopy.text("interface_detail.5e4232dd76"),
        self.interfaceCopy.text("interface_detail.65371c7f8c"),
        self.interfaceCopy.text("interface_detail.2cb204a578"),
      ], fractions: PaxTheme.TrafficDetail.basisColumns,
      count: entries.count, limit: PaxTheme.TrafficDetail.visibleBasis,
      identifier: "traffic-detail-basis", rowHeight: PaxTheme.TrafficDetail.ruleRowHeight)

    table.cellProvider = { [weak self] id, column in
      guard let self, let index = Int(id), entries.indices.contains(index) else { return nil }
      let hit = entries[index].hit
      let entry = entries[index]
      let raw = entry.rawEvidence.joined(separator: "\n\n")
      if column == 0 {
        let label = PaxText.label(
          TrafficRuleFormatting.package(
            rule: hit.condition, source: hit.source, context: self.interfaceCopy.context),
          font: PaxTheme.Typography.caption())
        label.toolTip = raw
        return label
      }
      if column == 1 {
        let view = self.twoLines(
          TrafficRuleFormatting.condition(
            rule: hit.condition, source: hit.source, context: self.interfaceCopy.context),
          entry.target)
        view.toolTip = raw
        for label in view.subviews.compactMap({ $0 as? NSTextField }) { label.toolTip = raw }
        return view
      }
      if column == 3 {
        let label = PaxText.label(String(entry.count), font: PaxTheme.Typography.caption())
        label.toolTip =
          self.interfaceCopy.text(
            "interface_detail.d5a23a72fd", ["arg0": String(describing: entry.count)]) + raw
        return label
      }
      if column == 2 {
        return PaxText.label(
          entry.route.isEmpty ? self.snapshot.route : entry.route,
          font: PaxTheme.Typography.caption(), wrapping: true)
      }
      return nil
    }
    table.apply(
      rows: entries.enumerated().map { index, value in
        .init(
          id: String(index),
          values: [
            value.hit.source, value.hit.condition,
            value.route.isEmpty ? snapshot.route : value.route, String(value.count),
          ])
      })
    pane.add(table)
  }
  private func connections() {
    let rows = visibleConnections
    if let selected = snapshot.related.first(where: { $0.id == relatedID }) {
      let heading = NSStackView(views: [
        PaxText.label(selected.name), NSView(),
        button(self.interfaceCopy.text("action.common.show_all"), #selector(clearFilter)),
      ])
      pane.add(heading)
    }
    pane.add(
      WorkbenchConnectionListView(
        rows: rows, total: snapshot.retainedConnectionCount,
        loadedTotal: snapshot.connections.count,
        state: connectionListState, onChange: { [weak self] in self?.connectionListState = $0 },
        onSelect: { [weak self] in self?.selectedConnectionID = $0 },
        onResize: { [weak self] in self?.pane.invalidateContentSize() },
        context: interfaceCopy.context))
    pane.add(
      self.interfaceCopy.label(
        "action.traffic.latency_note",
        font: PaxTheme.Typography.caption(), secondary: true, wrapping: true))
  }
  private func makeTable(
    titles: [String], fractions: [CGFloat], count: Int, limit: Int, identifier: String,
    selection: PaxDataTable.Selection = .none, rowHeight: CGFloat? = nil
  ) -> PaxDataTable {
    let table = PaxDataTable(
      columns: titles.enumerated().map { index, title in
        .init(
          title, width: 0, numeric: index == titles.count - 1, growth: fractions[index],
          sortable: false, contentLayout: .centered)
      },
      metrics: .init(
        rowHeight: rowHeight ?? PaxTheme.Table.memberRow, headerHeight: PaxTheme.Table.compactHeader
      ),
      selection: selection)
    table.setTableIdentifier(identifier)
    table.heightAnchor.constraint(
      equalToConstant: PaxTheme.Table.compactHeader + CGFloat(max(1, count))
        * (rowHeight ?? PaxTheme.Table.memberRow)
    ).isActive = true
    return table
  }
  private func vertical(_ views: [NSView]) -> NSStackView {
    let stack = NSStackView(views: views)
    stack.orientation = .vertical
    stack.alignment = .leading
    stack.spacing = PaxTheme.Spacing.tight
    return stack
  }
  private func twoLines(
    _ title: String, _ subtitle: String, numeric: Bool = false, alignment: NSTextAlignment = .left
  ) -> NSView {
    let label = PaxText.label(
      title,
      font: numeric
        ? PaxTheme.Typography.tableValue : PaxTheme.Typography.label(size: PaxTheme.Table.font))
    if subtitle.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { return label }
    let sub = PaxText.label(subtitle, font: PaxTheme.Typography.caption(), secondary: true)
    for field in [label, sub] {
      field.lineBreakMode = .byTruncatingTail
      field.toolTip = field.stringValue
      field.alignment = alignment
    }
    let stack = vertical([label, sub])
    stack.spacing = PaxTheme.Spacing.xs
    for field in [label, sub] {
      field.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
    }
    return stack
  }
  @objc private func clearFilter() { selectTab("connections") }
  private func locateRule(_ key: String) {
    guard let onLocate else { return }
    if let error = onLocate(key) {
      pane.status.stringValue = error
      pane.status.textColor = PaxTheme.Color.warning
      pane.setActions([button(self.interfaceCopy.text("reprice.finish"), #selector(close))])
    } else {
      close()
    }
  }
  override func close() {
    tooltip.close()
    super.close()
  }
  override func viewWillDisappear() {
    detailTask?.cancel()
    super.viewWillDisappear()
    tooltip.close()
  }
}
