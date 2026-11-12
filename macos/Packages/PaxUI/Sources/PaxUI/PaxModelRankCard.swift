import AppKit

extension PaxTheme.Dashboard {
  static let rankSummaryHeight: CGFloat = 62
  static let rankCostWhitespace: CGFloat = 0.5
  static let rankShareWhitespace: CGFloat = 0.3
  static let rankTableHeader: CGFloat = 32
  static let rankTabsOffset: CGFloat = 5
  static let rankColors = [
    PaxTheme.Chart.codexSeries, PaxTheme.DashboardColor.green, PaxTheme.Chart.inputSeries,
    PaxTheme.Chart.deepSeekSeries, PaxTheme.DashboardColor.red, PaxTheme.Chart.outputSeries,
  ]
}

/// 概览模型排行的值输入；费用与 Token 的排序和占比由调用方投影。
public struct PaxModelRankModel: Equatable {
  /// 排行固定六个位置，缺少的模型由卡片展示占位。
  public static let rowLimit = 6

  public struct Row: Equatable {
    public var id: String
    public var name: String
    public var clients: String
    public var tokens: String
    public var cost: String
    public var percent: String
    public var fraction: Double
    public init(
      name: String, clients: String, tokens: String, cost: String, percent: String,
      fraction: Double
    ) {
      self.id = name
      self.name = name
      self.clients = clients
      self.tokens = tokens
      self.cost = cost
      self.percent = percent
      self.fraction = fraction
    }
  }
  public var cost: String
  public var caption: String
  public var rows: [Row]
  public var tokenRows: [Row]
  public var empty: String?
  public init(
    cost: String = "—", caption: String = "", rows: [Row] = [], tokenRows: [Row] = [],
    empty: String? = nil
  ) {
    self.cost = cost
    self.caption = caption
    self.rows = rows
    self.tokenRows = tokenRows
    self.empty = empty
  }
}

/// 今日费用摘要与四列模型排行；局部切换口径，刷新时保留选择。
public final class PaxModelRankCard: PaxCard {
  private let heading = PaxCardHeading("模型用量排行", symbol: "trophy")
  private let link = PaxCompactButton("查看全部 ›", style: .text, target: nil, action: nil)
  private let title = PaxText.label("今日模型费用", font: PaxTheme.Typography.caption(), secondary: true)
  private let cost = PaxText.label(
    "", font: PaxTheme.Typography.metric(size: PaxTheme.Dashboard.secondaryMetric, weight: .medium))
  private let caption = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private let tabs = PaxTabs(
    ["费用", "Token"], accessibilityLabels: ["排行按费用", "排行按 Token"])
  private let headers = ["模型", "Token", "费用 ↓", "费用占比"].map {
    PaxText.label($0, font: PaxTheme.Typography.caption(), secondary: true)
  }
  private let divider = NSBox()
  private let headerDivider = NSBox()
  private let rows = (0..<PaxModelRankModel.rowLimit).map { _ in ModelRankRow() }
  private var empty = PaxEmptyState(symbol: "trophy", title: "", caption: "")
  private var emptyTitle = ""
  private var model = PaxModelRankModel()
  private var metric = 0
  public var onOpen: (() -> Void)?
  public override var isFlipped: Bool { true }

  public init() {
    super.init(insets: NSEdgeInsets())
    for v in [
      heading, link, title, cost, caption, tabs, empty, divider, headerDivider,
    ] + headers {
      addSubview(v)
    }
    for header in headers.dropFirst() { header.alignment = .right }
    for line in [divider, headerDivider] { line.boxType = .separator }
    for (index, row) in rows.enumerated() {
      row.showsDivider = index < rows.count - 1
      row.setAccessibilityIdentifier("model-rank-row-\(index + 1)")
      addSubview(row)
    }
    caption.alignment = .center
    tabs.setAccessibilityIdentifier("model-rank-metric")
    tabs.onSelect = { [weak self] index in
      self?.metric = index
      self?.render()
    }
    link.target = self
    link.action = #selector(open)
    link.setAccessibilityIdentifier("model-rank-open")
    setAccessibilityIdentifier("model-rank-card")
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }

  public func apply(_ model: PaxModelRankModel) {
    self.model = model
    render()
  }

  private func render() {
    let showsEmpty = model.empty != nil
    if let text = model.empty, text != emptyTitle {
      empty.removeFromSuperview()
      empty = PaxEmptyState(symbol: "trophy", title: text, caption: "")
      addSubview(empty)
      emptyTitle = text
    }
    empty.isHidden = !showsEmpty
    for v in [title, cost, caption, tabs, divider, headerDivider] + headers {
      v.isHidden = showsEmpty
    }
    cost.stringValue = model.cost
    caption.stringValue = model.caption
    caption.toolTip = model.caption
    tabs.selectedIndex = metric
    headers[1].stringValue = metric == 1 ? "Token ↓" : "Token"
    headers[2].stringValue = metric == 0 ? "费用 ↓" : "费用"
    headers[3].stringValue = metric == 0 ? "费用占比" : "Token 占比"
    let values = metric == 0 ? model.rows : model.tokenRows
    for (index, row) in rows.enumerated() {
      row.isHidden = showsEmpty
      row.apply(values.indices.contains(index) ? values[index] : nil)
    }
    needsLayout = true
  }

  @objc private func open() { onOpen?() }

  public override func layout() {
    super.layout()
    let d = PaxTheme.Dashboard.self
    let inset = d.inset
    let inner = max(1, bounds.width - inset * 2)
    let linkWidth = link.intrinsicContentSize.width
    heading.frame = NSRect(x: inset, y: inset, width: inner - linkWidth, height: d.icon)
    link.frame = NSRect(
      x: bounds.width - inset - linkWidth, y: inset + (d.icon - PaxTheme.Control.compactHeight) / 2,
      width: linkWidth, height: PaxTheme.Control.compactHeight)
    let top = inset + d.icon + PaxTheme.Spacing.regular
    empty.frame = NSRect(x: inset, y: top, width: inner, height: bounds.height - top - inset)
    let tabSize = tabs.intrinsicContentSize
    let numberHeight = ceil(d.secondaryMetric * 1.1) + PaxTheme.Spacing.xs
    let summaryHeight = d.factLine + numberHeight
    tabs.frame = NSRect(
      x: bounds.width - inset - tabSize.width,
      y: top + (summaryHeight - tabSize.height) / 2 - d.rankTabsOffset,
      width: tabSize.width, height: tabSize.height)
    let gap = PaxTheme.Spacing.tight
    let captionWidth = ceil(caption.intrinsicContentSize.width)
    let summaryWidth = max(1, tabs.frame.minX - PaxTheme.Spacing.regular - inset)
    let titleWidth = ceil(title.intrinsicContentSize.width)
    cost.font = PaxTheme.Typography.metric(size: d.secondaryMetric, weight: .medium)
    let naturalWidth = max(1, cost.intrinsicContentSize.width)
    let availableWidth = max(titleWidth, summaryWidth - captionWidth - gap * 2)
    let metricWidth = min(max(titleWidth, ceil(naturalWidth)), availableWidth)
    title.frame = NSRect(x: inset, y: top, width: metricWidth, height: d.factLine)
    let fontSize = min(d.secondaryMetric, d.secondaryMetric * metricWidth / naturalWidth)
    cost.font = PaxTheme.Typography.metric(size: fontSize, weight: .medium)
    cost.frame = NSRect(
      x: inset, y: title.frame.maxY, width: metricWidth, height: numberHeight)
    let captionX = inset + metricWidth + gap
    caption.frame = NSRect(
      x: captionX, y: top + (summaryHeight - d.factLine) / 2,
      width: max(1, tabs.frame.minX - gap - captionX), height: d.factLine)
    let tableTop = top + d.rankSummaryHeight
    divider.frame = NSRect(x: inset, y: tableTop, width: inner, height: PaxTheme.Layout.cardBorder)
    let columns = ModelRankColumns(
      width: inner, costTextWidth: rows.map(\.costNaturalWidth).max() ?? 0,
      percentTextWidth: rows.map(\.percentNaturalWidth).max() ?? 0,
      costHeaderWidth: ceil(headers[2].intrinsicContentSize.width),
      percentHeaderWidth: ceil(headers[3].intrinsicContentSize.width))
    for (index, header) in headers.enumerated() {
      header.frame = NSRect(
        x: inset + columns.origins[index], y: tableTop + PaxTheme.Spacing.tight,
        width: columns.widths[index], height: d.factLine)
    }
    let listTop = tableTop + d.rankTableHeader
    headerDivider.frame = NSRect(
      x: inset, y: listTop, width: inner, height: PaxTheme.Layout.cardBorder)
    let slot = max(1, (bounds.height - inset - listTop) / CGFloat(PaxModelRankModel.rowLimit))
    let nameWidth = min(rows.map(\.nameNaturalWidth).max() ?? 0, columns.widths[0] / 2)
    let tokenWidth = rows.map(\.tokenNaturalWidth).max() ?? 0
    let tokenStart = columns.origins[1] + columns.widths[1] - tokenWidth
    let barEnd = columns.widths[0] + max(0, tokenStart - columns.widths[0]) / 2
    for (index, row) in rows.enumerated() {
      row.columns = columns
      row.nameWidth = nameWidth
      row.barEnd = barEnd
      row.barColor = d.rankColors[index % d.rankColors.count]
      row.frame = NSRect(x: inset, y: listTop + CGFloat(index) * slot, width: inner, height: slot)
    }
  }
}

/// 表头与正文共用列宽；模型列保留最多空间，其余数值右对齐。
private struct ModelRankColumns {
  let origins: [CGFloat]
  let widths: [CGFloat]
  init(
    width: CGFloat, costTextWidth: CGFloat, percentTextWidth: CGFloat,
    costHeaderWidth: CGFloat, percentHeaderWidth: CGFloat
  ) {
    let gap = PaxTheme.Spacing.tight
    let usable = max(1, width - gap * 3)
    let tokenWidth = usable * 0.18
    let costWidth = max(
      costHeaderWidth,
      costTextWidth + max(0, usable * 0.20 - costTextWidth) * PaxTheme.Dashboard.rankCostWhitespace)
    let percentWidth = max(
      percentHeaderWidth,
      percentTextWidth + max(0, usable * 0.19 - percentTextWidth)
        * PaxTheme.Dashboard.rankShareWhitespace)
    widths = [
      max(1, usable - tokenWidth - costWidth - percentWidth), tokenWidth, costWidth, percentWidth,
    ]
    origins = [0, widths[0] + gap, widths[0] + widths[1] + gap * 2, usable - widths[3] + gap * 3]
  }
}

private final class ModelRankRow: NSView {
  private let name = PaxText.tableLabel("", identity: true)
  private let tokens = PaxText.tableLabel("", numeric: true)
  private let cost = PaxText.tableLabel("", numeric: true)
  private let percent = PaxText.tableLabel("", numeric: true)
  private let track = ModelRankTrack()
  private let divider = NSBox()
  var showsDivider = true { didSet { divider.isHidden = !showsDivider } }
  var columns: ModelRankColumns? {
    didSet { needsLayout = true }
  }
  var nameWidth: CGFloat = 0 { didSet { if nameWidth != oldValue { needsLayout = true } } }
  var barEnd: CGFloat = 0 { didSet { if barEnd != oldValue { needsLayout = true } } }
  var barColor: NSColor = PaxTheme.Chart.codexSeries {
    didSet { track.color = barColor }
  }
  var nameNaturalWidth: CGFloat { ceil(name.intrinsicContentSize.width) }
  var tokenNaturalWidth: CGFloat { ceil(tokens.intrinsicContentSize.width) }
  var costNaturalWidth: CGFloat { ceil(cost.intrinsicContentSize.width) }
  var percentNaturalWidth: CGFloat { ceil(percent.intrinsicContentSize.width) }
  override var isFlipped: Bool { true }

  init() {
    super.init(frame: .zero)
    name.lineBreakMode = .byTruncatingTail
    for field in [tokens, cost, percent] { field.alignment = .right }
    divider.boxType = .separator
    for v in [name, tokens, cost, percent, track, divider] { addSubview(v) }
    setAccessibilityElement(true)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  func apply(_ value: PaxModelRankModel.Row?) {
    name.stringValue = value?.name ?? "暂无模型"
    name.toolTip = value?.name
    name.font = PaxTheme.Typography.label(
      size: PaxTheme.Routes.nameFont, weight: value == nil ? .regular : .medium)
    tokens.stringValue = value?.tokens ?? "—"
    cost.stringValue = value?.cost ?? "—"
    percent.stringValue = value?.percent ?? "—"
    for field in [name, tokens, cost, percent] {
      field.textColor = value == nil ? PaxTheme.Color.textSecondary : PaxTheme.Table.text
    }
    track.isHidden = value == nil
    track.fraction = value?.fraction ?? 0
    if let value {
      setAccessibilityLabel(
        "\(value.name)，\(value.clients)，\(value.tokens) Token，\(value.cost)，\(value.percent)")
    } else {
      setAccessibilityLabel("暂无模型")
    }
    needsLayout = true
  }

  override func layout() {
    super.layout()
    let line = PaxTheme.RTT.textLine
    let top = max(0, (bounds.height - line) / 2)
    guard let columns else { return }
    for (index, field) in [name, tokens, cost, percent].enumerated() {
      field.frame = NSRect(
        x: columns.origins[index], y: top,
        width: index == 0 ? nameWidth : columns.widths[index], height: line)
    }
    let barX = nameWidth + PaxTheme.Spacing.tight
    let barWidth = max(0, barEnd - barX)
    let barY = (bounds.height - PaxTheme.Chart.shareBarHeight) / 2
    track.frame = NSRect(x: barX, y: barY, width: barWidth, height: PaxTheme.Chart.shareBarHeight)
    divider.frame = NSRect(
      x: 0, y: bounds.height - PaxTheme.Layout.cardBorder,
      width: bounds.width, height: PaxTheme.Layout.cardBorder)
  }
}

private final class ModelRankTrack: NSView {
  var fraction: Double = 0 { didSet { needsDisplay = true } }
  var color: NSColor = PaxTheme.Chart.codexSeries { didSet { needsDisplay = true } }
  override func draw(_ dirtyRect: NSRect) {
    effectiveAppearance.performAsCurrentDrawingAppearance {
      PaxTheme.Color.navigationHover.setFill()
      NSBezierPath(roundedRect: bounds, xRadius: bounds.height / 2, yRadius: bounds.height / 2)
        .fill()
      let width = bounds.width * CGFloat(min(1, max(0, fraction)))
      guard width > 0 else { return }
      let rect = NSRect(x: 0, y: 0, width: width, height: bounds.height)
      let shape = NSBezierPath(
        roundedRect: rect, xRadius: bounds.height / 2, yRadius: bounds.height / 2)
      NSGraphicsContext.saveGraphicsState()
      shape.addClip()
      color.setFill()
      shape.fill()
      PaxShareBar.stripes(in: rect)
      NSGraphicsContext.restoreGraphicsState()
    }
  }
}
