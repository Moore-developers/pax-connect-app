import AppKit

/// 图例标记：默认圆点，也可用相同尺寸的方块。
public final class PaxLegendDot: NSView {
  public enum Shape { case circle, square }
  private let color: NSColor
  private let shape: Shape
  public init(color: NSColor, shape: Shape = .circle) {
    self.color = color
    self.shape = shape
    super.init(frame: .zero)
    setAccessibilityElement(false)
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var intrinsicContentSize: NSSize {
    .init(width: PaxTheme.Chart.proportionDot, height: PaxTheme.Chart.proportionDot)
  }
  public override func draw(_ dirtyRect: NSRect) {
    color.setFill()
    (shape == .square ? NSBezierPath(rect: bounds) : NSBezierPath(ovalIn: bounds)).fill()
  }
  public override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }
}
