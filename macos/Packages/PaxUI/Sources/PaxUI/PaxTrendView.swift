import AppKit

/// 趋势样本默认等距，可指定窗口位置；nil 表示缺测，label 由调用方格式化。
public struct PaxTrendSample: Equatable {
  public let value: Double?
  public let label: String
  /// 在横轴时间窗中的位置；nil 保留既有等距样本布局。
  public let position: Double?
  public init(value: Double?, label: String, position: Double? = nil) {
    self.value = value.flatMap { $0.isFinite && $0 >= 0 ? $0 : nil }
    self.label = label
    self.position = position.flatMap { $0.isFinite ? min(1, max(0, $0)) : nil }
  }
}

/// 原生非负数值趋势：只消费历史值，悬停/键盘选择通过回调与其他视图联动。
public final class PaxTrendView: NSView {
  public enum Presentation {
    case standard, node, sparkline, rangeSparkline, request, latency
  }
  public var presentation: Presentation = .standard { didSet { rebuild() } }
  public var connectsMissingSamples = false { didSet { rebuild() } }
  var renderedPathCount: Int { segments.count }
  private var pinned = false
  public var tooltipCoordinator: PaxChartTooltipCoordinator?
  private(set) var readoutVisible = false
  public var interactionColor: NSColor? { didSet { needsDisplay = true } }
  /// 需要常驻峰值标注的紧凑图表可预留顶部标注区。
  public var topInset: CGFloat = 0 { didSet { rebuild() } }

  public var samples: [PaxTrendSample] = [] {
    didSet {
      guard samples != oldValue else { return }
      tooltipCoordinator?.close(from: self)
      pinned = false
      highlight(nil)
      rebuild()
      setAccessibilityLabel(
        "趋势，" + samples.map { $0.label }.joined(separator: "；"))
    }
  }
  public var color: NSColor = PaxTheme.Trend.lineColor { didSet { needsDisplay = true } }
  public var minimumUpperBound: Double = 1 { didSet { rebuild() } }
  public var scaleStep: Double = 1 { didSet { rebuild() } }
  var peakIndex: Int? {
    samples.indices.filter { samples[$0].value != nil }.max {
      (samples[$0].value ?? 0) < (samples[$1].value ?? 0)
    }
  }
  public var onSample: ((Int?) -> Void)?
  private var activeIndex: Int?
  private var segments: [[NSPoint]] = []
  private var points: [NSPoint?] = []
  private var plot = NSRect.zero
  public override var isFlipped: Bool { true }
  public override var acceptsFirstResponder: Bool { true }
  public override init(frame frameRect: NSRect) {
    super.init(frame: frameRect)
    setAccessibilityElement(true)
    setAccessibilityRole(.image)
  }
  public convenience init() { self.init(frame: .zero) }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override func layout() {
    super.layout()
    rebuild()
  }
  /// 外部色条可同步选择；不再次发送回调。
  public func highlight(_ index: Int?) {
    activeIndex = index.flatMap { samples.indices.contains($0) ? $0 : nil }
    setAccessibilityValue(
      activeIndex.map { samples[$0].label } ?? "")
    needsDisplay = true
  }
  func fraction(at index: Int) -> CGFloat {
    if let position = samples[index].position { return CGFloat(position) }
    return CGFloat(index) / CGFloat(max(1, samples.count - 1))
  }
  func nearestSample(to fraction: CGFloat) -> Int? {
    guard !samples.isEmpty else { return nil }
    if samples.allSatisfy({ $0.position == nil }) {
      return min(samples.count - 1, max(0, Int((fraction * CGFloat(samples.count - 1)).rounded())))
    }
    return samples.indices.min {
      abs(self.fraction(at: $0) - fraction) < abs(self.fraction(at: $1) - fraction)
    }
  }
  private func rebuild() {
    let c = PaxTheme.MiniChart.self
    plot = bounds.insetBy(dx: PaxTheme.Trend.inset, dy: 0)
    switch presentation {
    case .latency:
      plot.origin.y = PaxTheme.Trend.inset
      plot.size.height = max(1, bounds.height - PaxTheme.Trend.inset * 2)
    case .standard:
      plot.origin.y = PaxTheme.Trend.labelHeight + PaxTheme.Trend.labelGap
      plot.size.height = max(1, bounds.height - plot.origin.y - PaxTheme.Trend.inset)
    case .node:
      // `topInset` > 0：绘图区从该留白起一直铺到底（峰值标注占用顶部留白），否则沿用节点比例。
      plot.origin.y =
        topInset > 0 ? topInset : bounds.height * (c.nodeBottom - c.nodeHeight)
      let bottom = topInset > 0 ? bounds.height : bounds.height * c.nodeBottom
      plot.size.height = max(1, bottom - plot.origin.y)
    case .sparkline, .rangeSparkline, .request:
      plot.origin.y = bounds.height * (c.compactBottom - c.compactHeight)
      plot.size.height = bounds.height * c.compactHeight
    }
    guard plot.width > 0 else {
      segments = []
      points = []
      needsDisplay = true
      return
    }
    let ceiling = Self.ceiling(
      for: samples.compactMap(\.value), minimum: minimumUpperBound, step: scaleStep)
    let values = samples.compactMap(\.value)
    let range = PaxChartPlot.range(values)
    let minimum =
      (presentation == .rangeSparkline || presentation == .latency) ? range.lowerBound : 0
    let maximum =
      (presentation == .rangeSparkline || presentation == .latency) ? range.upperBound : ceiling
    points = samples.enumerated().map { index, sample in
      sample.value.map {
        NSPoint(
          x: plot.minX + fraction(at: index) * plot.width,
          y: plot.maxY - CGFloat(($0 - minimum) / max(1, maximum - minimum)) * plot.height)
      }
    }
    segments = []
    var run: [NSPoint] = []
    func flush() {
      if !run.isEmpty { segments.append(run) }
      run = []
    }
    for point in points {
      if let point { run.append(point) } else if !connectsMissingSamples { flush() }
    }
    flush()
    needsDisplay = true
  }
  static func ceiling(for values: [Double], minimum: Double, step: Double) -> Double {
    let step = step.isFinite && step > 0 ? step : 1
    let minimum = minimum.isFinite && minimum > 0 ? minimum : step
    return max(minimum, ceil((values.max() ?? 0) / step) * step)
  }
  static func path(through points: [NSPoint]) -> NSBezierPath {
    PaxChartPlot.path(through: points)
  }
  public override func updateTrackingAreas() {
    super.updateTrackingAreas()
    for area in trackingAreas { removeTrackingArea(area) }
    addTrackingArea(
      NSTrackingArea(
        rect: bounds,
        options: [.activeInKeyWindow, .inVisibleRect, .mouseMoved, .mouseEnteredAndExited],
        owner: self))
  }
  public override func mouseEntered(with event: NSEvent) {
    guard tooltipCoordinator?.suppressesHover(in: self) != true else { return }
    if pinned, let activeIndex { choose(activeIndex) } else { mouseMoved(with: event) }
  }
  public override func mouseMoved(with event: NSEvent) {
    guard tooltipCoordinator?.suppressesHover(in: self) != true else { return }
    guard !samples.isEmpty else { return }
    guard !pinned else {
      if tooltipCoordinator?.isShowing(from: self) == false { choose(activeIndex) }
      return
    }
    let x = convert(event.locationInWindow, from: nil).x
    choose(nearestSample(to: (x - plot.minX) / max(1, plot.width)))
  }
  public override func mouseDown(with event: NSEvent) {
    window?.makeFirstResponder(self)
    if pinned {
      pinned = false
      choose(nil)
    } else {
      mouseMoved(with: event)
      pinned = activeIndex != nil
    }
  }
  public override func mouseExited(with event: NSEvent) {
    if !pinned { choose(nil) }
  }
  private func choose(_ index: Int?) {
    highlight(index)
    onSample?(index)
    guard let activeIndex else {
      tooltipCoordinator?.close(from: self)
      setReadoutVisible(false)
      return
    }
    let sample = samples[activeIndex]
    let text =
      sample.value == nil
      ? (sample.label.components(separatedBy: " · ").first ?? "") + " · 未采集"
      : sample.label
    let point = points.indices.contains(activeIndex) ? points[activeIndex] : nil
    let anchorPoint =
      point
      ?? NSPoint(
        x: plot.minX + fraction(at: activeIndex) * plot.width,
        y: plot.midY)
    tooltipCoordinator?.show(
      text, from: self, anchor: NSRect(origin: anchorPoint, size: NSSize(width: 1, height: 1))
    ) { [weak self] in
      guard let self else { return }
      self.pinned = false
      self.highlight(nil)
      self.setReadoutVisible(false)
      self.onSample?(nil)
    }
    setReadoutVisible(tooltipCoordinator?.activeOwner === self)
  }
  private func setReadoutVisible(_ visible: Bool) {
    readoutVisible = visible
    needsDisplay = true
  }
  public override func viewDidMoveToWindow() {
    super.viewDidMoveToWindow()
    if window == nil {
      tooltipCoordinator?.close(from: self)
      pinned = false
      highlight(nil)
      setReadoutVisible(false)
    }
  }
  public override func keyDown(with event: NSEvent) {
    guard !samples.isEmpty else { return }
    switch event.keyCode {
    case 123: choose(max(0, (activeIndex ?? samples.count - 1) - 1))
    case 124: choose(min(samples.count - 1, (activeIndex ?? -1) + 1))
    case 53:
      pinned = false
      choose(nil)
    default: super.keyDown(with: event)
    }
  }
  public override func draw(_ dirtyRect: NSRect) {
    guard plot.width > 0 else { return }
    PaxTheme.Color.cardBorder.withAlphaComponent(PaxTheme.Trend.gridOpacity).setStroke()
    for fraction
      in (presentation == .standard
      ? [CGFloat(1) / 3, CGFloat(2) / 3] : presentation == .node ? PaxTheme.MiniChart.nodeGrid : [])
    {
      let line = NSBezierPath()
      line.move(
        to: NSPoint(
          x: plot.minX,
          y: presentation == .node ? bounds.height * fraction : plot.minY + plot.height * fraction))
      line.line(
        to: NSPoint(
          x: plot.maxX,
          y: presentation == .node ? bounds.height * fraction : plot.minY + plot.height * fraction))
      line.lineWidth = PaxTheme.Trend.gridWidth
      line.stroke()
    }
    if samples.isEmpty { return }
    PaxChartPlot(
      bounds: plot,
      series: segments.map { run in
        .init(
          points: run, color: color,
          lowerBoundary: run.map { NSPoint(x: $0.x, y: plot.maxY) })
      }
    ).draw()
    if readoutVisible, let activeIndex, points.indices.contains(activeIndex) {
      let x = plot.minX + fraction(at: activeIndex) * plot.width
      PaxTheme.Trend.markerColor.setStroke()
      let marker = NSBezierPath()
      marker.move(to: NSPoint(x: x, y: plot.minY))
      marker.line(to: NSPoint(x: x, y: plot.maxY))
      marker.lineWidth = PaxTheme.Trend.gridWidth
      marker.stroke()
      if let point = points[activeIndex] {
        PaxChartPlot.selection(at: point, color: interactionColor ?? color)
      }
    }
    let index =
      (presentation == .sparkline || presentation == .rangeSparkline || presentation == .latency)
      ? nil : peakIndex
    if let index, points.indices.contains(index) {
      let samplePoint = points[index]
      let point =
        samplePoint
        ?? NSPoint(
          x: plot.minX + fraction(at: index) * plot.width,
          y: plot.minY)
      if presentation == .request {
        color.setFill()
        let radius = PaxTheme.Trend.pointRadius
        NSBezierPath(
          ovalIn: NSRect(
            x: point.x - radius, y: point.y - radius,
            width: radius * 2, height: radius * 2)
        ).fill()
        return
      }
      let label = samples[index].label as NSString
      let font = PaxTheme.Typography.label(size: PaxTheme.Trend.labelSize)
      let labelWidth = min(plot.width, ceil(label.size(withAttributes: [.font: font]).width))
      let labelRect = NSRect(
        x: min(max(point.x - labelWidth / 2, plot.minX), plot.maxX - labelWidth),
        y: max(bounds.minY, point.y - (PaxTheme.Trend.labelGap + PaxTheme.Trend.labelHeight)),
        width: labelWidth, height: PaxTheme.Trend.labelHeight)
      let paragraph = NSMutableParagraphStyle()
      paragraph.alignment = .center
      paragraph.lineBreakMode = .byTruncatingTail
      label.draw(
        in: labelRect,
        withAttributes: [
          .font: font,
          .foregroundColor: color, .paragraphStyle: paragraph,
        ])
      guard samplePoint != nil else { return }
      PaxTheme.Trend.markerColor.setStroke()
      let marker = NSBezierPath()
      marker.move(to: NSPoint(x: point.x, y: labelRect.maxY))
      marker.line(to: NSPoint(x: point.x, y: point.y - PaxTheme.Trend.pointRadius))
      marker.lineWidth = PaxTheme.Trend.gridWidth
      marker.stroke()
      color.setFill()
      let radius = PaxTheme.Trend.pointRadius
      NSBezierPath(
        ovalIn: NSRect(
          x: point.x - radius, y: point.y - radius, width: radius * 2, height: radius * 2)
      ).fill()
    }
  }

  public override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }
}
