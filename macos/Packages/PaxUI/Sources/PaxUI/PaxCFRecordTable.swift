import AppKit

/// Stage-specific metrics, row actions and incremental updates share one native table.
public final class PaxCFRecordTable: NSView {
  public enum Action: Int { case latency, download, both, location }
  public struct Row: Equatable {
    public let id: String
    public let values: [String]
    public let captions: [String]
    public let numbers: [Int: Double]
    public let failed: Bool
    public init(
      id: String, values: [String], captions: [String], numbers: [Int: Double], failed: Bool
    ) {
      self.id = id
      self.values = values
      self.captions = captions
      self.numbers = numbers
      self.failed = failed
    }
  }
  public var onSelection: ((Set<String>) -> Void)?
  public var onSelect: ((String) -> Void)?
  public var onSort: ((Int, Bool) -> Void)?
  public var onTest: ((String, Action) -> Void)?
  private var byID: [String: Row] = [:]
  private var ids: [String] = []
  private var configured = ""
  private var selected = ""
  private var emptyMessage = ""
  private var actionsEnabled = false
  private let table = PaxDataTable(
    columns: [
      .init("IP 地址", width: PaxTheme.Workbench.directionColumn, growth: 2),
      .init("入口延迟", width: PaxTheme.Routes.measurementColumn, numeric: true, sortable: true),
      .init("CF 下载速度", width: PaxTheme.Routes.measurementColumn, numeric: true, sortable: true),
      .init(
        "IP 地区", width: PaxTheme.Workbench.directionColumn, sortable: false, contentLayout: .fill),
      .init(
        "操作", width: PaxTheme.Routes.formLabelWidth, growth: 0, alignment: .center, sortable: false,
        contentLayout: .fill),
    ],
    metrics: .init(
      rowHeight: PaxTheme.Table.memberRow, headerHeight: PaxTheme.Table.compactHeader,
      selectionRail: true),
    selection: .multiple)
  public init() {
    super.init(frame: .zero)
    table.translatesAutoresizingMaskIntoConstraints = false
    addSubview(table)
    NSLayoutConstraint.activate([
      table.leadingAnchor.constraint(equalTo: leadingAnchor),
      table.trailingAnchor.constraint(equalTo: trailingAnchor),
      table.topAnchor.constraint(equalTo: topAnchor),
      table.bottomAnchor.constraint(equalTo: bottomAnchor),
    ])
    table.setSortIndicator(column: 1)
    table.onSelect = { [weak self] id in if let id { self?.onSelect?(id) } }
    table.onSelection = { [weak self] ids in self?.onSelection?(ids) }
    table.onSort = { [weak self] column, ascending in self?.onSort?(column, ascending) }
    table.cellProvider = { [weak self] id, column in self?.cell(id, column) }
    table.cellUpdater = { [weak self] view, id, column in
      self?.update(view, id: id, column: column) ?? false
    }
    table.menuForRow = { [weak self] id in self?.menu(id) }
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public func setSort(column: Int, ascending: Bool) {
    table.setSortIndicator(column: column, ascending: ascending)
  }
  public func apply(
    _ rows: [Row], selected: String, configured: String, selectedIPs: Set<String> = [],
    empty: String, actionsEnabled: Bool = false
  ) {
    let values = Dictionary(uniqueKeysWithValues: rows.map { ($0.id, $0) })
    var changed = Set(rows.filter { byID[$0.id] != $0 }.map(\.id))
    if self.configured != configured { changed.formUnion([self.configured, configured]) }
    if self.actionsEnabled != actionsEnabled { changed.formUnion(values.keys) }
    let newIDs = rows.map(\.id)
    let dataChanged = newIDs != ids || !changed.isEmpty

    self.selected = selected
    emptyMessage = empty
    byID = values
    ids = newIDs
    self.configured = configured
    self.actionsEnabled = actionsEnabled
    // Even selection/empty state updates use the light path; unchanged rows are not reloaded.
    table.apply(
      rows: rows.map { .init(id: $0.id, values: $0.values, numericValues: $0.numbers) },
      emptyMessage: empty, selectedID: selected, reloading: dataChanged ? changed : [],
      selectedIDs: selectedIPs.isEmpty ? Set([selected]) : selectedIPs)
  }
  private func cell(_ id: String, _ column: Int) -> NSView? {
    let view: NSView =
      column == 4 ? CFActionCell() : column > 0 && column < 3 ? PaxMetricCell() : CFTextCell()
    _ = update(view, id: id, column: column)
    return view
  }
  private func update(_ view: NSView, id: String, column: Int) -> Bool {
    guard let row = byID[id], row.values.indices.contains(column) else { return false }
    let caption = row.captions.indices.contains(column) ? row.captions[column] : "—"
    if let metric = view as? PaxMetricCell {
      metric.apply(value: row.values[column], caption: caption)
      return true
    }
    if let action = view as? CFActionCell {
      action.button.isEnabled = actionsEnabled
      action.openMenu = { [weak self, weak action] in
        guard let action, let menu = self?.menu(id) else { return }
        menu.popUp(
          positioning: nil, at: NSPoint(x: 0, y: action.button.bounds.maxY), in: action.button)
      }
      return true
    }
    guard let text = view as? CFTextCell else { return false }
    text.apply(
      value: row.values[column], caption: caption, configured: column == 0 && id == configured,
      failed: column == 0 && row.failed)
    return true
  }
  private func menu(_ id: String) -> NSMenu? {
    guard onTest != nil, byID[id] != nil else { return nil }
    let menu = NSMenu()
    menu.autoenablesItems = false
    for (title, action) in [
      ("测试此 IP 延迟", Action.latency), ("测试此 IP 下载", .download), ("延迟和下载都测", .both),
      ("查询此 IP 地区", .location),
    ] {
      let item = NSMenuItem(title: title, action: #selector(test(_:)), keyEquivalent: "")
      item.target = self
      item.representedObject = [id, String(action.rawValue)]
      item.isEnabled = actionsEnabled
      menu.addItem(item)
    }
    return menu
  }
  @objc private func test(_ item: NSMenuItem) {
    guard actionsEnabled, let values = item.representedObject as? [String], values.count == 2,
      byID[values[0]] != nil, let number = Int(values[1]), let action = Action(rawValue: number)
    else { return }
    onTest?(values[0], action)
  }
}

private final class CFTextCell: NSView {
  private let value = PaxText.label("", font: PaxTheme.Typography.label(size: PaxTheme.Table.font))
  private let caption = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private let configured = PaxFeatureTag("当前配置", style: .protocolLabel)
  override init(frame: NSRect) {
    super.init(frame: frame)
    value.lineBreakMode = .byTruncatingTail
    caption.lineBreakMode = .byTruncatingTail
    let heading = NSStackView(views: [value, configured])
    heading.spacing = PaxTheme.Spacing.xs
    value.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    let labels = NSStackView(views: [heading, caption])
    labels.orientation = .vertical
    labels.alignment = .leading
    labels.spacing = PaxTheme.Spacing.xs
    let body = NSStackView(views: [labels])
    body.spacing = PaxTheme.Spacing.xs
    body.translatesAutoresizingMaskIntoConstraints = false
    addSubview(body)
    NSLayoutConstraint.activate([
      body.leadingAnchor.constraint(equalTo: leadingAnchor),
      body.trailingAnchor.constraint(equalTo: trailingAnchor),
      body.centerYAnchor.constraint(equalTo: centerYAnchor),
      body.topAnchor.constraint(greaterThanOrEqualTo: topAnchor),
      body.bottomAnchor.constraint(lessThanOrEqualTo: bottomAnchor),
    ])
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  func apply(value: String, caption: String, configured: Bool, failed: Bool) {
    self.value.stringValue = value
    self.caption.stringValue = caption
    self.caption.textColor = failed ? PaxTheme.Color.error : PaxTheme.Color.textSecondary
    self.caption.isHidden = caption.isEmpty
    self.configured.isHidden = !configured
    toolTip = value + (caption.isEmpty ? "" : " · " + caption)
  }
}

private final class CFActionCell: NSView {
  let button = PaxRouteButton("", symbol: "ellipsis", style: .quiet)
  var openMenu: (() -> Void)?
  override init(frame: NSRect) {
    super.init(frame: frame)
    button.target = self
    button.action = #selector(showMenu)
    button.imagePosition = .imageOnly
    button.setAccessibilityLabel("此 IP 的测试操作")
    button.translatesAutoresizingMaskIntoConstraints = false
    addSubview(button)
    NSLayoutConstraint.activate([
      button.centerXAnchor.constraint(equalTo: centerXAnchor),
      button.centerYAnchor.constraint(equalTo: centerYAnchor),
      button.leadingAnchor.constraint(greaterThanOrEqualTo: leadingAnchor),
      button.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor),
    ])
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  @objc private func showMenu() { openMenu?() }
}
