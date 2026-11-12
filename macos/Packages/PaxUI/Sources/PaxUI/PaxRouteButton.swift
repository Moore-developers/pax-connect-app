import AppKit

/// 与线路最终稿一致的 NSButton；值样式共享给工具栏、测试和所有编辑浮层。
public final class PaxRouteButton: NSButton {
  public enum Style { case secondary, primary, quiet }
  public var style: Style { didSet { needsDisplay = true } }
  public override var title: String {
    didSet {
      setAccessibilityLabel(title)
      invalidateIntrinsicContentSize()
      needsDisplay = true
    }
  }
  public var showsDisclosure = false {
    didSet {
      invalidateIntrinsicContentSize()
      needsDisplay = true
    }
  }
  /// Shows activity before the title while keeping cancellation buttons enabled.
  public var isLoading = false {
    didSet {
      guard oldValue != isLoading else { return }
      activity.isHidden = !isLoading
      activity.isAnimating = isLoading
      invalidateIntrinsicContentSize()
      needsLayout = true
      needsDisplay = true
    }
  }
  private let activity = PaxActivityIndicator(frame: .zero)
  private var hovered = false
  public init(
    _ title: String, symbol: String? = nil, style: Style = .secondary, target: AnyObject? = nil,
    action: Selector? = nil
  ) {
    self.style = style
    super.init(frame: .zero)
    self.title = title
    self.target = target
    self.action = action
    font = PaxTheme.Typography.label(size: PaxTheme.Routes.metaFont, weight: .medium)
    isBordered = false
    focusRingType = .none
    if let symbol {
      image = PaxRouteIcon.image(symbol, size: PaxTheme.Routes.metaFont)
      imagePosition = .imageLeading
    }
    setAccessibilityLabel(title)
    activity.isHidden = true
    addSubview(activity)
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var intrinsicContentSize: NSSize {
    let height =
      controlSize == .small ? PaxTheme.Control.compactHeight : PaxTheme.Routes.controlHeight
    if imagePosition == .imageOnly { return NSSize(width: height, height: height) }
    let font = font ?? PaxTheme.Typography.caption()
    let width = (title as NSString).size(withAttributes: [.font: font]).width
    return NSSize(
      width: width
        + (image == nil && !isLoading ? 0 : PaxTheme.Control.navigationIcon + PaxTheme.Spacing.xs)
        + PaxTheme.Routes.controlInset * 2
        + (showsDisclosure ? PaxTheme.Control.navigationIcon + PaxTheme.Spacing.xs : 0),
      height: height)
  }
  public override func layout() {
    super.layout()
    guard isLoading else { return }
    let size = PaxTheme.Control.navigationIcon
    let textWidth = (title as NSString).size(
      withAttributes: [.font: font ?? PaxTheme.Typography.caption()]).width
    let disclosure = showsDisclosure ? size + PaxTheme.Spacing.xs : 0
    activity.frame = NSRect(
      x: (bounds.width - textWidth - size - PaxTheme.Spacing.xs - disclosure) / 2,
      y: (bounds.height - size) / 2, width: size, height: size)
    activity.tintColor = style == .primary ? PaxTheme.Color.onSolid : PaxTheme.Color.accent
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
    let c = PaxTheme.Routes.self
    let shape = NSBezierPath(
      roundedRect: bounds.insetBy(dx: 0.5, dy: 0.5), xRadius: c.controlRadius,
      yRadius: c.controlRadius)
    if style != .quiet {
      NSGraphicsContext.saveGraphicsState()
      let shadow = NSShadow()
      shadow.shadowColor = PaxTheme.Color.shadow.withAlphaComponent(0.04)
      shadow.shadowBlurRadius = c.tagShadowBlur
      shadow.shadowOffset = PaxTheme.Shadow.cardOffset
      shadow.set()
      (style == .primary ? c.selectedBlue : c.card).setFill()
      shape.fill()
      NSGraphicsContext.restoreGraphicsState()
      if style == .secondary {
        NSGradient(starting: c.card, ending: c.canvas)?.draw(in: shape, angle: 90)
        c.line.setStroke()
        shape.lineWidth = PaxTheme.Layout.cardBorder
        shape.stroke()
      }
    }
    let color =
      style == .primary
      ? c.card : hovered || isHighlighted ? c.selectedBlue : contentTintColor ?? c.text
    let alpha: CGFloat = isEnabled ? 1 : 0.45
    let font = font ?? PaxTheme.Typography.caption()
    let attrs: [NSAttributedString.Key: Any] = [
      .font: font, .foregroundColor: color.withAlphaComponent(alpha),
    ]
    let textSize = (title as NSString).size(withAttributes: attrs)
    let glyph =
      imagePosition == .imageOnly
      ? PaxTheme.Control.navigationIcon
      : image == nil && !isLoading ? 0 : PaxTheme.Control.navigationIcon + PaxTheme.Spacing.xs
    let textWidth = imagePosition == .imageOnly ? 0 : textSize.width
    let disclosure = showsDisclosure ? PaxTheme.Control.navigationIcon + PaxTheme.Spacing.xs : 0
    let x = (bounds.width - textWidth - glyph - disclosure) / 2
    if let image, !isLoading {
      PaxRouteDrawing.icon(
        image,
        in: NSRect(
          x: x, y: (bounds.height - PaxTheme.Control.navigationIcon) / 2,
          width: PaxTheme.Control.navigationIcon, height: PaxTheme.Control.navigationIcon),
        color: color.withAlphaComponent(alpha))
    }
    if showsDisclosure,
      let chevron = PaxRouteIcon.image("chevron.down", size: PaxTheme.Control.navigationIcon)
    {
      PaxRouteDrawing.icon(
        chevron,
        in: NSRect(
          x: x + textWidth + glyph + PaxTheme.Spacing.xs,
          y: (bounds.height - PaxTheme.Control.navigationIcon) / 2,
          width: PaxTheme.Control.navigationIcon, height: PaxTheme.Control.navigationIcon),
        color: color.withAlphaComponent(alpha))
    }
    if imagePosition != .imageOnly {
      (title as NSString).draw(
        at: NSPoint(x: x + glyph, y: (bounds.height - textSize.height) / 2), withAttributes: attrs)
    }
  }
}
