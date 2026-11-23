import AppKit
import PaxCore
import PaxUI

/// Filters and sorts loaded target aggregates locally; only pagination reads the frozen daemon pool.
@MainActor
final class WorkbenchConnectionGroupsView: NSStackView {
  var ruleEditorOpen = false {
    didSet { if ruleEditorOpen { readout.close() } }
  }
  let readout = PaxHoverReadout(interactive: true)
  let context: PresentationContext
  private let telemetry: TelemetryStore?
  private let initial: TrafficDetail?
  let demoRows: [WorkbenchTrafficRecord]
  let relatedID: String
  let ruleActionTitle: (TrafficConnectionGroup) -> String
  let onRule: (TrafficConnectionGroup) -> Void
  private let onResize: () -> Void
  private let onError: (any Error) -> Void
  private var task: Task<Void, Never>?
  var query = TrafficDetailQuery()
  private var page = TrafficDetail()
  var loaded: [TrafficConnectionGroup] = []
  var shown: [TrafficConnectionGroup] = []
  var searchText = ""
  var selectedExit = ""
  var selectedRule = ""
  var groupSortColumn = 1
  var groupAscending = false
  var selectedHoverTab = 0
  private var unavailable = false
  private let summaryStrip: WorkbenchConnectionSummaryStrip
  private let targetFilter = PaxRouteSearchField()
  private let exitFilter = PaxRoutePicker(style: .toolbar)
  private let ruleFilter = PaxRoutePicker(style: .toolbar)
  private lazy var clearButton = PaxRouteButton(
    context.text("connection_groups.clear_filters"), style: .quiet,
    target: self, action: #selector(clearFilters))
  private let footerInfo = PaxText.label(
    "", font: PaxTheme.Typography.metric(size: 11.5), secondary: true)
  private let status = PaxText.label(
    "", font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  let table: PaxDataTable
  private var tableHeight: NSLayoutConstraint?
  private lazy var more: PaxRouteButton = {
    let button = PaxRouteButton(
      context.text("connection_groups.more"), style: .secondary,
      target: self, action: #selector(loadMore))
    button.isCapsule = true
    button.controlSize = .small
    return button
  }()

  init(
    detail: TrafficDetail?, rows: [WorkbenchTrafficRecord], relatedID: String?,
    connectionKey: String,
    telemetry: TelemetryStore?, context: PresentationContext,
    ruleActionTitle: @escaping (TrafficConnectionGroup) -> String,
    onRule: @escaping (TrafficConnectionGroup) -> Void,
    onResize: @escaping () -> Void, onError: @escaping (any Error) -> Void
  ) {
    self.initial = detail
    self.demoRows = rows
    self.relatedID = relatedID ?? ""
    self.telemetry = telemetry
    self.context = context
    self.ruleActionTitle = ruleActionTitle
    self.onRule = onRule
    self.onResize = onResize
    self.onError = onError
    summaryStrip = WorkbenchConnectionSummaryStrip(context: context)
    let titles = [
      "connection_groups.target", "connection_groups.connections",
      "connection_groups.transfer", "connection_groups.exit",
    ].map { context.text($0) }
    let countTitleWidth = ceil(
      (titles[1] as NSString).size(withAttributes: [.font: PaxTheme.Typography.caption()]).width)
    let countColumnWidth =
      countTitleWidth + PaxTheme.Table.inset + PaxTheme.Spacing.xs
      + 2 * (PaxTheme.Control.navigationIcon + PaxTheme.Spacing.xs)
    table = PaxDataTable(
      columns: titles.enumerated().map { index, title in
        .init(
          title, width: index == 1 ? countColumnWidth : 0, numeric: index == 1 || index == 2,
          growth: PaxTheme.TrafficDetail.targetColumns[index],
          symbol: [
            "network", "number", "arrow.up.arrow.down", "point.3.connected.trianglepath.dotted",
          ][index],
          sortable: true, contentLayout: .centered)
      },
      metrics: .init(
        rowHeight: PaxTheme.TrafficDetail.targetRowHeight,
        headerHeight: PaxTheme.Table.compactHeader), selection: .single)
    super.init(frame: .zero)
    orientation = .vertical
    alignment = .leading
    spacing = PaxTheme.Spacing.medium
    query.groupConnections = true
    query.connectionKey = connectionKey
    query.rowID = detail?.summary.id ?? ""
    query.readToken = detail?.readToken ?? ""
    query.limit = 20
    query.relatedID = self.relatedID

    targetFilter.placeholderString = context.text("connection_groups.search")
    targetFilter.setAccessibilityIdentifier("connection-target-filter")
    targetFilter.sendsSearchStringImmediately = true
    targetFilter.target = self
    targetFilter.action = #selector(filterChanged)
    targetFilter.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)

    let pickerWidth = PaxTheme.TrafficDetail.targetFilterPickerWidth
    for (picker, id) in [
      (exitFilter, "connection-exit-filter"), (ruleFilter, "connection-rule-filter"),
    ] {
      picker.target = self
      picker.action = #selector(filterChanged)
      picker.setAccessibilityIdentifier(id)
      picker.widthAnchor.constraint(equalToConstant: pickerWidth).isActive = true
    }
    clearButton.isHidden = true
    clearButton.setContentHuggingPriority(.required, for: .horizontal)
    clearButton.setContentCompressionResistancePriority(.required, for: .horizontal)

    let tools = NSStackView(views: [targetFilter, exitFilter, ruleFilter, clearButton])
    tools.orientation = .horizontal
    tools.alignment = .centerY
    tools.spacing = PaxTheme.Spacing.tight

    table.setTableIdentifier("traffic-detail-connections")
    table.hoverReadout = readout
    table.rowReadout = { [weak self] id in self?.hover(id) }
    table.cellProvider = { [weak self] id, column in self?.cell(id, column: column) }
    table.onActivate = { [weak self] id in self?.table.presentReadout(for: id) }
    table.menuForRow = { [weak self] id in
      guard let self else { return nil }
      return self.menu(id)
    }
    table.onSort = { [weak self] column, ascending in
      guard let self else { return }
      self.groupSortColumn = column
      self.groupAscending = ascending
      self.table.setSortIndicator(column: column, ascending: ascending)
      self.applyLocalFilters()
    }
    table.setSortIndicator(column: 1, ascending: false)

    footerInfo.lineBreakMode = .byTruncatingTail
    footerInfo.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    more.setContentHuggingPriority(.required, for: .horizontal)
    more.setContentCompressionResistancePriority(.required, for: .horizontal)
    let footer = NSView()
    footerInfo.translatesAutoresizingMaskIntoConstraints = false
    more.translatesAutoresizingMaskIntoConstraints = false
    footer.addSubview(footerInfo)
    footer.addSubview(more)
    NSLayoutConstraint.activate([
      footerInfo.leadingAnchor.constraint(equalTo: footer.leadingAnchor),
      footerInfo.centerYAnchor.constraint(equalTo: footer.centerYAnchor),
      footerInfo.trailingAnchor.constraint(lessThanOrEqualTo: more.leadingAnchor, constant: -12),
      more.trailingAnchor.constraint(equalTo: footer.trailingAnchor),
      more.topAnchor.constraint(equalTo: footer.topAnchor),
      more.bottomAnchor.constraint(equalTo: footer.bottomAnchor),
      footer.heightAnchor.constraint(greaterThanOrEqualTo: footerInfo.heightAnchor),
    ])

    status.isHidden = true
    for view in [summaryStrip, tools, table, footer, status] {
      addArrangedSubview(view)
      view.widthAnchor.constraint(equalTo: widthAnchor).isActive = true
    }

    if let detail,
      !detail.connectionGroups.isEmpty
        || detail.targetGroupTotal == 0 && detail.retainedConnectionCount == 0
    {
      page = detail
      if self.relatedID.isEmpty {
        loaded = detail.connectionGroups
        installChoices()
        render()
      } else {
        reload()
      }
    } else {
      reload()
    }
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }

  func stop() {
    task?.cancel()
    readout.close()
    summaryStrip.stop()
  }

  func invalidate() {
    unavailable = true
    task?.cancel()
    more.isEnabled = false
  }

  private func installChoices() {
    let exits = Dictionary(
      loaded.flatMap(\.exits).map { (Self.exitID($0), exitName($0)) },
      uniquingKeysWith: { first, _ in first }
    ).map { (id: $0.key, title: $0.value) }
    let rules = Dictionary(
      loaded.flatMap(\.rules).map { (Self.ruleID($0), ruleName($0)) },
      uniquingKeysWith: { first, _ in first }
    ).map { (id: $0.key, title: $0.value) }
    for (picker, title, values, current) in [
      (exitFilter, context.text("detail_controls.6800024db8"), exits, selectedExit),
      (ruleFilter, context.text("detail_controls.9dc263147c"), rules, selectedRule),
    ] {
      let sorted = values.sorted(by: { $0.title < $1.title })
      picker.removeAllItems()
      picker.addItem(withTitle: title)
      picker.lastItem?.representedObject = ""
      for value in sorted {
        picker.addItem(withTitle: value.title)
        picker.lastItem?.representedObject = value.id
      }
      picker.selectItem(at: sorted.firstIndex { $0.id == current }.map { $0 + 1 } ?? 0)
      picker.needsDisplay = true
    }
  }

  @objc private func filterChanged() {
    searchText = targetFilter.stringValue
    selectedExit = exitFilter.selectedItem?.representedObject as? String ?? ""
    selectedRule = ruleFilter.selectedItem?.representedObject as? String ?? ""
    exitFilter.needsDisplay = true
    ruleFilter.needsDisplay = true
    applyLocalFilters()
  }

  @objc private func clearFilters() {
    targetFilter.stringValue = ""
    searchText = ""
    selectedExit = ""
    selectedRule = ""
    exitFilter.selectItem(at: 0)
    ruleFilter.selectItem(at: 0)
    exitFilter.needsDisplay = true
    ruleFilter.needsDisplay = true
    applyLocalFilters()
  }

  private func applyLocalFilters() {
    readout.close()
    render()
  }

  private func reload() {
    query.cursor = ""
    readout.close()
    fetch(query, append: false)
  }

  @objc private func loadMore() {
    var q = query
    q.cursor = page.nextCursor
    fetch(q, append: true)
  }

  private func fetch(_ q: TrafficDetailQuery, append: Bool) {
    guard !unavailable else { return }
    task?.cancel()
    if telemetry == nil || initial == nil {
      page = demoPage()
      loaded = page.connectionGroups
      installChoices()
      render()
      return
    }
    guard let telemetry else { return }
    let generation = telemetry.sessionGeneration
    more.isEnabled = false
    setStatus(context.text("connection_groups.loading"))
    task = Task { [weak self] in
      do {
        let next = try await telemetry.trafficDetail(q)
        guard let self, !Task.isCancelled, generation == telemetry.sessionGeneration else { return }
        self.query.readToken = next.readToken
        self.page = next
        self.loaded = append ? self.loaded + next.connectionGroups : next.connectionGroups
        self.installChoices()
        self.render()
        self.setStatus("")
        self.more.isEnabled = true
      } catch {
        guard let self, !Task.isCancelled else { return }
        self.setStatus(ProductErrorPresentation.display(error, context: self.context))
        self.more.isEnabled = true
        self.onError(error)
        self.more.isEnabled = !self.unavailable
      }
    }
  }

  private func setStatus(_ text: String) {
    status.stringValue = text
    status.isHidden = text.isEmpty
  }

  private func render() {
    shown = visibleGroups()
    let hasFilter =
      !searchText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
      || !selectedExit.isEmpty || !selectedRule.isEmpty
    clearButton.isHidden = !hasFilter
    summaryStrip.update(
      targets: page.targetGroupTotal, connections: page.filteredConnectionCount,
      loaded: loaded.count, shown: shown.count)
    footerInfo.stringValue = context.text(
      "connection_groups.loaded_footer",
      ["loaded": String(loaded.count), "targets": String(page.targetGroupTotal)])

    table.isHidden = false
    tableHeight?.isActive = false
    tableHeight = table.heightAnchor.constraint(
      equalToConstant: PaxTheme.Table.compactHeader
        + CGFloat(max(1, shown.count)) * PaxTheme.TrafficDetail.targetRowHeight)
    tableHeight?.isActive = true
    table.apply(
      rows: shown.map { .init(id: $0.id, values: []) },
      emptyMessage: context.text("connection_groups.no_matches"))

    let remaining =
      page.targetGroupTotal > UInt64(loaded.count)
      ? Int(min(UInt64(query.limit), page.targetGroupTotal - UInt64(loaded.count))) : 0
    more.title =
      remaining > 0
      ? context.text("connection_groups.more_batch", ["count": String(remaining)])
      : context.text("connection_groups.more")
    more.isHidden = page.nextCursor.isEmpty
    more.isEnabled = !unavailable
    onResize()
  }

  private func cell(_ id: String, column: Int) -> NSView? {
    guard let group = shown.first(where: { $0.id == id }) else { return nil }
    switch column {
    case 0:
      return WorkbenchConnectionTargetCell(
        name: targetName(group), group: group, context: context)
    case 1:
      let label = PaxText.label(
        String(group.connections),
        font: PaxTheme.Typography.metric(size: 13, weight: .semibold))
      label.alignment = .right
      return label
    case 2:
      return WorkbenchConnectionTransferText(
        upload: group.upload, download: group.download, bytesState: group.bytesState)
    default:
      return WorkbenchConnectionExitCell(
        exits: group.exits, nameForExit: { [self] in exitName($0) }, context: context)
    }
  }
}
