import AppKit

/// CF 候选与导入预览使用共同数据表；单选仅表示待应用项。
public final class PaxCandidateTable: NSView {
  public struct Row {
    public let id: String
    public let values: [String]
    public let statusColor: NSColor
    public let statusSymbol: String?
    public let numericValues: [Int: Double]
    public init(
      id: String, values: [String], statusColor: NSColor = PaxTheme.Table.muted,
      statusSymbol: String? = nil, numericValues: [Int: Double] = [:]
    ) {
      self.id = id
      self.values = values
      self.statusColor = statusColor
      self.statusSymbol = statusSymbol
      self.numericValues = numericValues
    }
  }
  public var onSelect: ((String) -> Void)?
  private let data: PaxDataTable
  private let titles: [String]
  private let choices: Bool
  private var rows: [Row] = []
  private var selected: String?
  private var configured: String?
  public init(titles: [String], choices: Bool = false) {
    self.titles = titles
    self.choices = choices
    var columns: [PaxDataTable.Column] = []
    if choices {
      columns.append(
        .init(
          "", width: PaxTheme.Routes.choiceWidth, growth: 0, alignment: .center,
          inset: PaxTheme.Spacing.xs, sortable: false))
    }
    columns += titles.enumerated().map { index, title in
      .init(
        title,
        width: index == 0 ? PaxTheme.Workbench.identityColumn : PaxTheme.Routes.formLabelWidth,
        numeric: choices && index == 1, growth: index == 0 ? 3 : 1,
        sortable: choices && (index == 0 || index == 1))
    }
    data = PaxDataTable(
      columns: columns,
      metrics: .init(
        rowHeight: PaxTheme.Table.compactRow, headerHeight: PaxTheme.Table.compactHeader),
      selection: choices ? .single : .none)
    super.init(frame: .zero)
    data.cellProvider = { [weak self] id, column in self?.cell(id: id, column: column) }
    data.onSelect = { [weak self] id in if let id { self?.onSelect?(id) } }
    data.translatesAutoresizingMaskIntoConstraints = false
    addSubview(data)
    NSLayoutConstraint.activate([
      data.leadingAnchor.constraint(equalTo: leadingAnchor),
      data.trailingAnchor.constraint(equalTo: trailingAnchor),
      data.topAnchor.constraint(equalTo: topAnchor),
      data.bottomAnchor.constraint(equalTo: bottomAnchor),
    ])
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public func apply(rows: [Row], selected: String? = nil, configured: String? = nil) {
    self.rows = rows
    self.selected = selected
    self.configured = configured
    data.apply(
      rows: rows.map { row in
        .init(
          id: row.id, values: choices ? [""] + row.values : row.values,
          numericValues: Dictionary(
            uniqueKeysWithValues: row.numericValues.map { ($0.key + (choices ? 1 : 0), $0.value) }))
      }, selectedID: selected)
  }
  private func cell(id: String, column: Int) -> NSView? {
    guard let row = rows.first(where: { $0.id == id }) else { return nil }
    if choices && column == 0 {
      let radio = NSButton(title: "", target: self, action: #selector(choose(_:)))
      radio.setButtonType(.radio)
      radio.identifier = .init(id)
      radio.state = id == selected ? .on : .off
      radio.focusRingType = .none
      radio.setAccessibilityLabel("选择 " + id)
      return radio
    }
    let index = column - (choices ? 1 : 0)
    let value = row.values.indices.contains(index) ? row.values[index] : "—"
    let label = PaxText.label(
      value,
      font: index == 1 && choices
        ? PaxTheme.Typography.metric(size: PaxTheme.Table.font)
        : PaxTheme.Typography.label(size: PaxTheme.Table.font))
    label.lineBreakMode = .byTruncatingTail
    label.textColor = index == titles.count - 1 ? row.statusColor : PaxTheme.Table.text
    label.alignment = choices && index == 1 ? .right : .left
    if choices && index == 0 && id == configured {
      let stack = NSStackView(views: [label, PaxFeatureTag("当前配置", style: .protocolLabel)])
      stack.spacing = PaxTheme.Spacing.tight
      label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
      return stack
    }
    if let symbol = row.statusSymbol, index == titles.count - 1 {
      let icon = PaxIcon.view(symbol, size: PaxTheme.Table.font)
      icon.contentTintColor = row.statusColor
      let stack = NSStackView(views: [icon, label])
      stack.spacing = PaxTheme.Spacing.xs
      return stack
    }
    return label
  }
  @objc private func choose(_ sender: NSButton) {
    if let id = sender.identifier?.rawValue { onSelect?(id) }
  }
}
