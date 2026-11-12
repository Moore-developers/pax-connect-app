import AppKit

/// A bounded proportion mark reused by summaries, rankings, and category distributions.
public final class PaxShareBar: NSView {
  private let segments: [PaxChartSegment]
  private let colors: [NSColor]
  private let striped: Set<Int>
  /// Fills each segment lighter→colour left to right instead of a flat colour.
  private let gradient: Bool
  /// Denominator may include categories omitted from this row (e.g. a single model's share).
  private let total: Double
  public init(
    segments: [PaxChartSegment], colors: [NSColor], striped: Set<Int> = [], gradient: Bool = false,
    total: Double? = nil
  ) {
    self.gradient = gradient
    self.segments = segments
    self.colors = colors
    self.striped = striped
    self.total = total ?? segments.reduce(0) { $0 + max(0, $1.value) }
    super.init(frame: .zero)
    setAccessibilityElement(true)
    setAccessibilityRole(.image)
    setAccessibilityLabel(segments.map { "\($0.label)：\($0.value)" }.joined(separator: "，"))
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var intrinsicContentSize: NSSize {
    .init(width: NSView.noIntrinsicMetric, height: PaxTheme.Chart.shareBarHeight)
  }
  public override func draw(_ dirtyRect: NSRect) {
    let path = NSBezierPath(
      roundedRect: bounds, xRadius: PaxTheme.Radius.control / 2,
      yRadius: PaxTheme.Radius.control / 2)
    NSGradient(colors: PaxTheme.DashboardColor.track)?.draw(in: path, angle: 0)
    guard total.isFinite, total > 0 else { return }
    NSGraphicsContext.saveGraphicsState()
    path.addClip()
    var x = bounds.minX
    for (index, segment) in segments.enumerated() {
      let width = max(0, min(total, segment.value)) / total * bounds.width
      let rect = NSRect(
        x: x, y: 0, width: max(0, width - PaxTheme.Layout.cardBorder), height: bounds.height)
      let color = colors.isEmpty ? PaxTheme.Routes.blue : colors[index % colors.count]
      color.setFill()
      // A lone segment gets a rounded end so it reads as a pill on the track.
      let shape =
        segments.count == 1
        ? NSBezierPath(roundedRect: rect, xRadius: bounds.height / 2, yRadius: bounds.height / 2)
        : NSBezierPath(rect: rect)
      if gradient {
        let light = color.blended(withFraction: 0.38, of: .white) ?? color
        NSGradient(starting: light, ending: color)?.draw(in: shape, angle: 0)
      } else {
        shape.fill()
      }
      if striped.contains(index) {
        NSGraphicsContext.saveGraphicsState()
        shape.addClip()
        Self.stripes(in: rect)
        NSGraphicsContext.restoreGraphicsState()
      }
      x += width
    }
    NSGraphicsContext.restoreGraphicsState()
  }
  public static func stripes(in rect: NSRect) {
    guard rect.width > 0, rect.height > 0 else { return }
    NSGraphicsContext.saveGraphicsState()
    NSBezierPath(rect: rect).addClip()
    let c = PaxTheme.Chart.self
    c.stripeColor.withAlphaComponent(c.stripeOpacity).setStroke()
    let path = NSBezierPath()
    path.lineWidth = c.stripeWidth
    for x in stride(from: rect.minX - rect.height, through: rect.maxX, by: c.stripePeriod) {
      path.move(to: .init(x: x, y: rect.minY))
      path.line(to: .init(x: x + rect.height, y: rect.maxY))
    }
    path.stroke()
    NSGraphicsContext.restoreGraphicsState()
  }
}
