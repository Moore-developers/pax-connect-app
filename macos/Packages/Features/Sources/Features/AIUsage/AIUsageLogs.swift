import AppKit
import PaxCore
import PaxUI

extension AIUsageViewController {
  func renderLogs() {
    let logs = AIUsageLogView(store: store, interfaceCopy: interfaceCopy)
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
  let interfaceCopy: InterfaceCopyBindings
  var context: PresentationContext { interfaceCopy.context }
  let store: AIUsageStore
  var onRequest: ((String) -> Void)?
  let cellActions = AIUsageActions()
  let status = PaxRoutePicker(style: .filter)
  private lazy var cost = PaxFilterRange(title: context.text("ai_logs.77fe4ba3d2"))
  private var minimum: PaxRouteField { cost.minimum }
  private var maximum: PaxRouteField { cost.maximum }
  private let costError = AIUsageLayout.label("", secondary: true)
  private let profile = PaxRoutePicker(style: .filter)
  private let model = PaxRoutePicker(style: .filter)
  private var modelWidth: NSLayoutConstraint?
  private let client = PaxRoutePicker(style: .filter)
  private lazy var reset = PaxRouteButton(
    context.text("ai_logs.bce2377283"), style: .quiet, target: self, action: #selector(clear))
  private lazy var previous = PaxCompactButton(
    context.text("ai_logs.c9b9ae7a61"), target: self, action: #selector(previousPage))
  private lazy var next = PaxCompactButton(
    context.text("ai_logs.8a8542f696"), target: self, action: #selector(nextPage))
  private let pageLabel = AIUsageLayout.label("", secondary: true)
  private let countLabel = AIUsageLayout.label("", secondary: true)
  /// Summary cards of every row matching the period and filters, above the table.
  let summaryView = AIUsageLogSummaryView()
  private let emptyCard = PaxCard()
  private lazy var empty = PaxEmptyState(
    symbol: "list.bullet.rectangle", title: context.text("ai_logs.2f9320dc76"), caption: "",
    identifier: "ai-log-empty")
  private var page = 0
  private var rows: [AIRequest] = []
  private var matching: [AIRequest] = []
  private var rendered: [AIRequest] = []
  private let readStatus = PaxInlineStatus()
  let table: PaxDataTable

  init(store: AIUsageStore, interfaceCopy: InterfaceCopyBindings = InterfaceCopyBindings()) {
    self.interfaceCopy = interfaceCopy
    let context = interfaceCopy.context
    self.store = store
    table = PaxDataTable(
      columns: [
        // Minimum widths fit the 814pt content area. Names receive most of the extra width;
        // compact status and multiplier columns stay fixed, as in the route table.
        .init("CLI", width: 110, growth: 0, symbol: "terminal", sortable: false),
        .init(
          context.text("ai_logs.f996981f0d"), width: 166, growth: 3, symbol: "clock",
          sortable: false),
        .init(
          context.text("ai_logs.be547232eb"), width: 90, growth: 2, alignment: .center,
          symbol: "person.crop.circle",
          inset: cellInset, sortable: false),
        .init(
          context.text("ai_logs.205f148340"), width: 56, growth: 0, alignment: .center,
          symbol: "creditcard", inset: cellInset,
          sortable: false),
        .init(
          context.text("ai_logs.3282fd6773"), width: 104, growth: 1, alignment: .center,
          symbol: "speedometer",
          inset: cellInset, sortable: false),
        .init(
          context.text("ai_logs.df444cc4db"), width: 160, numeric: true, growth: 2,
          symbol: "number",
          inset: cellInset,
          sortable: false),
        .init(
          context.text("ai_logs.column.cache"), width: 72, growth: 0.5, alignment: .center,
          inset: cellInset, sortable: false),
        .init(
          context.text("ai_logs.6320b4a872"), width: 56, growth: 0, alignment: .center,
          inset: cellInset, sortable: false),
      ], metrics: .init(),
      selection: .browse)
    super.init(frame: .zero)
    for (field, placeholder, id) in [
      (minimum, context.text("ai_logs.8b5f2a61b7"), "ai-log-min-cost"),
      (maximum, context.text("ai_logs.8672864e90"), "ai-log-max-cost"),
    ] {
      field.placeholderString = placeholder
      field.delegate = self
      field.setAccessibilityIdentifier(id)
      field.setAccessibilityLabel(
        context.text("ai_logs.c0b69a3163") + placeholder + context.text("ai_logs.3b9fc50aab"))
      field.toolTip = context.text("ai_logs.ec25b4f178")
      field.widthAnchor.constraint(greaterThanOrEqualToConstant: 84).isActive = true
    }
    costError.textColor = PaxTheme.Color.error
    costError.isHidden = true
    status.setAccessibilityIdentifier("ai-log-result")
    reset.setAccessibilityIdentifier("ai-log-reset")
    reset.toolTip = context.text("ai_logs.755d15031a")
    for (picker, id) in [
      (profile, "ai-log-profile"), (model, "ai-log-model"), (client, "ai-log-client"),
      (status, "ai-log-result"),
    ] {
      picker.identifier = .init(id)
      picker.setAccessibilityIdentifier(id)
      picker.target = self
      picker.action = #selector(filterChanged(_:))
    }
    cost.toolTip = context.text("ai_logs.27925a6ba8")
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

  func invalidateLanguage() {
    for (index, key) in [
      "", "ai_logs.f996981f0d", "ai_logs.be547232eb", "ai_logs.205f148340", "ai_logs.3282fd6773",
      "ai_logs.df444cc4db", "ai_logs.column.cache", "ai_logs.6320b4a872",
    ].enumerated() where !key.isEmpty {
      table.setColumnTitle(context.text(key), at: index)
    }
    reset.title = context.text("ai_logs.bce2377283")
    previous.title = context.text("ai_logs.c9b9ae7a61")
    next.title = context.text("ai_logs.8a8542f696")
    empty.apply(
      title: context.text("ai_logs.2f9320dc76").isEmpty ? "" : context.text("ai_logs.2f9320dc76"),
      caption: "")
    cost.applyTitle(context.text("ai_logs.77fe4ba3d2"))
    refresh()
    table.refreshVisibleCopy()
  }
  func refresh() {
    for (field, value) in [(minimum, store.minimumCost), (maximum, store.maximumCost)] {
      if field.currentEditor() == nil, costError.isHidden {
        field.stringValue = AIUsageFormatting.costBoundInput(value)
      }
      field.isEnabled = store.preview || store.data?.costRangeSupported == true
    }
    populate(
      profile, title: context.text("ai_logs.4e108572f4"),
      options: store.logProfileOptions(context: context),
      selected: store.logProfileID)
    populate(
      model, title: context.text("ai_logs.0164f80178"),
      options: store.models.map { ($0.id, $0.name) },
      selected: store.logModelID)
    modelWidth?.constant = Self.pickerWidth(model, minimum: FilterWidth.model)
    populate(
      client, title: context.text("ai_logs.54438e1395"),
      options: AIClient.allCases.map { ($0.rawValue, $0.rawValue) },
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
      status, title: context.text("ai_logs.0a379c1e73"),
      options: [
        ("success", context.text("ai_logs.053461ce86")),
        ("failed", context.text("ai_logs.28384d7afd")),
      ],
      selected: store.logResult.map { $0 ? "success" : "failed" })
    reset.isEnabled =
      store.hasLogFilters || !minimum.stringValue.isEmpty || !maximum.stringValue.isEmpty
    matching = store.filteredLogs
    page = store.preview ? 0 : store.logPage
    summaryView.apply(
      period: store.period.label(context: context), totals: store.logSummary, context: context)
    summaryView.setAccessibilityLabel(
      AIUsageLogFormat.summary(
        period: store.period.label(context: context), totals: store.logSummary, context: context))
    renderRows()
  }
  private struct CellKey: Hashable {
    let request: AIRequest
    let modelName: String
    let column: Int
    let presentationRevision: UInt64
    func hash(into hasher: inout Hasher) {
      hasher.combine(request.id)
      hasher.combine(column)
    }
  }
  private func cellKey(_ request: AIRequest, column: Int) -> CellKey {
    CellKey(
      request: request, modelName: store.model(request.modelID), column: column,
      presentationRevision: context.revision)
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
      text: failure ?? context.text("ai_logs.8287d18154"),
      action: failure == nil ? nil : context.text("ai_logs.b8784c8dd5"))
    readStatus.onAction = { [weak self] in self?.store.data?.retry() }
    summaryView.isHidden = waiting
    // Product mode has no collected usage, so say so instead of blaming the filters.
    empty.update(caption: context.text("ai_logs.82cea0d2fa"))
    let total = store.preview ? matching.count : store.logSummary.count
    countLabel.stringValue =
      matching.isEmpty
      ? context.text("ai_logs.dce2940b63")
      : "\(start + 1)–\(start + rows.count) / " + AIUsageFormatting.count(total)
    pageLabel.stringValue = "\(page + 1) / \(max(1, (total + 9) / 10))"
    previous.isEnabled = page > 0
    next.isEnabled = start + 10 < total
  }
  private func populate(
    _ picker: NSPopUpButton, title: String, options: [(String, String)], selected: String?
  ) {
    let entries = [("", title)] + options
    if picker.itemArray.compactMap({ $0.representedObject as? String }) == entries.map(\.0) {
      for (item, entry) in zip(picker.itemArray, entries) { item.title = entry.1 }
    } else {
      picker.removeAllItems()
      for (id, label) in entries {
        let item = NSMenuItem(title: label, action: nil, keyEquivalent: "")
        item.representedObject = id
        picker.menu?.addItem(item)
      }
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
      costError.stringValue = error.message + context.text("ai_logs.dc02b05846")
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
