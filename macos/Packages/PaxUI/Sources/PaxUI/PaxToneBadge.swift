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
  public override var intrinsicContentSize: NSSize {
    NSSize(
      width: ceil((text as NSString).size(withAttributes: [.font: font]).width)
        + PaxTheme.RTT.badgePadding * 2,
      height: PaxTheme.Dashboard.badgeHeight)
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
      NSBezierPath(roundedRect: bounds, xRadius: bounds.height / 2, yRadius: bounds.height / 2)
        .fill()
      let size = (text as NSString).size(withAttributes: [.font: font])
      (text as NSString).draw(
        at: NSPoint(x: (bounds.width - size.width) / 2, y: (bounds.height - size.height) / 2),
        withAttributes: [.font: font, .foregroundColor: ink])
    }
  }
  public override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }
}
