import AppKit

/// 线路卡的值输入：只承载已显示的线路身份与演示历史，不执行网络命令。
public struct PaxRouteQualityItem: Equatable {
  public let id: String
  public let name: String
  public let protocolName: String
  public let source: String
  public let samples: [PaxRTTSample]
  public init(
    id: String, name: String, protocolName: String, source: String, samples: [PaxRTTSample]
  ) {
    self.id = id
    self.name = name
    self.protocolName = protocolName
    self.source = source
    self.samples = samples
  }
}

/// 六个固定行位；缺少记录时画分隔线，空位不会成为可点击线路。
public final class PaxRouteListCard: PaxCard {
  private let tooltip: PaxChartTooltipCoordinator?
  public var onSelect: ((String) -> Void)?
  public var onAdd: (() -> Void)?
  private let dividers = PaxRowDividers()
  private let heading = PaxCardHeading("线路列表", symbol: "network")
  private var rows: [RouteQualityRow] = []
  private let empty = PaxEmptyState(symbol: "network", title: "暂无线路", caption: "添加订阅后将在这里显示")
  private let add = PaxButton(.secondary, title: "添加订阅")
  public override var isFlipped: Bool { true }
  public init(tooltip: PaxChartTooltipCoordinator? = nil) {
    self.tooltip = tooltip
    super.init(insets: NSEdgeInsets())
    addSubview(dividers)
    addSubview(heading)
    addSubview(empty)
    addSubview(add)
    add.target = self
    add.action = #selector(addSubscription)
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
  public func apply(items: [PaxRouteQualityItem], selectedID: String, canSelect: Bool = true) {
    heading.trailing =
      "共 \(items.count) 条   可用 \(items.filter { $0.samples.last?.milliseconds != nil }.count) 条"
    let visible = Array(items.prefix(PaxTheme.Dashboard.routeSlots))
    if rows.map(\.item) != visible {
      for row in rows { row.removeFromSuperview() }
      rows = visible.map { item in
        let row = RouteQualityRow(item: item)
        row.history.tooltipCoordinator = tooltip
        row.onSelect = { [weak self] in self?.onSelect?(item.id) }
        row.history.onSample = { [weak self, weak row] _ in
          if row?.isEnabled == true { self?.onSelect?(item.id) }
        }
        addSubview(row)
        return row
      }
    }
    for row in rows {
      row.selected = row.item.id == selectedID
      row.isEnabled = canSelect
    }
    empty.isHidden = !rows.isEmpty
    add.isHidden = !rows.isEmpty
    needsLayout = true
    needsDisplay = true
  }
  public override func layout() {
    super.layout()
    let d = PaxTheme.Dashboard.self
    let inset = d.routeListInset
    dividers.frame = bounds
    dividers.horizontalInset = inset + PaxTheme.Spacing.tight
    dividers.positions = (rows.count..<d.routeSlots).map {
      d.routeHeader + CGFloat($0) * (d.routeRow + d.routeGap)
    }
    heading.frame = NSRect(x: inset, y: inset, width: bounds.width - inset * 2, height: d.icon)
    for (i, row) in rows.enumerated() {
      row.frame = NSRect(
        x: inset, y: d.routeHeader + CGFloat(i) * (d.routeRow + d.routeGap),
        width: bounds.width - inset * 2, height: d.routeRow)
    }
    empty.frame = NSRect(
      x: inset, y: d.routeHeader, width: bounds.width - inset * 2,
      height: PaxTheme.Layout.emptyState)
    add.frame = NSRect(
      x: (bounds.width - add.intrinsicContentSize.width) / 2, y: empty.frame.maxY,
      width: add.intrinsicContentSize.width, height: PaxTheme.Control.height)
  }
  @objc private func addSubscription() { onAdd?() }
}

@MainActor
final class RTTNumber: NSView {
  private let large: Bool
  private var value: Double?
  var compactFontSize = PaxTheme.Dashboard.routeMetaFont
  init(large: Bool) {
    self.large = large
    super.init(frame: .zero)
    setAccessibilityElement(true)
    setAccessibilityRole(.staticText)
  }
  @available(*, unavailable)
  required init?(coder: NSCoder) { fatalError("代码构建") }
  override var isFlipped: Bool { true }
  func apply(value: Double?) {
    self.value = value
    setAccessibilityLabel(value.map { "\(Int($0)) ms" } ?? "暂无测量")
    needsDisplay = true
  }
  override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }
  override func draw(_ dirtyRect: NSRect) {
    let size = large ? PaxTheme.Dashboard.largeMetric : compactFontSize
    let font = PaxTheme.Typography.metric(size: size, weight: .medium)
    let text = value.map { String(Int($0)) } ?? "—"
    let textSize = (text as NSString).size(withAttributes: [.font: font])
    let unitFont = PaxTheme.Typography.label(
      size: large ? PaxTheme.RTT.unit : compactFontSize)
    let unitGap = large ? PaxTheme.Spacing.tight : PaxTheme.RTT.rowGap
    let unitWidth =
      value == nil ? 0 : ("ms" as NSString).size(withAttributes: [.font: unitFont]).width + unitGap
    let originX = large ? 0 : max(0, bounds.width - textSize.width - unitWidth)
    effectiveAppearance.performAsCurrentDrawingAppearance {
      (text as NSString).draw(
        in: NSRect(x: originX, y: 0, width: textSize.width, height: bounds.height),
        withAttributes: [
          .font: font,
          .foregroundColor: value.map {
            large ? PaxRTTColor.solid($0) : PaxRTTColor.color($0, role: .text)
          } ?? PaxTheme.Color.textSecondary,
        ])
      if value != nil {
        ("ms" as NSString).draw(
          at: NSPoint(
            x: originX + textSize.width + unitGap,
            y: font.ascender - unitFont.ascender),
          withAttributes: [
            .font: PaxTheme.Typography.label(
              size: large ? PaxTheme.RTT.unit : compactFontSize),
            .foregroundColor: value.map {
              large ? PaxRTTColor.solid($0) : PaxRTTColor.color($0, role: .text)
            } ?? PaxTheme.Color.textSecondary,
          ])
      }
    }
  }
}

@MainActor
private final class RouteQualityRow: NSButton {
  let item: PaxRouteQualityItem
  var onSelect: (() -> Void)?
  var selected = false {
    didSet {
      needsDisplay = true
      setAccessibilityValue(selected ? "已选中" : "未选中")
    }
  }
  private var hovered = false
  let history = PaxRTTHistoryView()
  private let protocolLabel = PaxText.label(
    "", font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.routeProtocolFont), secondary: true
  )
  private let routeIcon = PaxIcon.view(
    "network", size: PaxTheme.Dashboard.iconGlyph, weight: .light)
  private let name: NSTextField
  private let number = RTTNumber(large: false)
  init(item: PaxRouteQualityItem) {
    self.item = item
    name = PaxText.label(
      item.name, font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.routeNameFont))
    super.init(frame: .zero)
    name.lineBreakMode = .byTruncatingTail
    name.toolTip = item.name
    isBordered = false
    focusRingType = .none
    title = ""
    target = self
    action = #selector(pick)
    protocolLabel.stringValue = item.protocolName
    number.apply(value: item.samples.last?.milliseconds)
    history.samples = item.samples
    for view in [routeIcon, protocolLabel, name, number, history] { addSubview(view) }
    setAccessibilityLabel(
      item.protocolName + "，" + item.name + "，" + (item.samples.last?.description ?? "未测"))
    setAccessibilityIdentifier("quality-row-" + item.id)
  }
  @available(*, unavailable)
  required init?(coder: NSCoder) { fatalError("代码构建") }
  override var isFlipped: Bool { true }
  func height(for width: CGFloat) -> CGFloat {
    PaxTheme.Dashboard.routeRow
  }
  override func layout() {
    super.layout()
    let inset = PaxTheme.RTT.rowInset
    let d = PaxTheme.Dashboard.self
    let iconWidth = d.iconGlyph
    routeIcon.contentTintColor = PaxTheme.DashboardColor.icon
    routeIcon.frame = NSRect(
      x: inset, y: (bounds.height - iconWidth) / 2,
      width: iconWidth, height: iconWidth)
    let textX = routeIcon.frame.maxX + PaxTheme.Spacing.tight
    let historyWidth =
      CGFloat(max(1, item.samples.count))
      * (PaxTheme.RTT.historyPillWidth + PaxTheme.RTT.historyPillGap)
    let historyX = bounds.width - inset - historyWidth
    let textWidth = max(1, historyX - d.routeColumnGap - textX)
    name.frame = NSRect(
      x: textX, y: d.routeContentInset, width: textWidth, height: PaxTheme.RTT.textLine)
    protocolLabel.frame = NSRect(
      x: textX, y: name.frame.maxY + d.routeLineGap,
      width: textWidth, height: PaxTheme.RTT.textLine)
    let nameFont = name.font ?? PaxTheme.Typography.label(size: d.routeNameFont)
    let metricFont = PaxTheme.Typography.metric(size: d.routeMetaFont, weight: .medium)
    number.frame = NSRect(
      x: historyX, y: name.frame.minY + nameFont.ascender - metricFont.ascender,
      width: historyWidth, height: PaxTheme.RTT.textLine)
    history.barHeight = d.routeHistoryHeight
    history.frame = NSRect(
      x: historyX, y: protocolLabel.frame.minY,
      width: historyWidth, height: d.routeHistoryHeight)
    routeIcon.frame.origin.y =
      d.routeContentInset + (protocolLabel.frame.maxY - d.routeContentInset - iconWidth) / 2

  }

  override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }
  override func draw(_ dirtyRect: NSRect) {
    effectiveAppearance.performAsCurrentDrawingAppearance {
      let path = NSBezierPath(
        roundedRect: bounds.insetBy(dx: PaxTheme.Layout.cardBorder, dy: PaxTheme.Layout.cardBorder),
        xRadius: PaxTheme.RTT.rowRadius, yRadius: PaxTheme.RTT.rowRadius)
      if hovered && !selected {
        PaxTheme.DashboardColor.rowHover.setFill()
        path.fill()
      }
      if selected {
        PaxTheme.Color.control.setFill()
        path.fill()
        PaxTheme.Color.cardBorder.setStroke()
        path.lineWidth = PaxTheme.Layout.cardBorder
        path.stroke()
      }
    }
  }
  override func updateTrackingAreas() {
    super.updateTrackingAreas()
    for area in trackingAreas { removeTrackingArea(area) }
    addTrackingArea(
      NSTrackingArea(
        rect: bounds, options: [.activeInKeyWindow, .inVisibleRect, .mouseEnteredAndExited],
        owner: self))
  }
  override func mouseEntered(with event: NSEvent) {
    hovered = true
    needsDisplay = true
  }
  override func mouseExited(with event: NSEvent) {
    hovered = false
    needsDisplay = true
  }
  @objc private func pick() {
    guard isEnabled else { return }
    onSelect?()
  }
}
