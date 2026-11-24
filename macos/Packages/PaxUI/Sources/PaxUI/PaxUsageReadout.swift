import AppKit

extension PaxTheme {
  public enum UsageReadout {
    public static let width = TrafficDetail.hoverWidth
    public static let mainNumber: CGFloat = 31
    static let surface = PaxTheme.Color.adaptive("PaxUsageSurface", light: 0xffffff, dark: 0x292a2d)
    static let border = PaxTheme.Color.adaptive("PaxUsageBorder", light: 0xd5dcd5, dark: 0x3c3e43)
    static let uploadBand = Color.adaptive("PaxUsageUploadBand", light: 0xf7f3ea, dark: 0x352f25)
    static let downloadBand = Color.adaptive(
      "PaxUsageDownloadBand", light: 0xebf4ee, dark: 0x23372e)
    static let uploadInk = Color.adaptive("PaxUsageUploadInk", light: 0x8b642d, dark: 0xeac489)
    static let downloadInk = Color.adaptive("PaxUsageDownloadInk", light: 0x326f5c, dark: 0x8dd6b8)
    static let detailInk = Color.adaptive("PaxUsageDetailInk", light: 0x616b65, dark: 0xbac4bd)
    static let bandValueSize: CGFloat = 16
    static let detailSize: CGFloat = 11
    static let directionWidth: CGFloat = 72
    static let rulerHeight: CGFloat = 16
    public static let rankRow: CGFloat = 38
    public static let rankHeader: CGFloat = 32
    public static let columnShares: [CGFloat] = [0.38, 0.18, 0.18, 0.26]
    public static let trafficColumn: CGFloat = 184
  }
}

/// Value-only usage readout: one surface, a primary metric and ranked evidence.
public final class PaxUsageReadout: NSView {
  public struct Rank {
    public var id: String
    public var name: String
    public var upload: String
    public var download: String
    public var total: String
    public init(id: String, name: String, upload: String, download: String, total: String) {
      self.id = id
      self.name = name
      self.upload = upload
      self.download = download
      self.total = total
    }
  }
  public var onSelect: ((Int) -> Void)?
  private let status = PaxText.label(
    "", font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  private let tabs: PaxTabs
  private var ranks: [String: Rank] = [:]
  private let timestamp = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private let table: PaxDataTable
  private let tableHeight: NSLayoutConstraint
  public init(
    title: String, updated: String, scope: String, progress: NSView, ranking: String,
    dimensions: [String], columns: [String]
  ) {
    let theme = PaxTheme.UsageReadout.self
    tabs = PaxTabs(dimensions, style: .tabs)
    table = PaxDataTable(
      columns: columns.enumerated().map { index, title in
        .init(
          title, width: theme.width * theme.columnShares[index], numeric: index > 0,
          growth: theme.columnShares[index], alignment: index == 0 ? .left : .right,
          inset: index == 0 ? 0 : PaxTheme.Spacing.xs, sortable: false)
      },
      metrics: .init(
        rowHeight: theme.rankRow, headerHeight: theme.rankHeader,
        rounded: false, background: theme.surface, headerBackground: theme.surface),
      selection: .none)
    tableHeight = table.heightAnchor.constraint(equalToConstant: theme.rankHeader + theme.rankRow)
    super.init(frame: .zero)

    let name = PaxText.label(
      title,
      font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyTitle, weight: .medium))
    name.lineBreakMode = .byTruncatingTail
    name.toolTip = title
    name.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    timestamp.stringValue = updated
    timestamp.alignment = .right
    let heading = row([name, NSView(), timestamp])
    let route = column([heading, note(scope)], gap: PaxTheme.Spacing.xs)
    let rankingHeading = row([sectionTitle(ranking), NSView(), tabs])
    let rankingSection = column([rankingHeading, table], gap: PaxTheme.Spacing.tight)
    let stack = column(
      [route, separator(), progress, separator(), rankingSection, status],
      gap: PaxTheme.Spacing.medium)
    stack.translatesAutoresizingMaskIntoConstraints = false
    addSubview(stack)
    NSLayoutConstraint.activate([
      widthAnchor.constraint(equalToConstant: theme.width),
      stack.leadingAnchor.constraint(equalTo: leadingAnchor),
      stack.trailingAnchor.constraint(equalTo: trailingAnchor),
      stack.topAnchor.constraint(equalTo: topAnchor),
      stack.bottomAnchor.constraint(equalTo: bottomAnchor),
      tableHeight,
    ])
    table.cellProvider = { [weak self] id, index in self?.rankCell(id, column: index) }
    tabs.onSelect = { [weak self] in self?.onSelect?($0) }
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }

  public func apply(updated: String, status: String, rows: [Rank], emptyText: String) {
    let theme = PaxTheme.UsageReadout.self
    tableHeight.constant = theme.rankHeader + theme.rankRow * CGFloat(max(1, rows.count))
    timestamp.stringValue = updated
    self.status.stringValue = status
    ranks = Dictionary(uniqueKeysWithValues: rows.map { ($0.id, $0) })
    table.apply(
      rows: rows.map { .init(id: $0.id, values: [$0.name, $0.upload, $0.download, $0.total]) },
      emptyMessage: emptyText)
  }

  private func rankCell(_ id: String, column index: Int) -> NSView? {
    guard let rank = ranks[id] else { return nil }
    let text = [rank.name, rank.upload, rank.download, rank.total][index]
    if index != 3 {
      let label = PaxText.label(
        text,
        font: index == 0
          ? PaxTheme.Typography.label(size: PaxTheme.Table.font)
          : PaxTheme.Typography.metric(size: PaxTheme.Table.font))
      label.alignment = index == 0 ? .left : .right
      label.lineBreakMode = .byTruncatingTail
      label.toolTip = text
      return label
    }
    let labels = text.components(separatedBy: "\n").enumerated().map { index, value in
      let label = PaxText.label(
        value,
        font: index == 0
          ? PaxTheme.Typography.metric(size: PaxTheme.Table.font, weight: .medium)
          : PaxTheme.Typography.caption(), secondary: index > 0)
      label.alignment = .right
      return label
    }
    let stack = column(labels, gap: PaxTheme.Spacing.xs / 2)
    stack.alignment = .trailing
    return stack
  }
  private func sectionTitle(_ text: String) -> NSTextField {
    PaxText.label(
      text, font: PaxTheme.Typography.label(size: PaxTheme.Routes.nameFont, weight: .medium))
  }
  private func note(_ text: String) -> NSTextField {
    PaxText.label(text, font: PaxTheme.Typography.caption(), secondary: true)
  }
  private func separator() -> NSView {
    let line = NSBox()
    line.boxType = .separator
    return line
  }
  private func row(_ views: [NSView]) -> NSStackView {
    let stack = NSStackView(views: views)
    stack.alignment = .centerY
    stack.spacing = PaxTheme.Spacing.tight
    return stack
  }
  private func column(_ views: [NSView], gap: CGFloat) -> NSStackView {
    let stack = NSStackView(views: views)
    stack.orientation = .vertical
    stack.alignment = .leading
    stack.spacing = gap
    for view in views { view.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true }
    return stack
  }
}
