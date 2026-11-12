import AppKit
import PaxCore
import PaxUI

/// A read-only list of the loaded portion of one frozen connection snapshot.
@MainActor
final class WorkbenchConnectionListView: NSStackView {
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
    onSelect: @escaping (String?) -> Void, onResize: @escaping () -> Void
  ) {
    self.state = state
    self.onChange = onChange
    self.rows = rows
    self.total = total
    self.loadedTotal = loadedTotal
    self.onSelect = onSelect
    self.onResize = onResize
    let titles = ["时间", "访问目标", "实际连接", "分流", "结果 / 延迟"]
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
    targetFilter.placeholderString = "访问目标"
    targetFilter.stringValue = state.target
    targetFilter.sendsSearchStringImmediately = true
    targetFilter.target = self
    targetFilter.action = #selector(filterChanged)
    targetFilter.setAccessibilityIdentifier("connection-target-filter")
    targetFilter.setAccessibilityLabel("访问目标，仅筛选已加载明细")
    for (picker, title, values, current, id) in [
      (
        exitFilter, "实际连接：全部", Array(Set(rows.map(\.actualConnection))).sorted(), state.exit,
        "connection-exit-filter"
      ),
      (
        ruleFilter, "分流规则：全部", Array(Set(rows.map(\.connectionRuleTitle))).sorted(), state.rule,
        "connection-rule-filter"
      ),
    ] {
      picker.addItems(withTitles: [title] + values)
      picker.selectItem(at: values.firstIndex(of: current).map { $0 + 1 } ?? 0)
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
    state.exit = exitFilter.indexOfSelectedItem > 0 ? exitFilter.titleOfSelectedItem ?? "" : ""
    state.rule = ruleFilter.indexOfSelectedItem > 0 ? ruleFilter.titleOfSelectedItem ?? "" : ""
    onChange(state)
    apply()
  }
  private func apply() {
    shown = state.visible(rows)
    count.stringValue =
      "近24小时保留 \(total) 条 · 已加载 \(loadedTotal) 条 · 筛选后 \(shown.count) 条（筛选和排序仅作用于已加载明细）"
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
      let label = PaxText.label(row.target, font: PaxTheme.Typography.caption())
      label.lineBreakMode = .byTruncatingMiddle
      label.toolTip = row.target + "\n" + row.connectionAddress + " · " + row.transport
      return label
    case 2: return lines(row.actualConnection, "")
    case 3:
      let label = lines(row.connectionRuleTitle, "")
      label.toolTip = row.hit.condition + "\n" + row.hit.source
      return label
    case 4:
      let label = lines(
        row.connectionResult, TrafficFormatting.milliseconds(row.establishmentMS), numeric: true)
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
        PaxRouteSectionHeading("这条连接如何分流", symbol: "arrow.triangle.branch"))
      let evidence = [
        "访问目标：" + row.target,
        "实际连接：" + row.actualConnection,
        "分流规则：" + row.recordedRuleTitle,
        "触发条件："
          + (row.hit.condition == "final"
            ? "未命中任何规则，走当前线路"
            : row.recordedRuleMissing
              ? "历史未记录"
              : TrafficRuleFormatting.condition(
                rule: row.hit.condition, source: row.hit.source)),
        "原始依据：" + row.hit.condition + " · " + row.hit.source,
        "地址 / 协议：" + row.connectionAddress + " · " + row.transport,
        "直接进程 / 归属：" + row.application + " · " + row.process,
        row.sourceNote,
        row.ipv6Limited ? "连接限制：" + TrafficRuleFormatting.ipv6Limitation : "",
        "连接结果：" + row.connectionResult,
        row.establishmentMS == nil
          ? row.latencyNote : "响应延迟：" + TrafficFormatting.milliseconds(row.establishmentMS),
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
        PaxText.label("选择连接查看触发条件、进程及上传 / 下载", font: PaxTheme.Typography.caption(), secondary: true)
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
