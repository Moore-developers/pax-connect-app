import AppKit

/// Shared indexed chart. Products supply facts and formatting; this component owns
/// the legend, line, area and bar geometry, and synchronized selection.
@MainActor public final class PaxAnalysisChart: NSView {
  public var singleLineLegend = false
  public var strokeWidth: CGFloat?
  public var lineDash: [CGFloat] = []
  public var smooth = false
  public var annotations: [Annotation] = [] { didSet { needsDisplay = true } }
  /// Formats the right cost axis; the first series uses the left axis.
  public var secondaryFormat: (Double) -> String = { String($0) }
  public var emptyMessage = "没有已记录的轮次数据"
  public var hoverDetails: [Hover] = []
  var hoveredIndex: Int?
  var hiddenSeries: Set<Int> = []
  public enum Kind { case lines, grouped, stacked, horizontal, lineStackedAreas }
  public var kind: Kind = .lines { didSet { needsDisplay = true } }
  public var labels: [String] = [] { didSet { needsDisplay = true } }
  public var values: [[Double?]] = [] { didSet { needsDisplay = true } }
  public var names: [String] = [] { didSet { needsDisplay = true } }
  public var colors = PaxTheme.Chart.analysisTokenSeries
  public var notes: [String] = []
  public var unit = ""
  public var showLegend = true
  public var showValues = false
  public var format: (Double) -> String = {
    $0.formatted(.number.precision(.fractionLength(0...2)))
  }
  public var selectedIndex: Int? { didSet { needsDisplay = true } }
  public var onSelect: ((Int) -> Void)?
  private var tracking: NSTrackingArea?
  public override var isFlipped: Bool { true }
  public override var acceptsFirstResponder: Bool { true }
  public override var intrinsicContentSize: NSSize {
    .init(width: NSView.noIntrinsicMetric, height: PaxTheme.Chart.analysisHeight)
  }
  public override init(frame: NSRect) {
    super.init(frame: frame)
    wantsLayer = true
    setAccessibilityElement(true)
    setAccessibilityRole(.image)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  var attributes: [NSAttributedString.Key: Any] {
    [.font: PaxTheme.Typography.caption(), .foregroundColor: PaxTheme.Table.muted]
  }
  var legendHeight: CGFloat {
    showLegend && names.count > 1 && kind != .lineStackedAreas
      ? CGFloat(Int(ceil(Double(names.count) / Double(legendColumns))))
        * PaxTheme.Chart.analysisLegendHeight : 0
  }
  var legendColumns: Int {
    singleLineLegend ? max(1, names.count) : max(1, min(names.count, Int(bounds.width / 210)))
  }
  var chartRect: NSRect {
    let p = bounds.insetBy(dx: PaxTheme.Spacing.regular, dy: PaxTheme.Spacing.regular)
    let measured =
      kind == .horizontal
      ? labels.map { ($0 as NSString).size(withAttributes: attributes).width }.max() ?? 0
      : (format(ceiling) as NSString).size(withAttributes: attributes).width
    let left = min(
      bounds.width * 0.35, max(PaxTheme.Chart.categoricalAxis, measured + PaxTheme.Spacing.regular))
    return .init(
      x: p.minX + left, y: p.minY + legendHeight + PaxTheme.Spacing.regular,
      width: max(1, p.width - left - rightAxisWidth - PaxTheme.Spacing.roomy),
      height: max(1, p.height - legendHeight - PaxTheme.Spacing.roomy * 2))
  }
  var rightAxisWidth: CGFloat {
    kind == .lineStackedAreas
      ? (secondaryFormat(linkedCostCeiling) as NSString).size(withAttributes: attributes).width
        + PaxTheme.Spacing.regular : 0
  }
  var maximum: Double {
    kind == .stacked
      ? values.map { $0.compactMap { $0 }.reduce(0, +) }.max() ?? 0
      : (kind == .lineStackedAreas
        ? seriesMaximum(0) : values.flatMap { $0 }.compactMap { $0 }.max() ?? 0)
  }
  func seriesMaximum(_ series: Int) -> Double {
    values.compactMap { $0.indices.contains(series) ? $0[series] : nil }.max() ?? 0
  }
  var tickStep: Double {
    guard maximum > 0 else { return kind == .lineStackedAreas ? 1 : 0.25 }
    let interval = kind == .lineStackedAreas ? max(1, maximum / 4) : maximum / 4
    let magnitude = pow(10, floor(log10(interval)))
    let normalized = interval / magnitude
    return ([1.0, 2, 2.5, 5, 10].first { $0 >= normalized } ?? 10) * magnitude
  }
  var tickCount: Int {
    maximum > 0 ? Int(ceil(maximum * (1 + PaxTheme.ChartPlot.rangePadding) / tickStep)) : 4
  }
  var ceiling: Double { Double(tickCount) * tickStep }
  func x(_ index: Int) -> CGFloat {
    let p = chartRect
    if kind == .lines || kind == .lineStackedAreas {
      return p.minX
        + (labels.count > 1 ? CGFloat(index) / CGFloat(labels.count - 1) * p.width : p.width / 2)
    }
    return p.minX + (CGFloat(index) + 0.5) / CGFloat(max(1, labels.count)) * p.width
  }
  public override func draw(_ dirtyRect: NSRect) {
    guard !labels.isEmpty else {
      (emptyMessage as NSString).draw(
        at: .init(x: PaxTheme.Spacing.card, y: PaxTheme.Spacing.card), withAttributes: attributes)
      return
    }
    if kind == .lineStackedAreas {
      drawLineStackedAreas()
      if let i = hoveredIndex, hoverDetails.indices.contains(i) { drawHover(i) }
      return
    }
    if showLegend && names.count > 1 { drawLegend() }
    if kind == .horizontal {
      drawHorizontal()
      return
    }
    drawAxes()
    if kind == .lines {
      drawLines()
    } else if kind == .stacked {
      drawStacked()
    } else {
      drawGrouped()
    }
    if let i = hoveredIndex ?? selectedIndex, labels.indices.contains(i) {
      let p = chartRect
      let line = NSBezierPath()
      line.move(to: .init(x: x(i), y: p.minY))
      line.line(to: .init(x: x(i), y: p.maxY))
      line.lineWidth = PaxTheme.Layout.cardBorder
      let dash: [CGFloat] = [3, 3]
      line.setLineDash(dash, count: 2, phase: 0)
      PaxTheme.Table.muted.withAlphaComponent(0.6).setStroke()
      line.stroke()
      if kind == .lines {
        for level in names.indices
        where !hiddenSeries.contains(level) && values.indices.contains(i)
          && values[i].indices.contains(level)
        {
          if let value = values[i][level] {
            PaxChartPlot.selection(
              at: .init(x: x(i), y: p.maxY - CGFloat(value / ceiling) * p.height),
              color: colors[level % colors.count])
            if showValues && names.count == 1 {
              drawPointValue(
                value, point: .init(x: x(i), y: p.maxY - CGFloat(value / ceiling) * p.height))
            }
          }
        }
      }
    }
    if let i = hoveredIndex, hoverDetails.indices.contains(i) { drawHover(i) }
  }

  func text(_ text: String, at point: NSPoint, color: NSColor? = nil) {
    var attrs = attributes
    if let color { attrs[.foregroundColor] = color }
    (text as NSString).draw(at: point, withAttributes: attrs)
  }
  func line(_ from: NSPoint, _ to: NSPoint, color: NSColor? = nil) {
    let path = NSBezierPath()
    path.move(to: from)
    path.line(to: to)
    path.lineWidth = PaxTheme.Layout.cardBorder
    (color ?? PaxTheme.Table.line.withAlphaComponent(0.55)).setStroke()
    path.stroke()
  }
  var legendGlyphWidth: CGFloat {
    kind == .lineStackedAreas
      ? PaxTheme.Chart.analysisLegendDot
      : (singleLineLegend ? PaxTheme.Chart.analysisLegendSwatch : PaxTheme.Spacing.tight)
  }
  // Drawing and hit testing share the same measured legend frames.
  var legendFrames: [NSRect] {
    if kind == .lineStackedAreas { return linkedLegendFrames }
    let columnWidth = (bounds.width - PaxTheme.Spacing.regular * 2) / CGFloat(legendColumns)
    var left = PaxTheme.Spacing.regular
    return names.enumerated().map { i, name in
      let width =
        singleLineLegend
        ? (name as NSString).size(withAttributes: attributes).width
          + legendGlyphWidth + PaxTheme.Spacing.tight
        : columnWidth
      let frame = NSRect(
        x: singleLineLegend ? left : PaxTheme.Spacing.regular + CGFloat(i % legendColumns) * width,
        y: PaxTheme.Spacing.tight
          + CGFloat(singleLineLegend ? 0 : i / legendColumns) * PaxTheme.Chart.analysisLegendHeight,
        width: width, height: PaxTheme.Chart.analysisLegendHeight)
      left += width + PaxTheme.Spacing.roomy
      return frame
    }
  }
  func drawLegend() {
    for (i, frame) in legendFrames.enumerated() {
      let hidden = hiddenSeries.contains(i)
      let color = hidden ? PaxTheme.Table.muted : colors[i % colors.count]
      let glyph = legendGlyphWidth
      if kind == .lineStackedAreas {
        let height = (names[i] as NSString).size(withAttributes: attributes).height
        color.withAlphaComponent(hidden ? 0.35 : 1).setFill()
        NSBezierPath(
          ovalIn: .init(
            x: frame.minX, y: frame.minY + (height - glyph) / 2, width: glyph, height: glyph)
        ).fill()
      } else {
        color.withAlphaComponent(hidden ? 0.35 : 1).setFill()
        NSBezierPath(
          roundedRect: .init(x: frame.minX, y: frame.minY + 3, width: glyph, height: glyph),
          xRadius: 2, yRadius: 2
        ).fill()
      }
      let value: String
      if !singleLineLegend, let index = selectedIndex, values.indices.contains(index),
        values[index].indices.contains(i), let v = values[index][i]
      {
        value = "  " + format(v)
      } else {
        value = ""
      }
      let style = NSMutableParagraphStyle()
      style.lineBreakMode = .byTruncatingTail
      var attrs = attributes
      attrs[.paragraphStyle] = style
      if singleLineLegend {
        attrs[.foregroundColor] =
          kind == .lineStackedAreas || hidden ? PaxTheme.Table.muted : color
        if hidden { attrs[.strikethroughStyle] = NSUnderlineStyle.single.rawValue }
      }
      let gap: CGFloat = singleLineLegend ? PaxTheme.Spacing.tight : 5
      (names[i] + value as NSString).draw(
        in: .init(
          x: frame.minX + glyph + gap, y: frame.minY,
          width: frame.width - glyph - gap, height: frame.height),
        withAttributes: attrs)
    }
  }
  func drawAxes() {
    let p = chartRect
    let top = ceiling
    for i in 0...tickCount {
      let value = top * Double(i) / Double(tickCount)
      let y = p.maxY - CGFloat(i) / CGFloat(tickCount) * p.height
      line(.init(x: p.minX, y: y), .init(x: p.maxX, y: y))
      let label = format(value)
      let width = (label as NSString).size(withAttributes: attributes).width
      text(
        label, at: .init(x: p.minX - width - PaxTheme.Spacing.tight, y: y - PaxTheme.Spacing.tight))
    }
    text(unit, at: .init(x: p.minX, y: p.minY - PaxTheme.Spacing.regular))
    drawXLabels(below: p)
  }
  func drawXLabels(below p: NSRect) {
    let step = max(1, Int(ceil(Double(labels.count) / max(2, Double(p.width / 80)))))
    for i in labels.indices
    where (i % step == 0 && labels.count - 1 - i >= step) || i == labels.count - 1 {
      let width = (labels[i] as NSString).size(withAttributes: attributes).width
      text(
        labels[i],
        at: .init(
          x: min(bounds.maxX - width, max(0, x(i) - width / 2)),
          y: p.maxY + PaxTheme.Spacing.regular))
    }
  }
  func drawLines() {
    let p = chartRect
    let top = ceiling
    for level in names.indices where !hiddenSeries.contains(level) {
      drawLineSeries(level, in: p, ceiling: top)
    }
  }
  func drawLineSeries(_ level: Int, in p: NSRect, ceiling top: Double) {
    var segments: [[NSPoint]] = []
    var points: [NSPoint] = []
    for i in labels.indices {
      guard values.indices.contains(i), values[i].indices.contains(level),
        let v = values[i][level]
      else {
        if !points.isEmpty {
          segments.append(points)
          points = []
        }
        continue
      }
      let point = NSPoint(
        x: kind == .lineStackedAreas
          ? linkedX(i, in: p)
          : p.minX
            + (labels.count > 1 ? CGFloat(i) / CGFloat(labels.count - 1) * p.width : p.width / 2),
        y: p.maxY - CGFloat(v / top) * p.height)
      points.append(point)
      if labels.count <= 12 && kind != .lineStackedAreas {
        PaxChartPlot.dot(
          point, radius: PaxTheme.Chart.analysisMarker, color: colors[level % colors.count])
      }
      if showValues && labels.count <= 12 && i != selectedIndex { drawPointValue(v, point: point) }
    }
    if !points.isEmpty { segments.append(points) }
    for points in segments {
      let path = NSBezierPath()
      if smooth {
        path.append(PaxChartPlot.path(through: points))
      } else {
        for (i, point) in points.enumerated() {
          if i == 0 { path.move(to: point) } else { path.line(to: point) }
        }
      }
      let color = colors[level % colors.count]
      styleLine(path, series: level)
      color.setStroke()
      path.stroke()
    }
  }
  func styleLine(_ path: NSBezierPath, series: Int) {
    path.lineWidth =
      strokeWidth
      ?? (series == 0 ? PaxTheme.ChartPlot.lineWidth : PaxTheme.Layout.cardBorder)
    if smooth { path.lineCapStyle = .round }
    if !lineDash.isEmpty { path.setLineDash(lineDash, count: lineDash.count, phase: 0) }
  }
  func drawPointValue(_ value: Double, point: NSPoint) {
    let label = format(value)
    let size = (label as NSString).size(withAttributes: attributes)
    let left = min(
      bounds.maxX - size.width - PaxTheme.Spacing.tight,
      max(chartRect.minX, point.x - size.width / 2))
    let y = point.y - size.height - PaxTheme.Spacing.tight
    text(label, at: .init(x: left, y: max(legendHeight + PaxTheme.Spacing.tight, y)))
  }
  func drawStacked() {
    let p = chartRect
    let width = min(
      PaxTheme.Spacing.roomy,
      p.width / CGFloat(max(1, labels.count)) * PaxTheme.Chart.usageBarFraction)
    for i in labels.indices where values.indices.contains(i) {
      var bottom = p.maxY
      for level in names.indices where values[i].indices.contains(level) {
        guard let value = values[i][level], value > 0 else { continue }
        let height = CGFloat(value / ceiling) * p.height
        colors[level % colors.count].withAlphaComponent(selectedIndex == i ? 1 : 0.8).setFill()
        NSBezierPath(
          rect: .init(
            x: x(i) - width / 2, y: bottom - height,
            width: width, height: height)
        ).fill()
        bottom -= height
      }
    }
  }

  func drawGrouped() {
    let p = chartRect
    let column = p.width / CGFloat(max(1, labels.count)) * PaxTheme.Chart.usageBarFraction
    for i in labels.indices where values.indices.contains(i) {
      for level in names.indices where values[i].indices.contains(level) {
        guard let value = values[i][level] else { continue }
        let width = column / CGFloat(max(1, names.count))
        let left = x(i) - column / 2 + CGFloat(level) * width
        let h = CGFloat(value / ceiling) * p.height
        let color = colors[level % colors.count]
        (selectedIndex == nil || i == selectedIndex
          ? color : PaxTheme.Table.muted.withAlphaComponent(0.25)).setFill()
        NSBezierPath(
          roundedRect: .init(x: left, y: p.maxY - h, width: max(1, width - 2), height: h),
          xRadius: PaxTheme.Chart.barTopRadius, yRadius: PaxTheme.Chart.barTopRadius
        ).fill()
      }
    }
  }
  func drawHorizontal() {
    let p = chartRect
    text(unit, at: .init(x: p.minX, y: p.minY - PaxTheme.Spacing.roomy))
    let step = p.height / CGFloat(max(1, labels.count))
    for i in 0...tickCount {
      let x = p.minX + CGFloat(i) / CGFloat(tickCount) * p.width
      line(.init(x: x, y: p.minY), .init(x: x, y: p.maxY))
      let label = format(ceiling * Double(i) / Double(tickCount))
      let w = (label as NSString).size(withAttributes: attributes).width
      text(label, at: .init(x: x - w / 2, y: p.maxY + PaxTheme.Spacing.tight))
    }
    for i in labels.indices {
      let y = p.minY + (CGFloat(i) + 0.5) * step
      let label = labels[i]
      let style = NSMutableParagraphStyle()
      style.lineBreakMode = .byTruncatingTail
      style.alignment = .right
      var attrs = attributes
      attrs[.paragraphStyle] = style
      (label as NSString).draw(
        in: .init(
          x: 0, y: y - PaxTheme.Spacing.tight,
          width: p.minX - PaxTheme.Spacing.tight, height: PaxTheme.Chart.analysisLegendHeight),
        withAttributes: attrs)
      guard values.indices.contains(i), let first = values[i].first, let value = first else {
        continue
      }
      let barWidth = CGFloat(value / ceiling) * p.width
      let h = min(PaxTheme.Spacing.roomy, step * 0.55)
      colors[0].withAlphaComponent(0.8).setFill()
      NSBezierPath(
        roundedRect: .init(x: p.minX, y: y - h / 2, width: barWidth, height: h),
        xRadius: PaxTheme.Chart.barTopRadius, yRadius: PaxTheme.Chart.barTopRadius
      ).fill()
      text(
        format(value),
        at: .init(
          x: min(
            bounds.maxX - (format(value) as NSString).size(withAttributes: attributes).width,
            p.minX + barWidth + PaxTheme.Spacing.tight), y: y - PaxTheme.Spacing.tight))
    }
  }
  public override func updateTrackingAreas() {
    super.updateTrackingAreas()
    if let tracking { removeTrackingArea(tracking) }
    tracking = NSTrackingArea(
      rect: bounds,
      options: [.mouseMoved, .mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect],
      owner: self)
    if let tracking { addTrackingArea(tracking) }
  }
  private func hit(_ event: NSEvent) -> Int? {
    guard !labels.isEmpty else { return nil }
    let point = convert(event.locationInWindow, from: nil)
    let p = chartRect
    guard p.contains(point) else { return nil }
    if kind == .lineStackedAreas, showLegend, legendFrames.contains(where: { $0.contains(point) }) {
      return nil
    }
    let f = kind == .horizontal ? (point.y - p.minY) / p.height : (point.x - p.minX) / p.width
    let usesSamples = kind == .lines || kind == .lineStackedAreas
    return min(
      labels.count - 1,
      max(
        0,
        Int(
          (f * CGFloat(usesSamples ? max(1, labels.count - 1) : labels.count)).rounded(
            usesSamples ? .toNearestOrAwayFromZero : .down))))
  }
  public override func mouseMoved(with event: NSEvent) {
    guard let i = hit(event) else {
      hoveredIndex = nil
      needsDisplay = true
      return
    }
    if !hoverDetails.isEmpty {
      hoveredIndex = i
      toolTip = nil
      needsDisplay = true
      return
    }
    let parts = names.indices.compactMap { level -> String? in
      guard values.indices.contains(i), values[i].indices.contains(level), let v = values[i][level]
      else { return nil }
      return names[level] + " " + format(v)
    }
    toolTip = ([labels[i]] + parts + (notes.indices.contains(i) ? [notes[i]] : [])).joined(
      separator: " · ")
  }
  public override func mouseExited(with event: NSEvent) {
    hoveredIndex = nil
    needsDisplay = true
  }
  public override func mouseDown(with event: NSEvent) {
    window?.makeFirstResponder(self)
    let point = convert(event.locationInWindow, from: nil)
    if showLegend && !hoverDetails.isEmpty,
      let index = legendFrames.firstIndex(where: { $0.contains(point) })
    {
      if hiddenSeries.contains(index) {
        hiddenSeries.remove(index)
      } else {
        hiddenSeries.insert(index)
      }
      hoveredIndex = nil
      needsDisplay = true
      return
    }
    if let i = hit(event) {
      if !hoverDetails.isEmpty { hoveredIndex = i }
      selectedIndex = i
      onSelect?(i)
    }
  }
  public override func keyDown(with event: NSEvent) {
    guard !labels.isEmpty, event.keyCode == 123 || event.keyCode == 124 else {
      super.keyDown(with: event)
      return
    }
    let i = min(labels.count - 1, max(0, (selectedIndex ?? 0) + (event.keyCode == 123 ? -1 : 1)))
    selectedIndex = i
    if kind == .lineStackedAreas { hoveredIndex = i }
    onSelect?(i)
  }
}
