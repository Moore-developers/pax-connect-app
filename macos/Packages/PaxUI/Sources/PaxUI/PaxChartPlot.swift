import AppKit

extension PaxTheme {
  /// Shared time-series geometry, independent of units and product thresholds.
  public enum ChartPlot {
    public static let lineWidth: CGFloat = 1.5
    public static let areaOpacity: CGFloat = 0.07
    public static let fadingAreaOpacity: CGFloat = 0.22
    public static let endpointRadius: CGFloat = 2
    public static let selectionRadius: CGFloat = 3
    public static let selectionHalo: CGFloat = 2
    public static let rangePadding = 0.18
    public static let minimumRelativeSpan = 0.25
  }
}

/// Shared plot component for compact trends and full multi-series charts.
/// Adapters supply projected real observations; labels, units and selection stay with the host.
@MainActor
struct PaxChartPlot {
  struct Series {
    let points: [NSPoint]
    let color: NSColor
    /// An explicit lower boundary supports stacked areas without changing any sample value.
    var lowerBoundary: [NSPoint] = []
    var showsEndpoint = true
  }

  let bounds: NSRect
  let series: [Series]
  var fadesAreaFill = false

  /// Monotone cubic (Fritsch-Carlson / monotone-x) through every point; never overshoots
  /// neighbouring samples. One or two points degrade to a dot or a straight segment.
  /// `lower` is a stacked series' boundary beneath this one; it keeps the two curves from crossing.
  static func path(through points: [NSPoint], above lower: [NSPoint] = []) -> NSBezierPath {
    let path = NSBezierPath()
    if let first = points.first {
      path.move(to: first)
      appendSmooth(to: path, through: points, above: lower)
    }
    path.lineCapStyle = .round
    path.lineJoinStyle = .round
    return path
  }

  /// Bezier segments (control1, control2, end) walking `points` forward.
  /// With `lower` (same x positions, screen-lower), control heights are capped by the lower
  /// curve's, which keeps this curve on or above it everywhere without moving any sample.
  static func smoothSegments(
    through points: [NSPoint], above lower: [NSPoint] = []
  ) -> [(NSPoint, NSPoint, NSPoint)] {
    let count = points.count
    guard count > 1 else { return [] }
    let h = (0..<count - 1).map { points[$0 + 1].x - points[$0].x }
    let slope = (0..<count - 1).map { h[$0] > 0 ? (points[$0 + 1].y - points[$0].y) / h[$0] : 0 }
    func sign(_ v: CGFloat) -> CGFloat { v > 0 ? 1 : (v < 0 ? -1 : 0) }
    var tangent = [CGFloat](repeating: 0, count: count)
    for k in 1..<max(1, count - 1) where count > 2 {
      let (s0, s1, h0, h1) = (slope[k - 1], slope[k], h[k - 1], h[k])
      guard h0 > 0, h1 > 0 else { continue }
      let weighted = (s0 * h1 + s1 * h0) / (h0 + h1)
      tangent[k] = (sign(s0) + sign(s1)) * min(abs(s0), abs(s1), 0.5 * abs(weighted))
    }
    func edge(_ s: CGFloat, _ neighbour: CGFloat) -> CGFloat {
      let m = (3 * s - neighbour) / 2
      return sign(m) != sign(s) ? 0 : (abs(m) > 3 * abs(s) ? 3 * s : m)
    }
    if count == 2 {
      tangent[0] = slope[0]
      tangent[1] = slope[0]
    } else {
      tangent[0] = edge(slope[0], tangent[1])
      tangent[count - 1] = edge(slope[count - 2], tangent[count - 2])
    }
    let bound = lower.count == count ? smoothSegments(through: lower) : []
    return (0..<count - 1).map { k in
      let (p0, p1, step) = (points[k], points[k + 1], h[k])
      var c1 = NSPoint(x: p0.x + step / 3, y: p0.y + tangent[k] * step / 3)
      var c2 = NSPoint(x: p1.x - step / 3, y: p1.y - tangent[k + 1] * step / 3)
      if bound.indices.contains(k) {
        c1.y = min(c1.y, bound[k].0.y)
        c2.y = min(c2.y, bound[k].1.y)
      }
      return (c1, c2, p1)
    }
  }

  /// Continues `path` (whose current point is the first point, or the last when `reversed`)
  /// along the same curve the forward stroke uses, so fills and strokes share one shape.
  static func appendSmooth(
    to path: NSBezierPath, through points: [NSPoint], reversed: Bool = false,
    above lower: [NSPoint] = []
  ) {
    let segments = smoothSegments(through: points, above: lower)
    if !reversed {
      for (c1, c2, end) in segments { path.curve(to: end, controlPoint1: c1, controlPoint2: c2) }
      return
    }
    for k in segments.indices.reversed() {
      path.curve(to: points[k], controlPoint1: segments[k].1, controlPoint2: segments[k].0)
    }
  }

  nonisolated static func range(_ values: [Double]) -> ClosedRange<Double> {
    let low = values.min() ?? 0
    let high = values.max() ?? 1
    let span = max(1, high - low, high * PaxTheme.ChartPlot.minimumRelativeSpan)
    let padding = span * PaxTheme.ChartPlot.rangePadding
    return max(0, low - padding)...max(low + 1, high + padding)
  }

  func draw() {
    guard bounds.width > 0, bounds.height > 0 else { return }
    let theme = PaxTheme.ChartPlot.self
    for item in series where !item.points.isEmpty {
      let path = Self.path(through: item.points, above: item.lowerBoundary)
      if item.points.count > 1, !item.lowerBoundary.isEmpty,
        let area = path.copy() as? NSBezierPath
      {
        if let last = item.lowerBoundary.last {
          area.line(to: last)
          Self.appendSmooth(to: area, through: item.lowerBoundary, reversed: true)
        }
        area.close()
        if fadesAreaFill {
          NSGraphicsContext.saveGraphicsState()
          area.addClip()
          NSGradient(
            starting: item.color.withAlphaComponent(theme.fadingAreaOpacity),
            ending: item.color.withAlphaComponent(0)
          )?.draw(in: bounds, angle: 90)
          NSGraphicsContext.restoreGraphicsState()
        } else {
          item.color.withAlphaComponent(theme.areaOpacity).setFill()
          area.fill()
        }
      }
      item.color.setStroke()
      path.lineWidth = theme.lineWidth
      path.stroke()
      if item.showsEndpoint, let last = item.points.last {
        Self.dot(last, radius: theme.endpointRadius, color: item.color)
      }
    }
  }

  static func selection(at point: NSPoint, color: NSColor) {
    dot(
      point, radius: PaxTheme.ChartPlot.selectionRadius + PaxTheme.ChartPlot.selectionHalo,
      color: PaxTheme.Color.card)
    dot(point, radius: PaxTheme.ChartPlot.selectionRadius, color: color)
  }

  static func dot(_ point: NSPoint, radius: CGFloat, color: NSColor) {
    color.setFill()
    NSBezierPath(
      ovalIn: NSRect(
        x: point.x - radius, y: point.y - radius, width: radius * 2, height: radius * 2)
    ).fill()
  }
}
