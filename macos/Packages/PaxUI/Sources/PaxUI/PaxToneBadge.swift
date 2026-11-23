import AppKit

/// 语义色小胶囊：升 / 降 / 持平、错误 / 待处理；`subdued` 把底色再淡一半，用于次要说明（更新时间）。颜色只取 `PaxTheme.DashboardColor`，文字总是明确说明。
public final class PaxToneBadge: NSView {
  public enum Tone: Equatable, Sendable { case success, warning, error, neutral }
  public var text: String {
    didSet {
      invalidateIntrinsicContentSize()
      setAccessibilityLabel(text)
      needsDisplay = true
    }
  }
  public var tone: Tone { didSet { needsDisplay = true } }
  private static let subduedFill: CGFloat = 0.5
  public var weight: NSFont.Weight = .medium {
    didSet {
      invalidateIntrinsicContentSize()
      needsDisplay = true
    }
  }
  public var compact = false {
    didSet {
      invalidateIntrinsicContentSize()
      needsDisplay = true
    }
  }
  /// Scale only an existing leading circle glyph; the label retains its own font.
  public var leadingDotScale: CGFloat = 1 {
    didSet {
      invalidateIntrinsicContentSize()
      needsDisplay = true
    }
  }
  public var subdued = false { didSet { needsDisplay = true } }
  public init(_ text: String, tone: Tone = .neutral) {
    self.text = text
    self.tone = tone
    super.init(frame: .zero)
    setAccessibilityElement(true)
    setAccessibilityRole(.staticText)
    setAccessibilityLabel(text)
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var isFlipped: Bool { true }
  private var font: NSFont {
    PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyFont, weight: weight)
  }
  private var displayText: NSMutableAttributedString {
    let value = NSMutableAttributedString(string: text, attributes: [.font: font])
    if leadingDotScale != 1 && text.hasPrefix("●") {
      let small = PaxTheme.Typography.label(size: font.pointSize * leadingDotScale, weight: weight)
      value.addAttributes(
        [.font: small, .baselineOffset: (font.capHeight - small.capHeight) / 2],
        range: NSRange(location: 0, length: 1))
    }
    return value
  }
  public override var intrinsicContentSize: NSSize {
    NSSize(
      width: ceil(displayText.size().width)
        + (compact ? 9 : PaxTheme.RTT.badgePadding * 2),
      height: compact ? 19 : PaxTheme.Dashboard.badgeHeight)
  }
  public override func draw(_ dirtyRect: NSRect) {
    let c = PaxTheme.DashboardColor.self
    let (fill, ink): (NSColor, NSColor) =
      switch tone {
      case .success: (c.successFill, c.successText)
      case .warning: (c.warningFill, c.warningText)
      case .error: (c.red.withAlphaComponent(PaxTheme.Color.noticeFillOpacity), c.redLabel)
      case .neutral: (c.neutralFill, PaxTheme.Color.textSecondary)
      }
    effectiveAppearance.performAsCurrentDrawingAppearance {
      (subdued ? fill.withAlphaComponent(fill.alphaComponent * Self.subduedFill) : fill)
        .setFill()
      NSBezierPath(
        roundedRect: bounds, xRadius: compact ? 4 : bounds.height / 2,
        yRadius: compact ? 4 : bounds.height / 2
      )
      .fill()
      let paragraph = NSMutableParagraphStyle()
      paragraph.alignment = .center
      paragraph.lineBreakMode = .byTruncatingTail
      let height = ceil(font.ascender - font.descender)
      let padding: CGFloat = compact ? 3 : 5
      let value = displayText
      value.addAttributes(
        [.foregroundColor: ink, .paragraphStyle: paragraph],
        range: NSRange(location: 0, length: value.length))
      value.draw(
        in: NSRect(
          x: padding, y: (bounds.height - height) / 2, width: max(0, bounds.width - 2 * padding),
          height: height + 1))

    }
  }
  public override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }
}
