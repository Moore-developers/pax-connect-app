import AppKit

/// Small native buttons for dense tables, card actions, and anchored pickers.
/// Uses NSButton events and accessibility; no product state enters the component.
public final class PaxCompactButton: NSButton {
  public enum Style { case text, bordered, selected, primary }
  public var compactStyle: Style = .bordered {
    didSet {
      needsDisplay = true
      invalidateIntrinsicContentSize()
    }
  }
  /// Larger capsule shape (see `PaxCompactButton+Capsule`) for a page's own actions; the default
  /// compact rectangle stays for dense tables and anchored pickers.
  public var capsule = false { didSet { invalidateIntrinsicContentSize() } }
  /// Text-style buttons stay plain links even when their page asks for capsules.
  var usesCapsule: Bool { capsule && compactStyle != .text }
  /// Capsule height; compact columns (e.g. a table's action column) can ask for a smaller one.
  public var capsuleHeight: CGFloat = 40 { didSet { invalidateIntrinsicContentSize() } }
  /// Base colour of the capsule; nil follows the style (primary → blue, others → light).
  public var capsuleTone: CapsuleTone? { didSet { needsDisplay = true } }
  public var leadingAligned = false
  public var destructive = false
  public var trailingGlyph: String? { didSet { needsDisplay = true } }
  /// Optional symbol at the trailing edge, centered by its image bounds.
  public var trailingImage: NSImage? {
    didSet {
      needsDisplay = true
      invalidateIntrinsicContentSize()
    }
  }
  public override init(frame: NSRect) {
    super.init(frame: frame)
    isBordered = false
    bezelStyle = .rounded
    font = PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyFont)
    setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
  }
  public convenience init(
    _ title: String, style: Style = .bordered, target: AnyObject?, action: Selector?
  ) {
    self.init(frame: .zero)
    self.title = title
    compactStyle = style
    self.target = target
    self.action = action
    setAccessibilityLabel(title)
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  /// The button draws its whole frame; the rounded bezel's default insets would make layout size
  /// and drawn size differ (a 32pt constraint drew 35pt).
  public override var alignmentRectInsets: NSEdgeInsets { NSEdgeInsets() }
  public override var intrinsicContentSize: NSSize {
    if usesCapsule { return capsuleSize }
    let width = (title as NSString).size(withAttributes: [
      .font: font ?? PaxTheme.Typography.caption()
    ]).width
    return NSSize(
      width: width + PaxTheme.Spacing.regular
        + trailingWidth([
          .font: font ?? PaxTheme.Typography.caption()
        ])
        + (image == nil ? 0 : PaxTheme.Dashboard.iconGlyph + PaxTheme.Spacing.xs),
      height: PaxTheme.Control.compactHeight)
  }

  func trailingWidth(_ attrs: [NSAttributedString.Key: Any]) -> CGFloat {
    trailingImage.map { $0.size.width + PaxTheme.Spacing.tight }
      ?? trailingGlyph.map {
        ($0 as NSString).size(withAttributes: attrs).width + PaxTheme.Spacing.tight
      } ?? 0
  }

  public override func draw(_ dirtyRect: NSRect) {
    if usesCapsule, !isTransparent {
      drawCapsule()
      return
    }
    if isTransparent {
      if window?.firstResponder === self {
        PaxTheme.Routes.blue.setStroke()
        let path = NSBezierPath(
          roundedRect: bounds.insetBy(
            dx: PaxTheme.Control.focusRingInset, dy: PaxTheme.Control.focusRingInset),
          xRadius: PaxTheme.Radius.control,
          yRadius: PaxTheme.Radius.control)
        path.lineWidth = PaxTheme.Control.focusRingWidth
        path.stroke()
      }
      return
    }
    let rect = bounds.insetBy(
      dx: PaxTheme.Layout.cardBorder / 2, dy: PaxTheme.Layout.cardBorder / 2)
    let path = NSBezierPath(
      roundedRect: rect, xRadius: PaxTheme.Radius.control,
      yRadius: PaxTheme.Radius.control)
    if compactStyle != .text {
      (compactStyle == .primary
        ? PaxTheme.Routes.blue
        : compactStyle == .selected ? PaxTheme.Routes.menuFill : PaxTheme.Color.control).setFill()
      path.fill()
      if compactStyle == .bordered {
        PaxTheme.Color.cardBorder.setStroke()
        path.lineWidth = PaxTheme.Layout.cardBorder
        path.stroke()
      }
    }
    if isHighlighted {
      PaxTheme.Color.text.withAlphaComponent(PaxTheme.Color.noticeFillOpacity).setFill()
      path.fill()
    }
    let color =
      !isEnabled
      ? PaxTheme.Color.textDisabled
      : compactStyle == .primary
        ? PaxTheme.Color.selectedText
        : destructive
          ? PaxTheme.Color.error
          : compactStyle == .text || compactStyle == .selected
            ? PaxTheme.Routes.blue : PaxTheme.Color.text
    let attrs: [NSAttributedString.Key: Any] = [
      .font: font ?? PaxTheme.Typography.caption(), .foregroundColor: color,
    ]
    let size = (title as NSString).size(withAttributes: attrs)
    let glyph = image == nil ? 0 : PaxTheme.Dashboard.iconGlyph + PaxTheme.Spacing.xs
    let tail = trailingWidth(attrs)
    let x =
      leadingAligned
      ? PaxTheme.Spacing.tight
      : max(PaxTheme.Spacing.xs, (bounds.width - size.width - glyph - tail) / 2)
    if let image {
      image.draw(
        in: NSRect(
          x: x, y: (bounds.height - PaxTheme.Dashboard.iconGlyph) / 2,
          width: PaxTheme.Dashboard.iconGlyph, height: PaxTheme.Dashboard.iconGlyph),
        from: .zero, operation: .sourceOver,
        fraction: isEnabled ? 1 : PaxTheme.Control.disabledOpacity,
        respectFlipped: true, hints: nil)
    }
    if let trailingImage {
      drawTrailingImage(
        trailingImage, color: color,
        rect: NSRect(
          x: bounds.maxX - PaxTheme.Spacing.tight - trailingImage.size.width,
          y: bounds.midY - trailingImage.size.height / 2,
          width: trailingImage.size.width, height: trailingImage.size.height))
    } else if let trailingGlyph {
      let glyphSize = (trailingGlyph as NSString).size(withAttributes: attrs)
      (trailingGlyph as NSString).draw(
        at: .init(
          x: bounds.width - glyphSize.width - PaxTheme.Spacing.tight,
          y: (bounds.height - glyphSize.height) / 2), withAttributes: attrs)
    }
    let textRect = NSRect(
      x: x + glyph, y: (bounds.height - size.height) / 2,
      width: max(
        0,
        bounds.width - x - glyph
          - (tail == 0 ? PaxTheme.Spacing.xs : tail + PaxTheme.Spacing.tight)),
      height: size.height)
    // A button title is one line; rectangle drawing can wrap dates at punctuation.
    NSGraphicsContext.saveGraphicsState()
    NSBezierPath(rect: textRect).addClip()
    (title as NSString).draw(at: textRect.origin, withAttributes: attrs)
    NSGraphicsContext.restoreGraphicsState()
  }

  func drawTrailingImage(_ image: NSImage, color: NSColor, rect: NSRect) {
    let tinted = image.withSymbolConfiguration(.init(paletteColors: [color])) ?? image
    tinted.draw(
      in: rect, from: .zero, operation: .sourceOver,
      fraction: isEnabled ? 1 : PaxTheme.Control.disabledOpacity,
      respectFlipped: true, hints: nil)
  }
}
