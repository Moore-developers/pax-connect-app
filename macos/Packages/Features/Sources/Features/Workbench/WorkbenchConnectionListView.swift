import AppKit
import PaxCore
import PaxUI

/// A read-only list of the loaded portion of one frozen connection snapshot.
@MainActor
final class WorkbenchConnectionListView: NSStackView {
  private let context: PresentationContext
  private let rows: [WorkbenchTrafficRecord]
  private let total: Int
  private let loadedTotal: Int
  private let targetFilter = NSSearchField()
  private let exitFilter = NSPopUpButton()
  private let ruleFilter = NSPopUpButton()
  private let onChange: (WorkbenchConnectionListState) -> Void
  private var state: WorkbenchConnectionListState
  private let count = PaxText.label(
    "", font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  private let selected = NSStackView()
  private let table: PaxDataTable
  private var tableHeight: NSLayoutConstraint?
  private var shown: [WorkbenchTrafficRecord] = []
  private var selectedID: String?
  private let onSelect: (String?) -> Void
  private let onResize: () -> Void

  init(
    rows: [WorkbenchTrafficRecord], total: Int, loadedTotal: Int,
    state: WorkbenchConnectionListState = .init(),
    onChange: @escaping (WorkbenchConnectionListState) -> Void = { _ in },
    onSelect: @escaping (String?) -> Void, onResize: @escaping () -> Void,
    context: PresentationContext = .chinese
  ) {
    self.context = context
    self.state = state
    self.onChange = onChange
    self.rows = rows
    self.total = total
    self.loadedTotal = loadedTotal
    self.onSelect = onSelect
    self.onResize = onResize
    let titles = [
      context.text("detail_controls.8b6ff49851"), context.text("detail_controls.4fb87823fc"),
      context.text("detail_controls.6f4ef658cb"), context.text("detail_controls.5d43f05f97"),
      context.text("detail_controls.4feee7090a"),
    ]
    table = PaxDataTable(
      columns: titles.enumerated().map { index, title in
        .init(
          title, width: 0, growth: PaxTheme.TrafficDetail.connectionColumns[index],
          symbol: [
            "clock", "network", "point.3.connected.trianglepath.dotted",
            "line.3.horizontal.decrease.circle", "timer",
          ][index],
          sortable: true, contentLayout: .centered)
      },
      metrics: .init(
        rowHeight: PaxTheme.Table.memberRow, headerHeight: PaxTheme.Table.compactHeader),
      selection: .browse)
    super.init(frame: .zero)
    orientation = .vertical
    alignment = .leading
    spacing = PaxTheme.Spacing.tight
    table.setTableIdentifier("traffic-detail-connections")
    targetFilter.placeholderString = context.text("detail_controls.4fb87823fc")
    targetFilter.stringValue = state.target
    targetFilter.sendsSearchStringImmediately = true
    targetFilter.target = self
    targetFilter.action = #selector(filterChanged)
    targetFilter.setAccessibilityIdentifier("connection-target-filter")
    targetFilter.setAccessibilityLabel(context.text("detail_controls.32f9b26fbd"))
    for (picker, title, values, current, id) in [
      (
        exitFilter, context.text("detail_controls.6800024db8"),
        Array(
          Dictionary(
            rows.map { ($0.exitIdentity, $0.actualConnection(context: context)) },
            uniquingKeysWith: { $1 }
          ).map { (id: $0.key, title: $0.value) }
        ).sorted { $0.title < $1.title }, state.exit,
        "connection-exit-filter"
      ),
      (
        ruleFilter, context.text("detail_controls.9dc263147c"),
        Array(
          Dictionary(
            rows.map { ($0.ruleIdentity, $0.connectionRuleTitle(context: context)) },
            uniquingKeysWith: { $1 }
          ).map { (id: $0.key, title: $0.value) }
        ).sorted { $0.title < $1.title }, state.rule,
        "connection-rule-filter"
      ),
    ] {
      picker.addItem(withTitle: title)
      picker.lastItem?.representedObject = ""
      for value in values {
        picker.addItem(withTitle: value.title)
        picker.lastItem?.representedObject = value.id
      }
      picker.selectItem(at: values.firstIndex { $0.id == current }.map { $0 + 1 } ?? 0)
      picker.target = self
      picker.action = #selector(filterChanged)
      picker.setAccessibilityIdentifier(id)
    }
    let tools = NSStackView(views: [targetFilter, exitFilter, ruleFilter])
    tools.spacing = PaxTheme.Spacing.tight
    tools.distribution = .fillEqually
    tools.setAccessibilityIdentifier("connection-filters")
    addArrangedSubview(tools)
    addArrangedSubview(count)
    addArrangedSubview(table)
    selected.orientation = .vertical
    selected.alignment = .leading
    selected.spacing = PaxTheme.Spacing.tight
    addArrangedSubview(selected)
    for view in arrangedSubviews {
      view.widthAnchor.constraint(equalTo: widthAnchor).isActive = true
    }
    table.cellProvider = { [weak self] id, column in self?.cell(id, column: column) }
    table.onSelect = { [weak self] id in self?.select(id) }
    table.onActivate = { [weak self] id in self?.select(id) }
    table.onSort = { [weak self] column, ascending in
      guard let self else { return }
      self.state.sortColumn = column
      self.state.ascending = ascending
      self.onChange(self.state)
      self.table.setSortIndicator(column: column, ascending: ascending)
      self.apply()
    }
    table.setSortIndicator(column: state.sortColumn, ascending: state.ascending)
    apply()
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }
  @objc private func filterChanged() {
    state.target = targetFilter.stringValue
    state.exit = exitFilter.selectedItem?.representedObject as? String ?? ""
    state.rule = ruleFilter.selectedItem?.representedObject as? String ?? ""
    onChange(state)
    apply()
  }
  private func apply() {
    shown = state.visible(rows, context: context)
    count.stringValue =
      context.text(
        "detail_controls.1379e7cb40",
        [
          "arg0": String(describing: total), "arg1": String(describing: loadedTotal),
          "arg2": String(describing: shown.count),
        ])
    tableHeight?.isActive = false
    tableHeight = table.heightAnchor.constraint(
      equalToConstant: PaxTheme.Table.compactHeader
        + CGFloat(max(1, shown.count)) * PaxTheme.Table.memberRow)
    tableHeight?.isActive = true
    table.apply(rows: shown.map { .init(id: $0.id, values: []) })
    select(shown.contains { $0.id == selectedID } ? selectedID : nil)
  }
  private func cell(_ id: String, column: Int) -> NSView? {
    guard let row = shown.first(where: { $0.id == id }) else { return nil }
    switch column {
    case 0: return lines(row.connectionTime.date, row.connectionTime.clock, numeric: true)
    case 1:
      let label = PaxText.label(row.target(context: context), font: PaxTheme.Typography.caption())
      label.lineBreakMode = .byTruncatingMiddle
      label.toolTip =
        row.target(context: context) + "\n" + row.connectionAddress + " · " + row.transport
      return label
    case 2: return lines(row.actualConnection(context: context), "")
    case 3:
      let label = lines(row.connectionRuleTitle(context: context), "")
      label.toolTip =
        TrafficRuleFormatting.recordedRuleText(row.hit.condition) + "\n" + row.hit.source
      return label
    case 4:
      let label = lines(
        row.connectionResult(context: context), TrafficFormatting.milliseconds(row.establishmentMS),
        numeric: true)
      if row.failure != nil {
        label.subviews.compactMap { $0 as? NSTextField }.first?.textColor = PaxTheme.Color.error
      }
      label.toolTip = row.failure ?? (row.establishmentMS == nil ? row.latencyNote : nil)
      return label
    default: return nil
    }
  }
  private func select(_ id: String?) {
    selectedID = id
    onSelect(id)
    for view in selected.arrangedSubviews {
      selected.removeArrangedSubview(view)
      view.removeFromSuperview()
    }
    if let row = shown.first(where: { $0.id == id }) {
      selected.addArrangedSubview(
        PaxRouteSectionHeading(
          context.text("detail_controls.53a8e8ea72"), symbol: "arrow.triangle.branch"))
      let evidence = [
        context.text("detail_controls.cfa47bfdcd") + row.target(context: context),
        context.text("detail_controls.083743515d") + row.actualConnection(context: context),
        context.text("detail_controls.88492dca64") + row.recordedRuleTitle(context: context),
        context.text("detail_controls.2f43305a0c")
          + (row.hit.condition == "final"
            ? context.text("detail_controls.f465077619")
            : row.recordedRuleMissing
              ? context.text("detail_controls.1988665c12")
              : TrafficRuleFormatting.condition(
                rule: row.hit.condition, source: row.hit.source)),
        context.text("detail_controls.d9cd453885")
          + TrafficRuleFormatting.recordedRuleText(row.hit.condition) + " · "
          + row.hit.source,
        context.text("detail_controls.da3df66c31") + row.connectionAddress + " · " + row.transport,
        context.text("detail_controls.99a6627332") + row.application + " · " + row.process,
        row.sourceNote,
        row.ipv6Limited
          ? context.text("detail_controls.a13262c6c3") + TrafficRuleFormatting.ipv6Limitation : "",
        context.text("detail_controls.24e283c38c") + row.connectionResult(context: context),
        row.establishmentMS == nil
          ? row.latencyNote
          : context.text("detail_controls.efd0476602")
            + TrafficFormatting.milliseconds(row.establishmentMS),
      ].joined(separator: "\n")
      selected.addArrangedSubview(
        PaxText.label(evidence, font: PaxTheme.Typography.caption(), wrapping: true))
      let transfer = PaxTrafficCells.transfer(
        uploadText: TrafficFormatting.bytes(row.upload),
        downloadText: TrafficFormatting.bytes(row.download),
        upload: Double(row.upload), download: Double(row.download))
      transfer.heightAnchor.constraint(equalToConstant: PaxTheme.Table.memberRow).isActive = true
      selected.addArrangedSubview(transfer)
    } else {
      selected.addArrangedSubview(
        PaxText.label(
          context.text("detail_controls.8bc62237de"), font: PaxTheme.Typography.caption(),
          secondary: true)
      )
    }
    for view in selected.arrangedSubviews {
      view.widthAnchor.constraint(equalTo: selected.widthAnchor).isActive = true
    }
    onResize()
  }
  private func lines(_ title: String, _ subtitle: String, numeric: Bool = false) -> NSStackView {
    let fields = [title, subtitle].filter { !$0.isEmpty }.enumerated().map { index, text in
      let label = PaxText.label(
        text, font: numeric ? PaxTheme.Typography.tableValue : PaxTheme.Typography.caption(),
        secondary: index > 0)
      label.lineBreakMode = .byTruncatingTail
      label.toolTip = text
      return label
    }
    let stack = NSStackView(views: fields)
    stack.orientation = .vertical
    stack.alignment = .leading
    stack.spacing = PaxTheme.Spacing.xs
    for field in fields { field.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true }
    return stack
  }
}
