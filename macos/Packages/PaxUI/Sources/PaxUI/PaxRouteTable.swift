import AppKit

/// 线路目录的值投影；PaxUI 不解析协议、不测速、不持有运行状态。
public struct PaxRouteTableRow: Equatable {
  public let id: String
  public let name: String
  public let connection: String
  public let certificateWarning: String?
  public let cf: String?
  public let chained: Bool
  public let selected: Bool
  public let current: Bool
  public var selectionLabel: String { current ? "当前线路" : selected ? "已选线路" : "" }
  public var emphasized: Bool { current || selected }
  public let address: String
  public let addressCaption: String
  public let latency: String
  public let latencyCaption: String
  public let speed: String
  public let latencyColor: NSColor
  public init(
    id: String, name: String, connection: String, cf: String? = nil, chained: Bool = false,
    current: Bool = false, selected: Bool = false, certificateWarning: String? = nil,
    address: String = "未获取", addressCaption: String = "",
    latency: String = "—", latencyCaption: String = "",
    speed: String = "—",
    latencyColor: NSColor = PaxTheme.Color.text
  ) {
    self.id = id
    self.name = name
    self.connection = connection
    self.certificateWarning = certificateWarning
    self.cf = cf
    self.chained = chained
    self.current = current
    self.selected = selected
    self.addressCaption = addressCaption
    self.address = address
    self.latency = latency
    self.latencyCaption = latencyCaption
    self.speed = speed
    self.latencyColor = latencyColor
  }
}

/// 线路专用值投影与单元格；表格布局和交互由 PaxDataTable 负责。
public final class PaxRouteTable: NSView {
  public var menuForRow: ((String) -> NSMenu)?
  public var onSort: ((Int, Bool) -> Void)?
  public var onActivate: ((String) -> Void)?
  public var onScrollPosition: ((Bool) -> Void)?
  private var rows: [PaxRouteTableRow] = []
  private let data: PaxDataTable
  private var initialAppearance = true
  public init() {
    let c = PaxTheme.Routes.self
    let titles = ["", "线路", "线路特性", "出口地址", "延迟", "下载速度"]
    let symbols: [String?] = [
      nil, "point.3.connected.trianglepath.dotted", "tag", "globe", "timer", "arrow.down",
    ]
    let widths = [
      c.ordinalWidth, c.nameMinimum, c.featureWidth, c.valueWidth, c.valueWidth, c.valueWidth,
    ]
    data = PaxDataTable(
      columns: titles.enumerated().map { index, title in
        .init(
          title, width: widths[index], growth: c.columnShares[index],
          alignment: index == 1 || index == 2 ? .left : .center,
          symbol: symbols[index], inset: index == 0 ? 0 : PaxTheme.Table.inset,
          sortable: index > 0)
      }, metrics: .init(rounded: false), selection: .browse, scrollMode: .fixedHeader)
    super.init(frame: .zero)
    data.setTableIdentifier("routes-table")
    data.contextOnReturn = true
    data.onScrollPosition = { [weak self] in self?.onScrollPosition?($0) }
    data.onActivate = { [weak self] id in self?.onActivate?(id) }
    data.cellProvider = { [weak self] id, column in self?.cell(id: id, index: column) }
    data.rowAppearance = { [weak self] id in
      self?.rows.first(where: { $0.id == id })?.emphasized == true ? .current : .normal
    }
    data.menuForRow = { [weak self] id in self?.menuForRow?(id) }
    data.onSort = { [weak self] column, ascending in self?.onSort?(column, ascending) }
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
  public func showFirstRowIfNeeded() {
    guard initialAppearance else { return }
    initialAppearance = false
    layoutSubtreeIfNeeded()
    data.revealFirstRow()
  }
  public func apply(_ rows: [PaxRouteTableRow], resetScroll: Bool = false) {
    guard self.rows != rows || resetScroll else { return }
    let previous = Dictionary(
      uniqueKeysWithValues: self.rows.enumerated().map { ($0.element.id, $0.offset) })
    let changed = Set(
      rows.enumerated().compactMap { index, row -> String? in
        guard let oldIndex = previous[row.id] else { return row.id }
        return oldIndex != index || self.rows[oldIndex] != row ? row.id : nil
      })
    self.rows = rows
    data.apply(
      rows: rows.map { .init(id: $0.id, values: []) }, emptyMessage: "", reloading: changed)
    if resetScroll {
      layoutSubtreeIfNeeded()
      data.scrollToTop()
    }
  }
  public func reveal(_ id: String) {
    data.apply(rows: rows.map { .init(id: $0.id, values: []) }, selectedID: id, reloading: [])
    _ = data.revealRow(id)
  }
  public func setSortIndicator(column: Int?, ascending: Bool = true) {
    data.setSortIndicator(column: column, ascending: ascending)
  }
  private func cell(id: String, index: Int) -> NSView? {
    guard let ordinal = rows.firstIndex(where: { $0.id == id }) else { return nil }
    let item = rows[ordinal]
    let content: NSView
    if index == 1 {
      let title = PaxText.label(
        item.name,
        font: PaxTheme.Typography.label(
          size: PaxTheme.Routes.nameFont, weight: item.emphasized ? .semibold : .medium))
      title.lineBreakMode = .byTruncatingTail
      title.maximumNumberOfLines = 1
      title.textColor = item.emphasized ? PaxTheme.Routes.selectedBlue : PaxTheme.Routes.text
      let name = NSStackView(views: [title])
      name.spacing = PaxTheme.Spacing.xs
      if item.emphasized {
        name.insertArrangedSubview(
          PaxFeatureTag(item.selectionLabel, symbol: "checkmark", tone: .blue, style: .current),
          at: 0)
      }
      let meta = PaxText.label(
        item.connection, font: PaxTheme.Typography.caption(), secondary: true)
      let metaRow = NSStackView(views: [meta])
      metaRow.orientation = .horizontal
      metaRow.alignment = .centerY
      metaRow.spacing = PaxTheme.Spacing.xs
      if let warning = item.certificateWarning {
        metaRow.addArrangedSubview(
          PaxFeatureTag(warning, symbol: "exclamationmark.shield", tone: .warning))
      }
      meta.lineBreakMode = .byTruncatingTail
      meta.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
      let stack = NSStackView(views: [name, metaRow])
      stack.orientation = .vertical
      stack.alignment = .leading
      stack.spacing = PaxTheme.Spacing.xs
      name.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
      metaRow.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
      content = stack
    } else if index == 2 {
      let stack = NSStackView()
      stack.orientation = .vertical
      stack.alignment = .leading
      stack.spacing = PaxTheme.Spacing.xs
      if let cf = item.cf {
        stack.addArrangedSubview(PaxFeatureTag(cf, symbol: "cloud", tone: .blue))
      }
      if item.chained {
        stack.addArrangedSubview(PaxFeatureTag("链式代理", symbol: "network", tone: .purple))
      }
      content = stack
    } else if index == 3 && !item.addressCaption.isEmpty {
      let value = PaxText.label(item.address, font: PaxTheme.Typography.tableValue)
      let caption = PaxText.label(
        item.addressCaption, font: PaxTheme.Typography.caption(), secondary: true)
      value.alignment = .center
      caption.alignment = .center
      value.lineBreakMode = .byTruncatingTail
      caption.lineBreakMode = .byTruncatingTail
      let stack = NSStackView(views: [value, caption])
      stack.orientation = .vertical
      stack.alignment = .centerX
      stack.spacing = PaxTheme.Spacing.xs
      value.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
      caption.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
      stack.toolTip = item.address + " · " + item.addressCaption
      content = stack
    } else if index == 4 && !item.latencyCaption.isEmpty {
      let value = PaxText.label(
        item.latency, font: PaxTheme.Typography.metric(size: PaxTheme.Routes.bodyFont))
      let caption = PaxText.label(
        item.latencyCaption, font: PaxTheme.Typography.caption(), secondary: true)
      value.alignment = .center
      caption.alignment = .center
      value.textColor = item.latencyColor
      value.lineBreakMode = .byTruncatingTail
      caption.lineBreakMode = .byTruncatingTail
      let stack = NSStackView(views: [value, caption])
      stack.orientation = .vertical
      stack.alignment = .centerX
      stack.spacing = PaxTheme.Spacing.xs
      value.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
      caption.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
      stack.toolTip = item.latency + " · " + item.latencyCaption
      content = stack
    } else {
      let text =
        index == 0
        ? String(ordinal + 1) : index == 3 ? item.address : index == 4 ? item.latency : item.speed
      let label = PaxText.label(
        text, font: PaxTheme.Typography.metric(size: PaxTheme.Routes.bodyFont))
      label.alignment = .center
      label.textColor =
        index == 4
        ? item.latencyColor
        : index == 0
          ? item.emphasized ? PaxTheme.Routes.selectedBlue : PaxTheme.Routes.muted
          : PaxTheme.Routes.text
      label.lineBreakMode = .byTruncatingTail
      if index == 4 {
        label.toolTip = text + "；延迟 ≤200 ms 绿色，200–500 ms 橙色，>500 ms 红色；历史与未知结果不分级。"
      } else if index == 5 {
        label.toolTip = text + "；正文平均下载速度，使用十进制字节率。"
      }
      content = label
    }
    content.setAccessibilityLabel(
      [
        item.name, item.connection, item.certificateWarning ?? "", item.selectionLabel,
        item.latency, item.speed,
      ].filter {
        !$0.isEmpty
      }.joined(separator: "，"))
    return content
  }
}
