import AppKit

/// 内容宽页签：可横向滚动，溢出时通过更多菜单直达任意项。
public final class PaxTabBar: NSView {
  public enum Style { case groups, detail }
  public struct Item: Equatable {
    public let id: String
    public let title: String
    public let symbol: String?
    public let accessibilityLabel: String?
    public let count: Int?
    public var displayTitle: String { title + (count.map { "（\($0)）" } ?? "") }
    public init(
      id: String, title: String, symbol: String? = nil, accessibilityLabel: String? = nil,
      count: Int? = nil
    ) {
      self.id = id
      self.title = title
      self.symbol = symbol
      self.accessibilityLabel = accessibilityLabel
      self.count = count
    }
  }
  public var onSelect: ((String) -> Void)?
  private let style: Style
  private let strip: PaxTabStrip
  private let more = NSButton(title: "更多", target: nil, action: nil)
  private var items: [Item] = []
  private var selected = ""
  public init(style: Style = .groups) {
    self.style = style
    strip = PaxTabStrip(style: style)
    super.init(frame: .zero)
    addSubview(strip)
    addSubview(more)
    more.bezelStyle = .rounded
    more.font = PaxTheme.Typography.caption()
    more.focusRingType = .none
    more.target = self
    more.action = #selector(showMore)
    more.setAccessibilityLabel("更多分组")
    more.isHidden = true
    strip.onSelect = { [weak self] id in self?.onSelect?(id) }
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var isFlipped: Bool { true }
  public override var intrinsicContentSize: NSSize { strip.intrinsicContentSize }
  public func apply(_ items: [Item], selected: String) {
    self.items = items
    self.selected = selected
    strip.apply(items, selected: selected)
    needsLayout = true
  }
  public override func layout() {
    super.layout()
    let overflow = (strip.documentView?.frame.width ?? 0) > bounds.width + 1
    more.isHidden = !overflow
    let width = overflow ? more.intrinsicContentSize.width + PaxTheme.Spacing.tight : 0
    strip.frame = NSRect(x: 0, y: 0, width: max(0, bounds.width - width), height: bounds.height)
    more.frame = NSRect(x: bounds.width - width, y: 0, width: width, height: bounds.height)
    strip.revealSelection()
  }
  public override func draw(_ dirtyRect: NSRect) {
    guard style == .detail else { return }
    PaxTheme.Routes.line.setFill()
    NSRect(
      x: 0, y: bounds.height - PaxTheme.Layout.cardBorder, width: bounds.width,
      height: PaxTheme.Layout.cardBorder
    ).fill()
  }
  @objc private func showMore() {
    let menu = NSMenu()
    for item in items {
      let choice = NSMenuItem(
        title: item.displayTitle, action: #selector(choose(_:)), keyEquivalent: "")
      if let symbol = item.symbol {
        choice.image = PaxIcon.image(symbol)
        choice.preferVisibleImage()
      }
      choice.setAccessibilityLabel(item.accessibilityLabel ?? item.displayTitle)
      choice.target = self
      choice.representedObject = item.id
      choice.state = item.id == selected ? .on : .off
      menu.addItem(choice)
    }
    menu.popUp(positioning: nil, at: NSPoint(x: 0, y: more.bounds.maxY), in: more)
  }
  @objc private func choose(_ item: NSMenuItem) {
    if let id = item.representedObject as? String { onSelect?(id) }
  }
}

private final class PaxTabStrip: NSScrollView {
  typealias Style = PaxTabBar.Style
  typealias Item = PaxTabBar.Item
  public var onSelect: ((String) -> Void)?
  private let stack = NSStackView()
  private var segments: PaxTabs?
  private let style: Style
  private var items: [Item] = []
  private var selected = ""
  public init(style: Style = .groups) {
    self.style = style
    super.init(frame: .zero)
    drawsBackground = false
    hasHorizontalScroller = false
    hasVerticalScroller = false
    horizontalScrollElasticity = .allowed
    documentView = stack
    stack.orientation = .horizontal
    stack.alignment = .centerY
    stack.spacing = style == .detail ? PaxTheme.Routes.detailTabGap : PaxTheme.Spacing.xs
    setAccessibilityLabel("横向页签")
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public func apply(_ items: [Item], selected: String) {
    guard self.items != items || self.selected != selected else { return }
    if self.items == items, let segments {
      self.selected = selected
      segments.selectedIndex = items.firstIndex { $0.id == selected } ?? 0
      return
    }
    self.items = items
    self.selected = selected
    let origin = contentView.bounds.origin
    if style == .groups {
      let tabs = PaxTabs(
        items.map(\.title), symbols: items.map(\.symbol),
        accessibilityLabels: items.map { $0.accessibilityLabel ?? $0.displayTitle },
        counts: items.map(\.count))
      tabs.selectedIndex = items.firstIndex { $0.id == selected } ?? 0
      tabs.onSelect = { [weak self] index in
        guard let self, self.items.indices.contains(index) else { return }
        self.onSelect?(self.items[index].id)
      }
      tabs.frame = NSRect(origin: .zero, size: tabs.intrinsicContentSize)
      segments = tabs
      documentView = tabs
      contentView.scroll(
        to: NSPoint(x: min(origin.x, max(0, tabs.frame.width - contentSize.width)), y: 0))
      reflectScrolledClipView(contentView)
      return
    }
    for v in stack.arrangedSubviews {
      stack.removeArrangedSubview(v)
      v.removeFromSuperview()
    }
    for item in items {
      let button = PaxTabButton(
        title: item.displayTitle, symbol: item.symbol, target: self,
        action: #selector(selectTab(_:)))
      button.identifier = NSUserInterfaceItemIdentifier(item.id)
      button.state = item.id == selected ? .on : .off
      button.setAccessibilityLabel(item.accessibilityLabel ?? item.displayTitle)
      button.setAccessibilityIdentifier("route-tab-" + item.id)
      button.setAccessibilityValue(item.id == selected ? "已选中" : "未选中")
      stack.addArrangedSubview(button)
    }
    let width =
      stack.arrangedSubviews.reduce(CGFloat(0)) { $0 + $1.intrinsicContentSize.width }
      + CGFloat(max(0, stack.arrangedSubviews.count - 1)) * stack.spacing
    stack.setFrameSize(NSSize(width: width, height: intrinsicContentSize.height))
    stack.layoutSubtreeIfNeeded()
    contentView.scroll(
      to: NSPoint(x: min(origin.x, max(0, stack.frame.width - contentSize.width)), y: 0))
    reflectScrolledClipView(contentView)
  }
  func revealSelection() {
    if let segments {
      segments.revealSelection()
      return
    }
    if let view = stack.arrangedSubviews.first(where: { $0.identifier?.rawValue == selected }) {
      stack.scrollToVisible(view.frame)
    }
  }
  public override var intrinsicContentSize: NSSize {
    NSSize(
      width: NSView.noIntrinsicMetric,
      height: style == .detail ? PaxTheme.Routes.detailTabHeight : PaxTheme.Dashboard.tabHeight)
  }
  @objc private func selectTab(_ button: NSButton) {
    if let id = button.identifier?.rawValue { onSelect?(id) }
  }
}

private final class PaxTabButton: NSButton {
  private var hovered = false
  init(
    title: String, symbol: String?, target: AnyObject?,
    action: Selector?
  ) {
    super.init(frame: .zero)
    self.title = title
    self.target = target
    self.action = action
    isBordered = false
    focusRingType = .none
    font = PaxTheme.Typography.label(size: PaxTheme.Routes.detailTabFont, weight: .medium)
    if let symbol { image = PaxRouteIcon.image(symbol, size: PaxTheme.Routes.detailTabIcon) }
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override var isFlipped: Bool { true }
  override var intrinsicContentSize: NSSize {
    let width = (title as NSString).size(withAttributes: [
      .font: font ?? PaxTheme.Typography.caption()
    ]).width
    let imageWidth =
      image == nil ? 0 : PaxTheme.Routes.detailTabIcon + PaxTheme.Routes.detailTabIconGap
    return NSSize(
      width: ceil(width + imageWidth) + PaxTheme.Routes.detailTabInset * 2,
      height: PaxTheme.Routes.detailTabHeight)
  }
  override func updateTrackingAreas() {
    super.updateTrackingAreas()
    for area in trackingAreas { removeTrackingArea(area) }
    addTrackingArea(
      NSTrackingArea(
        rect: bounds, options: [.mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect],
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
  override func draw(_ dirtyRect: NSRect) {
    let c = PaxTheme.Routes.self
    let color = state == .on || hovered ? c.selectedBlue : c.muted
    let labelFont = PaxTheme.Typography.label(
      size: c.detailTabFont, weight: state == .on ? .medium : .regular)
    let attrs: [NSAttributedString.Key: Any] = [.font: labelFont, .foregroundColor: color]
    let size = (title as NSString).size(withAttributes: attrs)
    let glyph = image == nil ? 0 : PaxTheme.Routes.detailTabIcon + PaxTheme.Routes.detailTabIconGap
    let x = (bounds.width - size.width - glyph) / 2
    let contentRect = NSRect(
      x: 0, y: c.detailTabTop, width: bounds.width,
      height: max(
        0, bounds.height - c.detailTabTop - c.detailTabIndicatorGap - c.detailTabUnderline))
    if state == .on {
      c.detailTabSelectedFill.setFill()
      NSBezierPath(roundedRect: contentRect, xRadius: c.detailTabRadius, yRadius: c.detailTabRadius)
        .fill()
    }
    if let image {
      PaxRouteDrawing.icon(
        image,
        in: NSRect(
          x: x, y: contentRect.midY - c.detailTabIcon / 2,
          width: c.detailTabIcon, height: c.detailTabIcon),
        color: state == .on ? c.selectedBlue : c.muted)
    }
    (title as NSString).draw(
      at: NSPoint(x: x + glyph, y: contentRect.midY - size.height / 2), withAttributes: attrs)
    if state == .on {
      c.blue.setFill()
      NSRect(
        x: 0, y: bounds.height - c.detailTabUnderline,
        width: bounds.width, height: c.detailTabUnderline
      ).fill()
    }
  }
}
