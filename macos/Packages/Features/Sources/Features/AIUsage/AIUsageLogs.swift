import AppKit
import PaxCore
import PaxUI

extension AIUsageViewController {
  func renderLogs() {
    let logs = AIUsageLogView(store: store)
    logs.onRequest = { [weak self] id in self?.show(.request(id)) }
    logContent = logs
    add(logs)
  }
}

/// Empty-state card height: icon, title and a two-line caption.
private let emptyHeight: CGFloat = 160

/// Dense columns retain a smaller inset; identity cells use the route table default.
private let cellInset: CGFloat = PaxTheme.Spacing.tight

/// 筛选行在最小内容宽度下仍保持单行的控件宽度。
private enum FilterWidth {
  static let cost: CGFloat = 260
  static let profile: CGFloat = 102
  static let model: CGFloat = 112
  /// 长模型名按所选标题加宽，费用输入组保留最低宽度。
  static let modelMax: CGFloat = 176
  static let costMin: CGFloat = 244
  static let client: CGFloat = 92
  static let status: CGFloat = 96
  static let reset: CGFloat = 46
}

/// Retains filter controls while only the rows and matching totals are refreshed.
@MainActor
final class AIUsageLogView: NSView, NSTextFieldDelegate {
  let store: AIUsageStore
  var onRequest: ((String) -> Void)?
  let cellActions = AIUsageActions()
  let status = PaxRoutePicker(style: .filter)
  private let cost = PaxFilterRange(title: "费用 $")
  private var minimum: PaxRouteField { cost.minimum }
  private var maximum: PaxRouteField { cost.maximum }
  private let costError = AIUsageLayout.label("", secondary: true)
  private let profile = PaxRoutePicker(style: .filter)
  private let model = PaxRoutePicker(style: .filter)
  private var modelWidth: NSLayoutConstraint?
  private let client = PaxRoutePicker(style: .filter)
  private lazy var reset = PaxRouteButton(
    "清除", style: .quiet, target: self, action: #selector(clear))
  private lazy var previous = PaxCompactButton("上一页", target: self, action: #selector(previousPage))
  private lazy var next = PaxCompactButton("下一页", target: self, action: #selector(nextPage))
  private let pageLabel = AIUsageLayout.label("", secondary: true)
  private let countLabel = AIUsageLayout.label("", secondary: true)
  /// Summary cards of every row matching the period and filters, above the table.
  let summaryView = AIUsageLogSummaryView()
  private let emptyCard = PaxCard()
  private let empty = PaxEmptyState(
    symbol: "list.bullet.rectangle", title: "没有请求日志", caption: "", identifier: "ai-log-empty")
  private var page = 0
  private var rows: [AIRequest] = []
  private var matching: [AIRequest] = []
  private var rendered: [AIRequest] = []
  private let readStatus = PaxInlineStatus()
  let table: PaxDataTable

  init(store: AIUsageStore) {
    self.store = store
    table = PaxDataTable(
      columns: [
        // Minimum widths fit the 814pt content area. Names receive most of the extra width;
        // compact status and multiplier columns stay fixed, as in the route table.
        .init("CLI", width: 110, growth: 0, symbol: "terminal", sortable: false),
        .init(
          "模型 / 时间", width: 166, growth: 3, symbol: "clock", sortable: false),
        .init(
          "Profile", width: 90, growth: 2, alignment: .center, symbol: "person.crop.circle",
          inset: cellInset, sortable: false),
        .init(
          "倍率", width: 56, growth: 0, alignment: .center, symbol: "creditcard", inset: cellInset,
          sortable: false),
        .init(
          "TPS / 耗时", width: 104, growth: 1, alignment: .center, symbol: "speedometer",
          inset: cellInset, sortable: false),
        .init(
          "Token · 官方 / 渠道", width: 160, numeric: true, growth: 2, symbol: "number",
          inset: cellInset,
          sortable: false),
        .init(
          "缓存率", width: 72, growth: 0.5, alignment: .center, symbol: "internaldrive",
          inset: cellInset, sortable: false),
        .init(
          "状态", width: 56, growth: 0, alignment: .center, symbol: "checkmark.circle",
          inset: cellInset, sortable: false),
      ], metrics: .init(),
      selection: .browse)
    super.init(frame: .zero)
    for (field, placeholder, id) in [
      (minimum, "最低", "ai-log-min-cost"), (maximum, "最高", "ai-log-max-cost"),
    ] {
      field.placeholderString = placeholder
      field.delegate = self
      field.setAccessibilityIdentifier(id)
      field.setAccessibilityLabel("官方费用" + placeholder + "（美元）")
      field.toolTip = "按1×官方费用筛选，含边界；留空不限制，最多6位小数"
      field.widthAnchor.constraint(greaterThanOrEqualToConstant: 84).isActive = true
    }
    costError.textColor = PaxTheme.Color.error
    costError.isHidden = true
    status.setAccessibilityIdentifier("ai-log-result")
    reset.setAccessibilityIdentifier("ai-log-reset")
    reset.toolTip = "清除费用范围和全部筛选，保留日期"
    for (picker, id) in [
      (profile, "ai-log-profile"), (model, "ai-log-model"), (client, "ai-log-client"),
      (status, "ai-log-result"),
    ] {
      picker.identifier = .init(id)
      picker.setAccessibilityIdentifier(id)
      picker.target = self
      picker.action = #selector(filterChanged(_:))
    }
    cost.toolTip = "按官方费用（1×）筛选"
    let fields: [NSView] = [cost, client, profile, model, status, reset]
    let filters = PaxFilterRow(fields)
    // Keep the filter controls on one row at the minimum supported content width.
    for (index, width) in [
      0: FilterWidth.cost,
      1: FilterWidth.client,
      2: FilterWidth.profile,
      3: FilterWidth.model,
      4: FilterWidth.status,
      5: FilterWidth.reset,
    ] {
      let constraint = fields[index].widthAnchor.constraint(equalToConstant: width)
      if index == 0 { constraint.priority = .defaultHigh }
      if index == 3 { modelWidth = constraint }
      constraint.isActive = true
    }
    cost.widthAnchor.constraint(greaterThanOrEqualToConstant: FilterWidth.costMin).isActive = true
    emptyCard.contentView = empty
    emptyCard.heightAnchor.constraint(equalToConstant: emptyHeight).isActive = true
    empty.heightAnchor.constraint(equalToConstant: emptyHeight - PaxTheme.Spacing.card * 2)
      .isActive = true
    let foot = AIUsageLayout.stack(
      [countLabel, NSView(), previous, pageLabel, next], vertical: false)
    let body = AIUsageLayout.stack([
      filters, costError, readStatus, summaryView, table, emptyCard, foot,
    ])
    body.spacing = PaxTheme.Spacing.regular
    body.translatesAutoresizingMaskIntoConstraints = false
    addSubview(body)
    NSLayoutConstraint.activate([
      body.leadingAnchor.constraint(equalTo: leadingAnchor),
      body.trailingAnchor.constraint(equalTo: trailingAnchor),
      body.topAnchor.constraint(equalTo: topAnchor),
      body.bottomAnchor.constraint(equalTo: bottomAnchor),
    ])
    table.setTableIdentifier("ai-usage-log-list")
    table.cellProvider = { [weak self] id, column in
      guard let self, let request = self.rows.first(where: { $0.id == id }) else { return nil }
      return PaxDataTable.reusableContent(
        self.cell(request, column: column), reuseKey: self.cellKey(request, column: column))
    }
    table.cellUpdater = { [weak self] existing, id, column in
      guard let self, let request = self.rows.first(where: { $0.id == id }) else { return false }
      return PaxDataTable.updateContent(existing, reusing: self.cellKey(request, column: column)) {
        self.cell(request, column: column)
      }
    }
    table.onRowClick = { [weak self] id in
      self?.store.data?.setLogsReading(true)
      self?.onRequest?(id)
    }
    table.onActivate = table.onRowClick
    refresh()
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  func refresh() {
    for (field, value) in [(minimum, store.minimumCost), (maximum, store.maximumCost)] {
      if field.currentEditor() == nil, costError.isHidden {
        field.stringValue = AIUsageFormatting.costBoundInput(value)
      }
      field.isEnabled = store.preview || store.data?.costRangeSupported == true
    }
    populate(
      profile, title: "全部 Profile", options: store.logProfileOptions, selected: store.logProfileID)
    populate(
      model, title: "全部模型", options: store.models.map { ($0.id, $0.name) },
      selected: store.logModelID)
    modelWidth?.constant = Self.pickerWidth(model, minimum: FilterWidth.model)
    populate(
      client, title: "全部 CLI", options: AIClient.allCases.map { ($0.rawValue, $0.rawValue) },
      selected: store.logClient?.rawValue)
    for item in client.itemArray {
      if let raw = item.representedObject as? String, let cli = AIClient(rawValue: raw),
        let icon = AIClientMark.image(cli)?.copy() as? NSImage
      {
        icon.size = .init(width: PaxTheme.Dashboard.iconGlyph, height: PaxTheme.Dashboard.iconGlyph)
        item.image = icon
      }
    }
    populate(
      status, title: "全部状态", options: [("success", "成功"), ("failed", "失败")],
      selected: store.logResult.map { $0 ? "success" : "failed" })
    reset.isEnabled =
      store.hasLogFilters || !minimum.stringValue.isEmpty || !maximum.stringValue.isEmpty
    matching = store.filteredLogs
    page = store.preview ? 0 : store.logPage
    summaryView.apply(period: store.period.rawValue, totals: store.logSummary)
    summaryView.setAccessibilityLabel(
      AIUsageLogFormat.summary(period: store.period.rawValue, totals: store.logSummary))
    renderRows()
  }
  private struct CellKey: Hashable {
    let request: AIRequest
    let modelName: String
    let column: Int
    func hash(into hasher: inout Hasher) {
      hasher.combine(request.id)
      hasher.combine(column)
    }
  }
  private func cellKey(_ request: AIRequest, column: Int) -> CellKey {
    CellKey(request: request, modelName: store.model(request.modelID), column: column)
  }
  /// Width at which the picker draws its selected title in full (title + insets), capped.
  static func pickerWidth(_ picker: NSPopUpButton, minimum: CGFloat) -> CGFloat {
    let title = (picker.titleOfSelectedItem ?? "") as NSString
    let text = title.size(withAttributes: [.font: picker.font ?? PaxTheme.Typography.caption()])
    let need = ceil(text.width) + PaxTheme.Routes.controlInset * 3 + 2
    return min(FilterWidth.modelMax, max(minimum, need))
  }
  private func renderRows() {
    let old = Dictionary(uniqueKeysWithValues: rendered.map { ($0.id, $0) })
    let start = page * 10
    rows = store.preview ? Array(matching.dropFirst(start).prefix(10)) : matching
    let changed = Set(rows.filter { old[$0.id] != $0 }.map(\.id))
    if rows != rendered {
      cellActions.reset()
      table.apply(
        rows: rows.map { .init(id: $0.id, values: []) }, emptyMessage: "", reloading: changed)
      rendered = rows
    }
    table.isHidden = rows.isEmpty
    let waiting = !store.preview && store.data?.logs == nil
    emptyCard.isHidden = !rows.isEmpty || waiting
    let failure = store.data?.error
    readStatus.isHidden = !waiting && failure == nil
    readStatus.apply(
      failure == nil ? .working : .failure,
      text: failure ?? "正在读取本地请求…", action: failure == nil ? nil : "重试")
    readStatus.onAction = { [weak self] in self?.store.data?.retry() }
    summaryView.isHidden = waiting
    // Product mode has no collected usage, so say so instead of blaming the filters.
    empty.update(caption: "没有匹配的本地请求，请调整日期或清除筛选")
    let total = store.preview ? matching.count : store.logSummary.count
    countLabel.stringValue =
      matching.isEmpty
      ? "0 条"
      : "\(start + 1)–\(start + rows.count) / " + AIUsageFormatting.count(total)
    pageLabel.stringValue = "\(page + 1) / \(max(1, (total + 9) / 10))"
    previous.isEnabled = page > 0
    next.isEnabled = start + 10 < total
  }
  private func populate(
    _ picker: NSPopUpButton, title: String, options: [(String, String)], selected: String?
  ) {
    picker.removeAllItems()
    for (id, label) in [("", title)] + options {
      let item = NSMenuItem(title: label, action: nil, keyEquivalent: "")
      item.representedObject = id
      picker.menu?.addItem(item)
    }
    picker.selectItem(
      at: picker.itemArray.firstIndex { ($0.representedObject as? String) == (selected ?? "") } ?? 0
    )
    picker.toolTip = picker.titleOfSelectedItem
    picker.setAccessibilityLabel(title)
  }
  @objc private func filterChanged(_ sender: NSPopUpButton) {
    guard let raw = sender.selectedItem?.representedObject as? String else { return }
    let value = raw.isEmpty ? nil : raw
    switch sender.identifier?.rawValue {
    case "ai-log-result": store.perform(.logResult(value.map { $0 == "success" }))
    case "ai-log-client": store.perform(.logClient(value.flatMap(AIClient.init(rawValue:))))
    case "ai-log-profile": store.perform(.logProfile(value))
    case "ai-log-model": store.perform(.logModel(value))
    default: break
    }
  }
  func controlTextDidChange(_ notification: Notification) { applyCostRange() }
  func controlTextDidEndEditing(_ notification: Notification) { applyCostRange() }
  private func applyCostRange() {
    reset.isEnabled =
      store.hasLogFilters || !minimum.stringValue.isEmpty || !maximum.stringValue.isEmpty
    do {
      let bounds = try AIUsageFormatting.costBounds(
        minimum: minimum.stringValue, maximum: maximum.stringValue)
      costError.isHidden = true
      guard bounds.minimum != store.minimumCost || bounds.maximum != store.maximumCost else {
        return
      }
      store.perform(.logCostRange(bounds.minimum, bounds.maximum))
    } catch {
      costError.stringValue = error.message + "；仍显示上次有效筛选结果"
      costError.isHidden = false
    }
  }
  @objc private func clear() {
    costError.isHidden = true
    minimum.stringValue = ""
    maximum.stringValue = ""
    store.perform(.resetLogFilters)
  }
  @objc private func previousPage() {
    guard page > 0 else { return }
    if store.preview {
      page -= 1
      renderRows()
    } else {
      store.setLogPage(page - 1)
    }
  }
  @objc private func nextPage() {
    guard (page + 1) * 10 < (store.preview ? matching.count : store.logSummary.count) else {
      return
    }
    if store.preview {
      page += 1
      renderRows()
    } else {
      store.setLogPage(page + 1)
    }
  }
}
