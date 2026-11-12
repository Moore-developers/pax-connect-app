import AppKit

/// 分类份额的通用值，颜色按类别顺序从图表 token 取用。
public struct PaxChartSegment {
  public let label: String
  public let value: Double
  public init(_ label: String, value: Double) {
    self.label = label
    self.value = value
  }
}

/// 时间样本的已格式化标签与非负分类值；缺样本由调用方明确标示，不插值填补。
public struct PaxChartSample: Equatable {
  public var partial = false
  public var interval: TimeInterval = 0
  public let label: String
  public let values: [Double]
  /// Optional actual sample time; empty values mark a gap for time-series charts.
  public let at: Date?
  /// 柱顶标签的金额行（`labeledColumns`）；缺省显示「—」，不补 0。
  public var amountLabel: String?
  /// 悬停浮层的分行文案（含时间行）；缺省按系列标签与 `unit` 生成。
  public var readout: [String]?
  public init(_ label: String, values: [Double], at: Date? = nil) {
    self.label = label
    self.values = values
    self.at = at
  }
}

/// 自绘时间堆叠柱，用于流量概览与组件目录。单位、值与样本都由调用方提供。
public final class PaxStackedChart: NSView {
  public enum Presentation { case categorical, traffic }
  public var presentation: Presentation = .categorical { didSet { needsDisplay = true } }
  /// Opt-in bar look: `standard` is flat; `gradientRounded` shades each layer top-to-bottom, rounds
  /// the topmost layer's top corners and drops the in-plot readout (the hover tooltip stays).
  /// `funnel`: wide bars, no track, past periods hatched / current solid, a faint band between bar tops,
  /// whole-column hover highlight, no header readout (first series stacks on top). `labeledColumns`:
  /// stacked columns with a fixed date + amount header. `summaryColumns`: moving total labels,
  /// bottom dates and a faint current-period column, sharing the same breakdown tooltip.
  public enum Style { case standard, gradientRounded, funnel, labeledColumns, summaryColumns }
  public var style: Style = .standard { didSet { needsDisplay = true } }
  /// With `gradientRounded`: solid layers with 45° stripes instead of the shading.
  public var hatched = false { didSet { needsDisplay = true } }
  /// 分类柱占时间列的宽度比例；未指定时沿用默认柱间距。
  public var barWidthFraction: CGFloat? { didSet { needsDisplay = true } }
  /// Categorical charts may reserve at least this many slots; fewer samples form a centered group.
  public var minimumColumnCount: Int? { didSet { needsDisplay = true } }
  /// Optional width-only slot floor; preserves narrow bars while centers fill the plot.
  public var minimumBarColumnCount: Int? { didSet { needsDisplay = true } }
  /// Opt-in categorical axis labels: show as many centered labels as fit without overlap.
  public var automaticallySpacesCategoryLabels = false { didSet { needsDisplay = true } }
  /// Optional secondary-axis line, indexed to the categorical bars; nil keeps the original chart.
  public var valueLine: ValueLine? {
    didSet {
      updateSummary()
      updateSelection()
      needsDisplay = true
    }
  }
  /// Optional structured hover cards in sample order; empty keeps the existing text readout.
  public var hoverDetails: [PaxAnalysisChart.Hover] = [] { didSet { updateSelection() } }
  /// 摘要柱的可选固定宽度；首尾柱贴绘图区边缘，剩余宽度均分为间距。
  public var fixedBarWidth: CGFloat? { didSet { needsDisplay = true } }
  /// 摘要柱的柱顶合计；关闭时仍保留悬停读数与坐标刻度。
  public var showsSummaryAmounts = true { didSet { needsDisplay = true } }
  /// Funnel / labeled styles: the sample drawn solid (default the last); earlier ones are hatched.
  public var currentIndex: Int? { didSet { needsDisplay = true } }
  public var tooltipCoordinator: PaxChartTooltipCoordinator?
  /// Optional funnel-axis formatting; sample values and hover readouts stay unchanged.
  public var axisLabel: ((Double) -> String)? { didSet { needsDisplay = true } }
  /// Labeled style: rounds the bottom-trailing corner so a chart flush with its card's corner follows it.
  public var bottomTrailingRadius: CGFloat = 0 { didSet { needsDisplay = true } }
  /// Layer colors in series order; defaults to the shared Dashboard series.
  public var colors: [NSColor] = PaxTheme.Color.series { didSet { needsDisplay = true } }
  public var samples: [PaxChartSample] {
    didSet {
      guard samples != oldValue else { return }
      let previousHover = hoverIndex
      let previousPin = pinnedIndex
      tooltipCoordinator?.close(from: self)
      func restore(_ index: Int?) -> Int? {
        guard let index, oldValue.indices.contains(index), let at = oldValue[index].at else {
          return nil
        }
        let matches = samples.indices.filter {
          samples[$0].at == at && samples[$0].label == oldValue[index].label
        }
        return matches.count == 1 ? matches.first : nil
      }
      hoverIndex = restore(previousHover)
      pinnedIndex = restore(previousPin)
      toolTip = nil
      setAccessibilityValue(nil)
      updateSummary()
      updateSelection()
      needsDisplay = true
    }
  }
  public var unit: String {
    didSet {
      updateSummary()
      needsDisplay = true
    }
  }
  let seriesLabels: [String]
  private var selectionDamage = NSRect.zero
  private var mouseTrackingArea: NSTrackingArea?
  var hoverIndex: Int?
  var pinnedIndex: Int?
  var headerHeight: CGFloat {
    style == .gradientRounded ? PaxTheme.Chart.compactHeader : PaxTheme.Chart.header
  }
  var selectedIndex: Int? { hoverIndex ?? pinnedIndex }
  public init(samples: [PaxChartSample], seriesLabels: [String], unit: String) {
    self.samples = samples
    self.seriesLabels = seriesLabels
    self.unit = unit
    super.init(frame: .zero)
    wantsLayer = true
    layerContentsRedrawPolicy = .onSetNeedsDisplay
    setAccessibilityElement(true)
    setAccessibilityRole(.image)
    updateSummary()
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var isFlipped: Bool { true }
  public override func setFrameSize(_ newSize: NSSize) {
    let changed = frame.size != newSize
    super.setFrameSize(newSize)
    if changed { needsDisplay = true }
  }

  public override var acceptsFirstResponder: Bool { true }
  public override var intrinsicContentSize: NSSize {
    NSSize(width: NSView.noIntrinsicMetric, height: PaxTheme.Chart.height)
  }
  private func updateSummary() {
    setAccessibilityLabel(
      "时间流量图，单位 \(unit)，" + (valueLine.map { "\($0.name)，单位\($0.unit)，" } ?? "")
        + samples.map {
          "\($0.label)：\($0.values.reduce(0, +).formatted(.number.precision(.fractionLength(2))))"
        }.joined(separator: "；"))
  }
  public override func updateTrackingAreas() {
    super.updateTrackingAreas()
    // AppKit maintains this area's visible rect during scrolling and resizing.
    // Keep its identity instead of unnecessarily resetting mouse tracking.
    if let mouseTrackingArea, trackingAreas.contains(where: { $0 === mouseTrackingArea }) {
      return
    }
    let area = NSTrackingArea(
      rect: .zero,
      options: [.mouseMoved, .mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect],
      owner: self)
    mouseTrackingArea = area
    addTrackingArea(area)
  }
  public override func mouseEntered(with event: NSEvent) {
    guard tooltipCoordinator?.suppressesHover(in: self) != true else { return }
    if pinnedIndex != nil { updateSelection() } else { mouseMoved(with: event) }
  }
  public override func mouseMoved(with event: NSEvent) {
    guard tooltipCoordinator?.suppressesHover(in: self) != true else { return }
    guard pinnedIndex == nil else {
      if tooltipCoordinator?.isShowing(from: self) == false { updateSelection() }
      return
    }
    let previous = selectedIndex
    hoverIndex = sampleIndex(at: convert(event.locationInWindow, from: nil).x)
    // Keep the floating readout anchored while scrolling without repainting the same sample.
    updateSelection()
    if selectedIndex != previous { invalidateSelection() }
  }
  public override func mouseDown(with event: NSEvent) {
    window?.makeFirstResponder(self)
    let clicked = sampleIndex(at: convert(event.locationInWindow, from: nil).x)
    pinnedIndex = pinnedIndex == nil ? clicked : nil
    hoverIndex = nil
    updateSelection()
    invalidateSelection()
  }
  public override func mouseExited(with event: NSEvent) {
    let previous = selectedIndex
    hoverIndex = nil
    if pinnedIndex == nil { updateSelection() }
    if selectedIndex != previous { invalidateSelection() }
  }
  public override func keyDown(with event: NSEvent) {
    guard !samples.isEmpty else { return }
    if event.keyCode == 123 || event.keyCode == 124 {
      let delta = event.keyCode == 124 ? 1 : -1
      pinnedIndex = min(samples.count - 1, max(0, (selectedIndex ?? 0) + delta))
      hoverIndex = nil
      updateSelection()
      invalidateSelection()
    } else if event.keyCode == 53 {
      pinnedIndex = nil
      hoverIndex = nil
      updateSelection()
      invalidateSelection()
    } else {
      super.keyDown(with: event)
    }
  }
  /// Categorical charts reserve a left column for the 0 / middle / maximum tick labels.
  var leftInset: CGFloat {
    if let mode = stackedMode {
      return mode == .funnel || (mode == .summary && axisLabel != nil) ? funnelAxisInset : 0
    }
    return presentation == .traffic
      ? PaxTheme.MiniChart.trafficLeft : PaxTheme.Chart.categoricalAxis
  }
  private func sampleIndex(at x: CGFloat) -> Int? {
    guard !samples.isEmpty else { return nil }
    if presentation == .categorical, stackedMode == nil,
      minimumColumnCount != nil || valueLine != nil
    {
      return categoricalLayout.index(at: x, count: samples.count)
    }
    if fixedBarWidth != nil, let layout = stackedLayout {
      return layout.columns.first { x < $0.slot.maxX }?.index ?? samples.count - 1
    }
    let left = leftInset
    let right =
      presentation == .traffic
      ? PaxTheme.MiniChart.trafficRight
      : (stackedMode == .labeled ? 0 : PaxTheme.Spacing.xs)
    return min(
      samples.count - 1,
      max(
        0,
        Int(
          (x - left) / max(1, bounds.width - left - right)
            * CGFloat(samples.count))))
  }
  /// Invalidate both the old and new selection, including the in-chart readout.
  func invalidateSelection() {
    let previous = selectionDamage
    selectionDamage = selectionRect()
    if !previous.isEmpty { setNeedsDisplay(previous) }
    if !selectionDamage.isEmpty { setNeedsDisplay(selectionDamage) }
  }
  private func selectionRect() -> NSRect {
    guard let index = selectedIndex, samples.indices.contains(index) else { return .zero }
    if let layout = stackedLayout {
      let column = layout.columns[index]
      if stackedMode == .labeled {
        let tip = labeledTipFrame(samples[index], column, layout)
        // Includes the pill shadow and its stem as well as the column tint.
        return column.slot.union(tip.insetBy(dx: -8, dy: -8)).intersection(bounds)
      }
      return column.slot.intersection(bounds)
    }
    if presentation == .traffic {
      let c = PaxTheme.MiniChart.self
      let step = max(1, (bounds.width - c.trafficLeft - c.trafficRight) / CGFloat(samples.count))
      let x = c.trafficLeft + (CGFloat(index) + 0.5) * step
      return NSRect(
        x: x - (PaxTheme.Trend.gridWidth + 1), y: c.trafficTop - (PaxTheme.Trend.gridWidth + 1),
        width: (PaxTheme.Trend.gridWidth + 1) * 2,
        height: c.trafficPlotHeight + (PaxTheme.Trend.gridWidth + 1) * 2)
    }
    return valueLine == nil
      ? NSRect(x: 0, y: 0, width: bounds.width, height: headerHeight) : bounds
  }
  private func updateSelection() {
    guard let selectedIndex else {
      tooltipCoordinator?.close(from: self)
      toolTip = nil
      setAccessibilityValue(nil)
      return
    }
    let sample = samples[selectedIndex]
    if let layout = stackedLayout {
      showStackedReadout(sample, layout.columns[selectedIndex])
      return
    }
    if presentation == .categorical, minimumColumnCount != nil || valueLine != nil {
      showCategoricalReadout(sample, index: selectedIndex)
      return
    }
    let values = sample.values.enumerated().map { index, value in
      let label = index < seriesLabels.count ? seriesLabels[index] : "分类"
      return "\(label) \(value.formatted(.number.precision(.fractionLength(2)))) \(unit)"
    }.joined(separator: " · ")
    let total = sample.values.reduce(0, +).formatted(.number.precision(.fractionLength(2)))
    let readout = sample.label + " · " + values + " · 合计 " + total + " " + unit
    let left = leftInset
    let right =
      presentation == .traffic
      ? PaxTheme.MiniChart.trafficRight
      : (stackedMode == .labeled ? 0 : PaxTheme.Spacing.xs)
    let x =
      left + (CGFloat(selectedIndex) + 0.5) * (bounds.width - left - right) / CGFloat(samples.count)
    let ceiling =
      ceil(
        max(
          PaxTheme.MiniChart.trafficCeiling,
          samples.map { $0.values.reduce(0, +) }.max() ?? 0) / PaxTheme.MiniChart.trafficTick)
      * PaxTheme.MiniChart.trafficTick
    let categoricalCeiling = max(
      0.1, samples.map { $0.values.reduce(0) { $0 + max(0, $1) } }.max() ?? 0)
    let categoricalHeight = max(1, bounds.height - headerHeight - PaxTheme.Chart.footer)
    let y =
      presentation == .traffic
      ? PaxTheme.MiniChart.trafficBottom - sample.values.reduce(0, +) / ceiling
        * PaxTheme.MiniChart.trafficPlotHeight
      : headerHeight + categoricalHeight
        * (1 - sample.values.reduce(0) { $0 + max(0, $1) } / categoricalCeiling)
    tooltipCoordinator?.show(readout, from: self, anchor: NSRect(x: x, y: y, width: 1, height: 1)) {
      [weak self] in
      self?.hoverIndex = nil
      self?.pinnedIndex = nil
      self?.setAccessibilityValue(nil)
      self?.invalidateSelection()
    }
    setAccessibilityValue(readout)
  }
  public override func draw(_ dirtyRect: NSRect) {
    selectionDamage = selectionRect()
    guard !samples.isEmpty, bounds.width > 0 else { return }
    if let layout = stackedLayout {
      drawStacked(layout)
      return
    }
    if presentation == .traffic {
      drawTraffic()
      return
    }
    drawCategorical()
  }
  private func drawTraffic() {
    let c = PaxTheme.MiniChart.self
    let maximum = max(c.trafficCeiling, samples.map { $0.values.reduce(0, +) }.max() ?? 0)
    let ceiling = ceil(maximum / c.trafficTick) * c.trafficTick
    let step = max(1, (bounds.width - c.trafficLeft - c.trafficRight) / CGFloat(samples.count))
    let bw = min(c.barWidth, step * c.barFraction)
    func y(_ value: Double) -> CGFloat {
      c.trafficBottom - CGFloat(value / ceiling) * c.trafficPlotHeight
    }
    let attrs: [NSAttributedString.Key: Any] = [
      .font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.captionFont),
      .foregroundColor: PaxTheme.Color.textSecondary,
    ]
    for value in [0, ceiling / 2, ceiling] {
      let path = NSBezierPath()
      path.move(to: NSPoint(x: c.trafficLeft, y: y(value)))
      path.line(to: NSPoint(x: bounds.width - c.trafficRight, y: y(value)))
      PaxTheme.Color.cardBorder.setStroke()
      path.lineWidth = PaxTheme.Trend.gridWidth
      path.stroke()
      let text = (String(Int(value)) + (value == 0 ? "" : " " + unit)) as NSString
      let size = text.size(withAttributes: attrs)
      text.draw(
        at: NSPoint(
          x: c.trafficLeft - PaxTheme.Spacing.xs - size.width, y: y(value) - size.height / 2),
        withAttributes: attrs)
    }
    for (index, sample) in samples.enumerated() {
      let x = c.trafficLeft + (CGFloat(index) + 0.5) * step - bw / 2
      let track = NSRect(x: x, y: c.trafficTop, width: bw, height: c.trafficPlotHeight)
      NSGradient(colors: PaxTheme.DashboardColor.track)?.draw(
        in: NSBezierPath(roundedRect: track, xRadius: c.barRadius, yRadius: c.barRadius), angle: 90)
      let upload = sample.values.first ?? 0
      let download = sample.values.count > 1 ? sample.values[1] : 0
      let total = max(0, upload) + max(0, download)
      NSGraphicsContext.saveGraphicsState()
      NSBezierPath(
        roundedRect: NSRect(x: x, y: y(total), width: bw, height: c.trafficBottom - y(total)),
        xRadius: c.barRadius, yRadius: c.barRadius
      ).addClip()
      NSGradient(colors: PaxTheme.DashboardColor.download)?.draw(
        in: NSRect(x: x, y: y(download), width: bw, height: c.trafficBottom - y(download)), angle: 0
      )
      NSGradient(colors: PaxTheme.DashboardColor.upload)?.draw(
        in: NSRect(x: x, y: y(total), width: bw, height: y(download) - y(total)), angle: 0)
      NSGraphicsContext.restoreGraphicsState()
      if selectedIndex == index {
        PaxTheme.DashboardColor.icon.setStroke()
        let guide = NSBezierPath()
        guide.move(to: NSPoint(x: x + bw / 2, y: c.trafficTop))
        guide.line(to: NSPoint(x: x + bw / 2, y: c.trafficBottom))
        guide.lineWidth = PaxTheme.Trend.gridWidth
        guide.stroke()
      }
    }
    for i in Set([0, samples.count / 2, samples.count - 1]).sorted() {
      let text = samples[i].label as NSString
      let size = text.size(withAttributes: attrs)
      let x = c.trafficLeft + (CGFloat(i) + 0.5) * step
      text.draw(
        at: NSPoint(
          x: min(bounds.width - size.width, max(c.trafficLeft, x - size.width / 2)),
          y: c.trafficAxisY), withAttributes: attrs)
    }
  }
  public override func viewDidMoveToWindow() {
    super.viewDidMoveToWindow()
    if window == nil { tooltipCoordinator?.close(from: self) }
  }
  public override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }
}
