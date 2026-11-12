import AppKit

/// Part-to-whole chart shared by usage summaries, model analysis, and request details.
public final class PaxDonutChart: NSView {
  private let values: [Double]
  private let colors: [NSColor]
  public let center: String
  private let compact: Bool
  private let centerFont: NSFont
  public init(
    segments: [PaxChartSegment], center: String, compact: Bool = false,
    colors: [NSColor] = PaxTheme.Chart.categorySeries, centerFont: NSFont = PaxTheme.Typography.time
  ) {
    self.values = segments.map { $0.value.isFinite ? max(0, $0.value) : 0 }
    self.colors = colors
    self.center = center
    self.compact = compact
    self.centerFont = centerFont
    super.init(frame: .zero)
    setAccessibilityElement(true)
    setAccessibilityRole(.image)
    setAccessibilityLabel(segments.map { "\($0.label)：\($0.value)" }.joined(separator: "；"))
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var intrinsicContentSize: NSSize {
    NSSize(
      width: compact ? PaxTheme.Chart.compactDonutSize : PaxTheme.Chart.donutSize,
      height: compact ? PaxTheme.Chart.compactDonutSize : PaxTheme.Chart.donutSize)
  }
  public override func draw(_ dirtyRect: NSRect) {
    let width = compact ? PaxTheme.Chart.compactDonutStroke : PaxTheme.Chart.donutStroke
    let radius = max(0, min(bounds.width, bounds.height) / 2 - width / 2)
    let point = NSPoint(x: bounds.midX, y: bounds.midY)
    let total = values.reduce(0, +)
    var angle: CGFloat = 90
    if total == 0 {
      let track = NSBezierPath(ovalIn: bounds.insetBy(dx: width / 2, dy: width / 2))
      track.lineWidth = width
      PaxTheme.Color.cardBorder.setStroke()
      track.stroke()
    }
    for (index, value) in values.enumerated() where total > 0 && value > 0 {
      let end = angle - CGFloat(value / total) * 360
      let path = NSBezierPath()
      path.appendArc(
        withCenter: point, radius: radius, startAngle: angle, endAngle: end, clockwise: true)
      path.lineWidth = width
      (colors.isEmpty ? PaxTheme.Color.series[0] : colors[index % colors.count]).setStroke()
      path.stroke()
      angle = end
    }
    let attributes: [NSAttributedString.Key: Any] = [
      .font: centerFont, .foregroundColor: PaxTheme.Color.text,
    ]
    let size = (center as NSString).size(withAttributes: attributes)
    (center as NSString).draw(
      at: NSPoint(x: bounds.midX - size.width / 2, y: bounds.midY - size.height / 2),
      withAttributes: attributes)
  }
  public override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }
}
