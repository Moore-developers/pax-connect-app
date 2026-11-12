import AppKit

/// 堆叠比例条与文字摘要；零总量不画假比例。
public final class PaxProportionView: NSView {
  /// Consumers can match their surrounding table while charts retain the default caption size.
  public var legendFont = PaxTheme.Typography.label(size: PaxTheme.Dashboard.captionFont) {
    didSet { needsDisplay = true }
  }
  /// Allows consumers to compose their own legend while reusing the same bar.
  public var showsLegend = true {
    didSet {
      invalidateIntrinsicContentSize()
      needsDisplay = true
    }
  }
  /// Optional pre-rounded percentages; narrow segments retain hover/AX detail.
  public var percentageLabels: [String]? { didSet { needsDisplay = true } }
  public var compact = false {
    didSet {
      invalidateIntrinsicContentSize()
      needsDisplay = true
    }
  }
  public var gradients: [[NSColor]] = PaxTheme.Color.series.map { [$0, $0] } {
    didSet { needsDisplay = true }
  }
  public var segments: [PaxChartSegment] {
    didSet {
      updateSummary()
      needsDisplay = true
    }
  }
  public init(segments: [PaxChartSegment]) {
    self.segments = segments
    super.init(frame: .zero)
    setAccessibilityElement(true)
    setAccessibilityRole(.image)
    updateSummary()
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var isFlipped: Bool { true }
  public override var intrinsicContentSize: NSSize {
    NSSize(
      width: NSView.noIntrinsicMetric,
      height: showsLegend
        ? (compact ? PaxTheme.Chart.proportionCompactHeight : PaxTheme.Chart.proportionHeight)
        : (compact ? PaxTheme.Chart.proportionCompactBar : PaxTheme.Chart.proportionBar))
  }
  private func updateSummary() {
    let total = segments.reduce(0) { $0 + max(0, $1.value) }
    let text =
      total > 0
      ? segments.map { "\($0.label) \(Int(max(0, $0.value) / total * 100))%" }.joined(
        separator: "，") : "暂无分类统计"
    setAccessibilityLabel(text)
  }
  public override func draw(_ dirtyRect: NSRect) {
    let total = segments.reduce(0) { $0 + max(0, $1.value) }
    guard total > 0 else { return }
    let c = PaxTheme.Chart.self
    var x: CGFloat = 0
    for (index, segment) in segments.enumerated() {
      let palette = gradients.isEmpty ? PaxTheme.Color.series.map { [$0, $0] } : gradients
      let stops = palette[index % palette.count]
      let color = stops.last ?? PaxTheme.Color.textSecondary
      let width = bounds.width * max(0, segment.value) / total
      let bar = NSRect(
        x: x, y: 0, width: max(0, width - c.proportionGap),
        height: compact ? c.proportionCompactBar : c.proportionBar)
      let shape = NSBezierPath(
        roundedRect: bar, xRadius: c.proportionRadius, yRadius: c.proportionRadius)
      (NSGradient(colors: stops.isEmpty ? [color, color] : stops))?.draw(in: shape, angle: 0)
      let number =
        (percentageLabels.flatMap { $0.indices.contains(index) ? $0[index] : nil }
        ?? "\(Int(max(0, segment.value) / total * 100))%") as NSString
      let attrs: [NSAttributedString.Key: Any] = [
        .font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.captionFont, weight: .semibold),
        .foregroundColor: PaxTheme.Color.control,
      ]
      let size = number.size(withAttributes: attrs)
      if !compact && bar.width >= size.width + PaxTheme.Spacing.tight {
        number.draw(
          at: NSPoint(
            x: bar.midX - size.width / 2,
            y: (bar.height - size.height) / 2), withAttributes: attrs)
      }
      x += width
      guard showsLegend else { continue }
      let text = segment.label as NSString
      let labelAttrs: [NSAttributedString.Key: Any] = [
        .font: legendFont,
        .foregroundColor: PaxTheme.Color.textSecondary,
      ]
      let textWidth = text.size(withAttributes: labelAttrs).width
      let labelX =
        index == segments.count - 1 && index > 0
        ? bounds.width - textWidth - c.proportionLegendInset
        : CGFloat(index) * bounds.width / CGFloat(max(1, segments.count))
      color.setFill()
      NSBezierPath(
        ovalIn: NSRect(
          x: labelX,
          y: (compact ? c.proportionCompactLabel : c.proportionLabel)
            + c.proportionDotOffset,
          width: c.proportionDot, height: c.proportionDot)
      ).fill()
      text.draw(
        at: NSPoint(
          x: labelX + c.proportionLegendInset,
          y: compact ? c.proportionCompactLabel : c.proportionLabel),
        withAttributes: labelAttrs)
    }
  }

  public override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }
}

/// 真实样本折线的纯呈现；没有数据不画趋势，不做网络预测。
public final class PaxSparkline: NSView {
  private let values: [Double]
  public init(values: [Double], summary: String) {
    self.values = values
    super.init(frame: .zero)
    setAccessibilityElement(true)
    setAccessibilityRole(.image)
    setAccessibilityLabel(summary)
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var intrinsicContentSize: NSSize {
    NSSize(width: NSView.noIntrinsicMetric, height: PaxTheme.Chart.sparklineHeight)
  }
  public override func draw(_ dirtyRect: NSRect) {
    guard values.count > 1, values.allSatisfy({ $0.isFinite && $0 >= 0 }) else { return }
    let plot = bounds.insetBy(dx: PaxTheme.Spacing.xs, dy: PaxTheme.Spacing.tight)
    guard plot.width > 0, plot.height > 0 else { return }
    let range = PaxChartPlot.range(values)
    let columns = max(1, Int(convertToBacking(plot).width.rounded(.up)))
    let points = drawingIndices(columns: columns).map { index in
      let value = values[index]
      return NSPoint(
        x: plot.minX + CGFloat(index) / CGFloat(values.count - 1) * plot.width,
        y: plot.minY + (value - range.lowerBound) / (range.upperBound - range.lowerBound)
          * plot.height)
    }
    PaxChartPlot(
      bounds: plot,
      series: [
        .init(
          points: points, color: PaxTheme.Color.series[0],
          lowerBoundary: points.map { NSPoint(x: $0.x, y: plot.minY) })
      ]
    ).draw()
  }
  /// Keep extrema in time order within each device-pixel column. Passing tens of
  /// thousands of subpixel zigzags to CoreGraphics makes area rasterization stall.
  /// Source values, peak/range and original request-index positions remain intact.
  private func drawingIndices(columns: Int) -> [Int] {
    guard values.count > columns * 2 else { return Array(values.indices) }
    var result = [0]
    var bucket = -1
    var low = 0
    var high = 0
    func append(_ index: Int) {
      if result.last != index { result.append(index) }
    }
    func flush() {
      append(min(low, high))
      append(max(low, high))
    }
    for index in values.indices {
      let next = index * columns / values.count
      if next != bucket {
        if bucket >= 0 { flush() }
        bucket = next
        low = index
        high = index
      } else {
        if values[index] < values[low] { low = index }
        if values[index] > values[high] { high = index }
      }
    }
    flush()
    append(values.count - 1)
    return result
  }

  public override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }
}
