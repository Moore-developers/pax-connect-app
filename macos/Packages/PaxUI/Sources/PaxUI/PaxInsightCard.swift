import AppKit

/// 「需要关注」「最近变化」一行的值输入。`symbol` 非空时行首是图标，否则行首是时间。
public struct PaxInsightItem: Equatable, Identifiable {
  public var id: String
  public var symbol: String?
  public var time: String
  public var title: String
  public var detail: String
  public var detailTone: PaxToneBadge.Tone
  public var badge: String?
  public var badgeTone: PaxToneBadge.Tone
  public init(
    id: String, symbol: String? = nil, time: String = "", title: String, detail: String = "",
    detailTone: PaxToneBadge.Tone = .neutral, badge: String? = nil,
    badgeTone: PaxToneBadge.Tone = .neutral
  ) {
    self.id = id
    self.symbol = symbol
    self.time = time
    self.title = title
    self.detail = detail
    self.detailTone = detailTone
    self.badge = badge
    self.badgeTone = badgeTone
  }
}

public struct PaxInsightModel: Equatable {
  public var items: [PaxInsightItem]
  public var emptyTitle: String
  public var updateFailed: Bool
  public init(items: [PaxInsightItem] = [], emptyTitle: String = "", updateFailed: Bool = false) {
    self.items = items
    self.emptyTitle = emptyTitle
    self.updateFailed = updateFailed
  }
}

/// 概览底部两张列表卡共用：标题、至多 3 行与空态，刷新失败在标题旁提示。
public final class PaxInsightCard: PaxCard {
  private var title: String
  private var failedSuffix = " · 更新失败"
  private var updateFailed = false
  public func applyTitle(_ title: String, failedSuffix: String) {
    self.title = title
    self.failedSuffix = failedSuffix
    titleLabel.stringValue = title + (updateFailed ? failedSuffix : "")
  }
  private let titleLabel: NSTextField
  private var rows: [InsightRow] = []
  private var emptyView = PaxEmptyState(symbol: "checkmark.circle", title: "", caption: "")
  private var emptyTitle = ""
  public var onSelect: ((String) -> Void)?
  public override var isFlipped: Bool { true }

  public init(title: String, identifier: String) {
    self.title = title
    titleLabel = PaxText.label(
      title, font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyTitle, weight: .medium))
    super.init(insets: NSEdgeInsets())
    for v in [titleLabel, emptyView] { addSubview(v) }
    setAccessibilityIdentifier(identifier)
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }

  public func apply(_ model: PaxInsightModel) {
    updateFailed = model.updateFailed
    let items = Array(model.items.prefix(PaxTheme.Dashboard.insightRows))
    let existing = Dictionary(uniqueKeysWithValues: rows.map { ($0.item.id, $0) })
    let ids = Set(items.map(\.id))
    for row in rows where !ids.contains(row.item.id) { row.removeFromSuperview() }
    rows = items.map { item in
      let row: InsightRow
      if let old = existing[item.id], old.canApply(item) {
        row = old
        row.apply(item)
      } else {
        existing[item.id]?.removeFromSuperview()
        row = InsightRow(item)
      }
      row.onSelect = { [weak self] in self?.onSelect?(item.id) }
      if row.superview == nil { addSubview(row) }
      return row
    }
    if emptyTitle != model.emptyTitle {
      emptyView.removeFromSuperview()
      emptyTitle = model.emptyTitle
      emptyView = PaxEmptyState(symbol: "checkmark.circle", title: emptyTitle, caption: "")
      addSubview(emptyView)
    }
    emptyView.isHidden = !items.isEmpty
    titleLabel.stringValue = title + (model.updateFailed ? failedSuffix : "")
    needsLayout = true
  }

  public override func layout() {
    super.layout()
    let d = PaxTheme.Dashboard.self
    titleLabel.frame = NSRect(
      x: d.inset, y: d.inset, width: max(1, bounds.width - d.inset * 2),
      height: PaxTheme.RTT.textLine)
    let top = d.insightHeader
    for (index, row) in rows.enumerated() {
      row.frame = NSRect(
        x: 0, y: top + CGFloat(index) * d.insightRow, width: bounds.width, height: d.insightRow)
    }
    emptyView.frame = NSRect(
      x: d.inset, y: top, width: max(1, bounds.width - d.inset * 2),
      height: d.insightRow * CGFloat(d.insightRows))
  }
}

private final class InsightRow: NSButton {
  private(set) var item: PaxInsightItem
  var onSelect: (() -> Void)?
  private let icon: NSImageView?
  private let time: NSTextField
  private let heading: NSTextField
  private let caption: NSTextField
  private let badge: PaxToneBadge?
  private let chevron = PaxIcon.view("chevron.right", size: PaxTheme.Dashboard.bodyFont)
  override var isFlipped: Bool { true }

  init(_ item: PaxInsightItem) {
    self.item = item
    let d = PaxTheme.Dashboard.self
    icon = item.symbol.map { PaxIcon.view($0, size: d.nodeTitle) }
    time = PaxText.label(
      item.time, font: PaxTheme.Typography.metric(size: d.bodyFont), secondary: true)
    heading = PaxText.label(
      item.title, font: PaxTheme.Typography.label(size: d.bodyTitle - 1, weight: .medium))
    caption = PaxText.label(
      item.detail, font: PaxTheme.Typography.label(size: d.captionFont), secondary: true)
    badge = item.badge.map { PaxToneBadge($0, tone: item.badgeTone) }
    super.init(frame: .zero)
    isBordered = false
    title = ""
    target = self
    action = #selector(pick)
    heading.lineBreakMode = .byTruncatingTail
    caption.lineBreakMode = .byTruncatingTail
    heading.toolTip = item.title
    caption.toolTip = item.detail
    caption.textColor =
      switch item.detailTone {
      case .success: PaxTheme.DashboardColor.successText
      case .warning: PaxTheme.DashboardColor.warningText
      case .error: PaxTheme.DashboardColor.redLabel
      case .neutral: PaxTheme.Color.textSecondary
      }
    chevron.contentTintColor = PaxTheme.Color.textSecondary
    icon?.contentTintColor = tint
    for v in [icon, time, heading, caption, badge, chevron] as [NSView?] {
      if let v { addSubview(v) }
    }
    time.isHidden = item.symbol != nil
    setAccessibilityLabel(
      [item.time, item.title, item.detail, item.badge ?? ""].filter { !$0.isEmpty }.joined(
        separator: "，"))
    setAccessibilityIdentifier("insight-" + item.id)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  func canApply(_ next: PaxInsightItem) -> Bool {
    (item.symbol == nil) == (next.symbol == nil) && (item.badge == nil) == (next.badge == nil)
  }
  func apply(_ next: PaxInsightItem) {
    guard item != next else { return }
    item = next
    time.stringValue = next.time
    heading.stringValue = next.title
    heading.toolTip = next.title
    caption.stringValue = next.detail
    caption.toolTip = next.detail
    caption.textColor =
      switch next.detailTone {
      case .success: PaxTheme.DashboardColor.successText
      case .warning: PaxTheme.DashboardColor.warningText
      case .error: PaxTheme.DashboardColor.redLabel
      case .neutral: PaxTheme.Color.textSecondary
      }
    if let symbol = next.symbol { icon?.image = PaxIcon.image(symbol) }
    icon?.contentTintColor = tint
    badge?.text = next.badge ?? ""
    badge?.tone = next.badgeTone
    setAccessibilityLabel(
      [next.time, next.title, next.detail, next.badge ?? ""].filter { !$0.isEmpty }.joined(
        separator: "，"))
    needsLayout = true
  }

  private var tint: NSColor {
    switch item.badgeTone {
    case .error: PaxTheme.DashboardColor.redLabel
    case .warning: PaxTheme.DashboardColor.warningText
    case .success: PaxTheme.DashboardColor.successText
    case .neutral: PaxTheme.DashboardColor.icon
    }
  }

  @objc private func pick() { onSelect?() }

  override func layout() {
    super.layout()
    let d = PaxTheme.Dashboard.self
    var left = d.inset
    var right = bounds.width - d.inset
    if let icon {
      icon.frame = NSRect(
        x: left + (d.icon - d.nodeTitle) / 2, y: (bounds.height - d.nodeTitle) / 2,
        width: d.nodeTitle, height: d.nodeTitle)
      left += d.icon + PaxTheme.Spacing.medium
      chevron.frame = NSRect(
        x: right - d.bodyFont, y: (bounds.height - d.bodyFont) / 2, width: d.bodyFont,
        height: d.bodyFont)
      right = chevron.frame.minX - PaxTheme.Spacing.tight
      chevron.isHidden = false
    } else {
      time.frame = NSRect(
        x: left, y: (bounds.height - PaxTheme.RTT.textLine) / 2, width: PaxTheme.Layout.timeColumn,
        height: PaxTheme.RTT.textLine)
      left += PaxTheme.Layout.timeColumn + PaxTheme.Spacing.medium
      chevron.isHidden = true
    }
    if let badge {
      let size = badge.intrinsicContentSize
      badge.frame = NSRect(
        x: right - size.width, y: (bounds.height - size.height) / 2, width: size.width,
        height: size.height)
      right = badge.frame.minX - PaxTheme.Spacing.medium
    }
    let width = max(1, right - left)
    let hasDetail = !item.detail.isEmpty
    let block = PaxTheme.RTT.textLine + (hasDetail ? PaxTheme.RTT.badgeTextHeight : 0)
    let top = (bounds.height - block) / 2
    heading.frame = NSRect(x: left, y: top, width: width, height: PaxTheme.RTT.textLine)
    caption.frame = NSRect(
      x: left, y: top + PaxTheme.RTT.textLine, width: width, height: PaxTheme.RTT.badgeTextHeight)
    caption.isHidden = !hasDetail
  }

  override func draw(_ dirtyRect: NSRect) {
    effectiveAppearance.performAsCurrentDrawingAppearance {
      PaxTheme.DashboardColor.divider.setStroke()
      let edge = NSBezierPath()
      edge.move(to: NSPoint(x: PaxTheme.Dashboard.inset, y: 0))
      edge.line(to: NSPoint(x: bounds.width - PaxTheme.Dashboard.inset, y: 0))
      edge.lineWidth = PaxTheme.Layout.cardBorder
      edge.stroke()
    }
  }
}

/// Compact dashboard activity feed. The full event remains available on hover and in Logs.
public final class PaxActivityStrip: PaxCard {
  private let heading = PaxText.label(
    "", font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyFont))
  private let message = PaxText.label(
    "", font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyFont), secondary: true)
  private let dot = NSView()
  private let slots = (0..<3).map { _ in ActivitySlot() }
  private var name: String
  private var failure = ""
  private var failed = false
  public var onSelect: ((String) -> Void)?
  public override var isFlipped: Bool { true }

  public init(title: String, identifier: String) {
    name = title
    super.init(insets: NSEdgeInsets())
    dot.wantsLayer = true
    dot.layer?.cornerRadius = 3
    dot.layer?.backgroundColor = PaxTheme.DashboardColor.green.cgColor
    for view in [dot, heading, message] + slots { addSubview(view) }
    for slot in slots {
      slot.isHidden = true
      slot.onSelect = { [weak self] id in self?.onSelect?(id) }
    }
    setAccessibilityIdentifier(identifier)
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError() }
  public func applyTitle(_ title: String, failedSuffix: String) {
    name = title
    failure = failedSuffix
    updateHeading()
  }
  private func updateHeading() {
    let text = name + (failed ? failure : "")
    if heading.stringValue != text {
      heading.stringValue = text
      needsLayout = true
    }
  }
  public func apply(_ model: PaxInsightModel) {
    failed = model.updateFailed
    updateHeading()
    let text = model.items.isEmpty ? model.emptyTitle : ""
    if message.stringValue != text { message.stringValue = text }
    for (index, slot) in slots.enumerated() {
      let next = model.items.indices.contains(index) ? model.items[index] : nil
      guard slot.item != next else { continue }
      let previousWidth = slot.item == nil ? 0 : slot.intrinsicContentSize.width
      slot.apply(next)
      let width = next == nil ? 0 : slot.intrinsicContentSize.width
      if previousWidth != width { needsLayout = true }
    }
  }
  public override func layout() {
    super.layout()
    layer?.cornerRadius = bounds.height / 2
    let pad = PaxTheme.Dashboard.inset
    dot.frame = NSRect(x: pad, y: (bounds.height - 6) / 2, width: 6, height: 6)
    heading.sizeToFit()
    heading.frame.origin = NSPoint(
      x: dot.frame.maxX + 7, y: (bounds.height - heading.frame.height) / 2)
    var x = heading.frame.maxX + 12
    let messageHeight = message.intrinsicContentSize.height
    message.frame = NSRect(
      x: x, y: (bounds.height - messageHeight) / 2, width: max(1, bounds.width - pad - x),
      height: messageHeight)
    let visible = slots.filter { !$0.isHidden }
    for (index, slot) in visible.enumerated() {
      let width = min(
        slot.intrinsicContentSize.width,
        max(0, (bounds.width - pad - x) / CGFloat(visible.count - index)))
      slot.frame = NSRect(x: x, y: 0, width: width, height: bounds.height)
      x += width + 16
    }
  }
}

/// A stable event position. Its text and status change independently of its neighbors.
private final class ActivitySlot: NSButton {
  private(set) var item: PaxInsightItem?
  var onSelect: ((String) -> Void)?
  private let time = PaxText.label(
    "", font: PaxTheme.Typography.metric(size: PaxTheme.Dashboard.bodyFont), secondary: true)
  private let event = PaxText.label(
    "", font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyFont))
  private let status = PaxToneBadge("")
  override init(frame: NSRect) {
    super.init(frame: frame)
    isBordered = false
    title = ""
    target = self
    action = #selector(open)
    status.compact = true
    event.lineBreakMode = .byTruncatingTail
    for view in [time, event, status] as [NSView] { addSubview(view) }
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError() }
  @objc private func open() { if let item { onSelect?(item.id) } }
  override func hitTest(_ point: NSPoint) -> NSView? { super.hitTest(point) == nil ? nil : self }
  func apply(_ next: PaxInsightItem?) {
    item = next
    isHidden = next == nil
    guard let next else { return }
    if time.stringValue != next.time { time.stringValue = next.time }
    if event.stringValue != next.title { event.stringValue = next.title }
    let label = next.badge ?? next.detail
    let tone = next.badge == nil ? next.detailTone : next.badgeTone
    if status.text != label { status.text = label }
    if status.tone != tone { status.tone = tone }
    status.isHidden = label.isEmpty
    toolTip = [next.time, next.title, next.detail, next.badge ?? ""].filter { !$0.isEmpty }.joined(
      separator: " · ")
    setAccessibilityLabel(toolTip)
    invalidateIntrinsicContentSize()
    needsLayout = true
  }
  private var timeWidth: CGFloat {
    time.stringValue.isEmpty ? 0 : time.intrinsicContentSize.width + 5
  }
  private var statusWidth: CGFloat { status.isHidden ? 0 : status.intrinsicContentSize.width + 5 }
  override var intrinsicContentSize: NSSize {
    .init(
      width: timeWidth
        + ceil((event.stringValue as NSString).size(withAttributes: [.font: event.font!]).width)
        + statusWidth + 6, height: 20)
  }
  override func layout() {
    super.layout()
    func centered(_ view: NSView, x: CGFloat, width: CGFloat) -> NSRect {
      let height = view.intrinsicContentSize.height
      return NSRect(x: x, y: (bounds.height - height) / 2, width: max(0, width), height: height)
    }
    time.frame = centered(time, x: 0, width: timeWidth)
    event.frame = centered(event, x: timeWidth, width: bounds.width - timeWidth - statusWidth)
    status.frame = centered(status, x: event.frame.maxX + 5, width: statusWidth - 5)
  }
}
