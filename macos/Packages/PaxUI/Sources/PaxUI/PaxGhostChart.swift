import AppKit

/// 空态占位图：低对比的示意柱 / 折线，只表示「这里将显示什么」，不承载数据，也不进入无障碍树。
public final class PaxGhostChart: NSView {
  public enum Style { case bars, line }
  public let style: Style
  public override var isFlipped: Bool { true }

  /// 示意柱高（占绘图区高度的比例），固定序列，不随机，避免每次重绘闪动。
  private static let barShape: [CGFloat] = [
    0.28, 0.42, 0.34, 0.56, 0.48, 0.66, 0.52, 0.74, 0.6, 0.46, 0.58, 0.4,
    0.5, 0.7, 0.62, 0.78, 0.54, 0.44, 0.6, 0.36, 0.48, 0.3, 0.38, 0.26,
  ]

  public init(style: Style) {
    self.style = style
    super.init(frame: .zero)
    setAccessibilityElement(false)
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }

  public override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }

  public override func draw(_ dirtyRect: NSRect) {
    guard bounds.width > 0, bounds.height > 0 else { return }
    effectiveAppearance.performAsCurrentDrawingAppearance {
      switch style {
      case .bars: drawBars()
      case .line: drawLine()
      }
    }
  }

  private func drawBars() {
    let count = Self.barShape.count
    let gap = PaxTheme.Chart.proportionGap * 2
    let width = max(1, (bounds.width - gap * CGFloat(count - 1)) / CGFloat(count))
    PaxTheme.Color.navigationHover.setFill()
    for (index, fraction) in Self.barShape.enumerated() {
      let height = max(2, bounds.height * fraction)
      let rect = NSRect(
        x: CGFloat(index) * (width + gap), y: bounds.height - height, width: width, height: height)
      NSBezierPath(roundedRect: rect, xRadius: min(2, width / 2), yRadius: min(2, width / 2)).fill()
    }
  }

  private func drawLine() {
    let grid = NSBezierPath()
    grid.lineWidth = PaxTheme.Layout.cardBorder
    grid.setLineDash([3, 4], count: 2, phase: 0)
    for fraction in [0.2, 0.5, 0.8] as [CGFloat] {
      let y = (bounds.height * fraction).rounded() + 0.5
      grid.move(to: NSPoint(x: 0, y: y))
      grid.line(to: NSPoint(x: bounds.width, y: y))
    }
    PaxTheme.Color.cardBorder.setStroke()
    grid.stroke()
    let wave = NSBezierPath()
    wave.lineWidth = 2
    wave.lineCapStyle = .round
    wave.setLineDash([1, 6], count: 2, phase: 0)
    let steps = 48
    for step in 0...steps {
      let t = CGFloat(step) / CGFloat(steps)
      let y = bounds.height * (0.5 - 0.16 * sin(t * .pi * 3) - 0.08 * t)
      let point = NSPoint(x: bounds.width * t, y: y)
      step == 0 ? wave.move(to: point) : wave.line(to: point)
    }
    PaxTheme.Color.textSecondary.withAlphaComponent(0.45).setStroke()
    wave.stroke()
  }
}
