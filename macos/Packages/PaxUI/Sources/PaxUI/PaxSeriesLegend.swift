import AppKit

/// 图表上方的系列图例：色点 + 文字（可带占比），按给定顺序横排。
public final class PaxSeriesLegend: NSView {
  public struct Item: Equatable {
    public let text: String
    public let color: NSColor
    public init(_ text: String, color: NSColor) {
      self.text = text
      self.color = color
    }
  }
  public var items: [Item] = [] {
    didSet {
      invalidateIntrinsicContentSize()
      setAccessibilityLabel(items.map(\.text).joined(separator: "，"))
      needsDisplay = true
    }
  }
  public override var isFlipped: Bool { true }
  private var attributes: [NSAttributedString.Key: Any] {
    [
      .font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyFont),
      .foregroundColor: PaxTheme.Color.textSecondary,
    ]
  }
  public override init(frame: NSRect) {
    super.init(frame: frame)
    setAccessibilityElement(true)
    setAccessibilityRole(.image)
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var intrinsicContentSize: NSSize {
    let dot = PaxTheme.Chart.proportionDot + PaxTheme.Spacing.xs
    let widths = items.map {
      ceil(($0.text as NSString).size(withAttributes: attributes).width) + dot
    }
    return NSSize(
      width: widths.reduce(0, +) + PaxTheme.Spacing.regular * CGFloat(max(0, items.count - 1)),
      height: PaxTheme.Dashboard.legendHeight)
  }
  public override func draw(_ dirtyRect: NSRect) {
    let diameter = PaxTheme.Chart.proportionDot
    var x: CGFloat = 0
    for item in items {
      item.color.setFill()
      NSBezierPath(
        ovalIn: NSRect(x: x, y: (bounds.height - diameter) / 2, width: diameter, height: diameter)
      ).fill()
      x += diameter + PaxTheme.Spacing.xs
      let text = item.text as NSString
      let size = text.size(withAttributes: attributes)
      text.draw(at: NSPoint(x: x, y: (bounds.height - size.height) / 2), withAttributes: attributes)
      x += ceil(size.width) + PaxTheme.Spacing.regular
    }
  }
  public override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }
}
