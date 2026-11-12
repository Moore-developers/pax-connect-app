import AppKit

/// 有历史才绘制的测速柱条；每根柱对应一个调用者提供的样本，不生成预测。
public final class PaxMiniBars: NSView {
  private let values: [Double]
  private let color: NSColor
  public init(values: [Double], color: NSColor, summary: String) {
    self.values = values.filter { $0.isFinite && $0 >= 0 }
    self.color = color
    super.init(frame: .zero)
    setAccessibilityElement(true)
    setAccessibilityRole(.image)
    setAccessibilityLabel(summary)
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override func draw(_ dirtyRect: NSRect) {
    guard values.count >= 3, let maximum = values.max(), maximum > 0 else { return }
    let plot = bounds.insetBy(dx: PaxTheme.Spacing.xs, dy: PaxTheme.Spacing.xs)
    let step = plot.width / CGFloat(values.count)
    for (index, value) in values.enumerated() {
      let width = step * (1 - PaxTheme.Chart.barGapFraction)
      let rect = NSRect(
        x: plot.minX + CGFloat(index) * step, y: plot.minY, width: width,
        height: CGFloat(value / maximum) * plot.height)
      color.withAlphaComponent(index == values.count - 1 ? 1 : 1 - PaxTheme.Chart.barGapFraction)
        .setFill()
      NSBezierPath(
        roundedRect: rect, xRadius: PaxTheme.Chart.proportionGap,
        yRadius: PaxTheme.Chart.proportionGap
      ).fill()
    }
  }
}
