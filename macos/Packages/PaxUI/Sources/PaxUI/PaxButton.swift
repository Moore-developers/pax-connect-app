import AppKit

/// 工作台按钮：保留 NSButton 的键盘、动作与可访问性，统一参考样式的表面与间距。
public final class PaxButton: NSButton {
  public enum Style: Sendable { case prominent, secondary, ghost }
  public private(set) var style: Style
  private var hovered = false
  public init(_ style: Style, title: String, target: AnyObject? = nil, action: Selector? = nil) {
    self.style = style
    super.init(frame: .zero)
    focusRingType = .none
    self.title = title
    self.target = target
    self.action = action
    font = PaxTheme.Typography.label()
    bezelStyle = .rounded
    apply(style: style)
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
  public func apply(style: Style, title: String? = nil) {
    self.style = style
    if let title { self.title = title }
    isBordered = false
    bezelColor = style == .prominent ? PaxTheme.Color.accent : nil
    contentTintColor = style == .prominent ? PaxTheme.Color.selectedText : PaxTheme.Color.text
    setAccessibilityLabel(self.title)
    needsDisplay = true
    invalidateIntrinsicContentSize()
  }
  public override var intrinsicContentSize: NSSize {
    let size = super.intrinsicContentSize
    return NSSize(
      width: size.width + PaxTheme.Control.horizontalPadding * 2,
      height: max(PaxTheme.Control.height, size.height))
  }
  public override func updateTrackingAreas() {
    super.updateTrackingAreas()
    for area in trackingAreas { removeTrackingArea(area) }
    addTrackingArea(
      NSTrackingArea(
        rect: bounds, options: [.mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect],
        owner: self))
  }
  public override func mouseEntered(with event: NSEvent) {
    hovered = true
    needsDisplay = true
  }
  public override func mouseExited(with event: NSEvent) {
    hovered = false
    needsDisplay = true
  }
  public override func draw(_ dirtyRect: NSRect) {
    if style != .ghost || hovered || isHighlighted {
      let path = NSBezierPath(
        roundedRect: bounds.insetBy(dx: 0.5, dy: 0.5), xRadius: PaxTheme.Radius.control,
        yRadius: PaxTheme.Radius.control)
      let fill = style == .prominent ? PaxTheme.Color.selected : PaxTheme.Color.control
      (isEnabled ? fill : fill.withAlphaComponent(0.5)).setFill()
      path.fill()
      if style == .secondary {
        PaxTheme.Color.cardBorder.setStroke()
        path.lineWidth = PaxTheme.Layout.cardBorder
        path.stroke()
      }
      if isHighlighted {
        PaxTheme.Color.text.withAlphaComponent(0.06).setFill()
        path.fill()
      }
    }
    super.draw(dirtyRect)
  }
  public override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    apply(style: style)
  }
}
