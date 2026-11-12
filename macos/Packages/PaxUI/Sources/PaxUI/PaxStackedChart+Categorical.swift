import AppKit

extension PaxStackedChart {
  /// A line of secondary-axis values in the same order as the bars. Nil entries are gaps.
  public struct ValueLine {
    public let values: [Double?]
    public let name: String
    public let unit: String
    public let color: NSColor
    public var connectsGaps = false
    public var showsAxis = true
    public init(values: [Double?], name: String, unit: String, color: NSColor) {
      self.values = values
      self.name = name
      self.unit = unit
      self.color = color
    }
    var ceiling: Double {
      let peak = values.compactMap { $0 }.max() ?? 0
      guard peak > 0 else { return 1 }
      let magnitude = pow(10, floor(log10(peak)))
      return ([1.0, 2, 2.5, 5, 10].first { $0 >= peak / magnitude } ?? 10) * magnitude
    }
    func text(_ value: Double) -> String {
      value.formatted(.number.precision(.fractionLength(0...3)))
    }
  }

  struct CategoricalLayout {
    let plot: NSRect
    let step: CGFloat
    let barWidth: CGFloat
    let firstX: CGFloat
    let ceiling: Double
    func center(_ index: Int) -> CGFloat { firstX + CGFloat(index) * step + barWidth / 2 }
    func index(at x: CGFloat, count: Int) -> Int? {
      let lower = firstX - (step - barWidth) / 2
      guard count > 0, x >= lower, x < lower + CGFloat(count) * step else { return nil }
      return min(count - 1, max(0, Int((x - lower) / step)))
    }
  }

  var categoricalAttributes: [NSAttributedString.Key: Any] {
    [.font: PaxTheme.Typography.caption(), .foregroundColor: PaxTheme.Color.textSecondary]
  }

  var categoricalLayout: CategoricalLayout {
    let attrs = categoricalAttributes
    let right =
      valueLine.flatMap { $0.showsAxis ? $0 : nil }.map {
        max(
          ($0.unit as NSString).size(withAttributes: attrs).width,
          ($0.text($0.ceiling) as NSString).size(withAttributes: attrs).width)
          + PaxTheme.Spacing.regular
      } ?? PaxTheme.Spacing.xs
    let plot = NSRect(
      x: leftInset, y: headerHeight, width: max(1, bounds.width - leftInset - right),
      height: max(1, bounds.height - headerHeight - PaxTheme.Chart.footer))
    let step = plot.width / CGFloat(max(1, samples.count, minimumColumnCount ?? 0))
    let widthStep =
      plot.width
      / CGFloat(max(1, samples.count, minimumColumnCount ?? 0, minimumBarColumnCount ?? 0))
    let barWidth =
      barWidthFraction.map { widthStep * $0 }
      ?? max(1, step - min(PaxTheme.Spacing.tight, step * PaxTheme.Chart.barGapFraction))
    let groupWidth = CGFloat(max(0, samples.count - 1)) * step + barWidth
    let firstX = minimumColumnCount == nil ? plot.minX : plot.midX - groupWidth / 2
    return .init(
      plot: plot, step: step, barWidth: barWidth, firstX: firstX,
      ceiling: max(0.1, samples.map { $0.values.reduce(0) { $0 + max(0, $1) } }.max() ?? 0))
  }

  func drawCategorical() {
    let layout = categoricalLayout
    let plot = layout.plot
    let attrs = categoricalAttributes
    let heading =
      style == .gradientRounded
      ? unit
      : selectedIndex.map { index in
        let sample = samples[index]
        let values = sample.values.enumerated().map { category, value in
          let name = category < seriesLabels.count ? seriesLabels[category] : "分类"
          return name + " " + value.formatted(.number.precision(.fractionLength(2)))
        }.joined(separator: " / ")
        return sample.label + " · " + unit + "\n" + values
      } ?? unit
    (heading as NSString).draw(
      in: NSRect(x: plot.minX, y: 0, width: plot.width, height: headerHeight),
      withAttributes: attrs)
    if let line = valueLine, line.showsAxis {
      (line.unit as NSString).draw(
        at: NSPoint(x: plot.maxX + PaxTheme.Spacing.tight, y: 0), withAttributes: attrs)
    }
    for fraction in [0.0, 0.5, 1.0] {
      let y = plot.maxY - fraction * plot.height
      let path = NSBezierPath()
      path.move(to: NSPoint(x: plot.minX, y: y))
      path.line(to: NSPoint(x: plot.maxX, y: y))
      path.lineWidth = PaxTheme.Layout.cardBorder
      PaxTheme.Color.cardBorder.setStroke()
      path.stroke()
      let tick =
        (layout.ceiling * fraction).formatted(.number.precision(.fractionLength(2))) as NSString
      let size = tick.size(withAttributes: attrs)
      tick.draw(
        at: NSPoint(
          x: plot.minX - PaxTheme.Spacing.tight - size.width,
          y: y - size.height / 2), withAttributes: attrs)
      if let line = valueLine, line.showsAxis {
        (line.text(line.ceiling * fraction) as NSString).draw(
          at: NSPoint(x: plot.maxX + PaxTheme.Spacing.tight, y: y - size.height / 2),
          withAttributes: attrs)
      }
    }
    for (index, sample) in samples.enumerated() {
      var y = plot.maxY
      let top = sample.values.lastIndex { $0 > 0 }
      for (category, value) in sample.values.enumerated() {
        let height = max(0, value) / layout.ceiling * plot.height
        y -= height
        let rect = NSRect(
          x: layout.firstX + CGFloat(index) * layout.step, y: y,
          width: layout.barWidth, height: height)
        let color = colors[category % max(1, colors.count)]
        if style == .gradientRounded {
          Self.drawShaded(rect, color: color, rounded: category == top, hatched: hatched)
        } else {
          color.setFill()
          NSBezierPath(rect: rect).fill()
        }
      }
    }
    drawCategoricalLine(layout)
    let indices: [Int]
    if automaticallySpacesCategoryLabels {
      let labelWidth =
        samples.map { ($0.label as NSString).size(withAttributes: attrs).width }.max() ?? 0
      let stride = max(1, Int(ceil((labelWidth + PaxTheme.Spacing.regular) / layout.step)))
      let last = samples.count - 1
      indices =
        Array(Swift.stride(from: 0, to: last, by: stride)).filter {
          CGFloat(last - $0) * layout.step >= labelWidth + PaxTheme.Spacing.regular
        } + [last]
    } else {
      indices = Set([0, samples.count / 2, samples.count - 1]).sorted()
    }
    for index in indices {
      let text = samples[index].label as NSString
      let size = text.size(withAttributes: attrs)
      let position =
        minimumColumnCount == nil
        ? plot.minX + CGFloat(index) * layout.step : layout.center(index) - size.width / 2
      let x = min(plot.maxX - size.width, max(plot.minX, position))
      text.draw(at: NSPoint(x: x, y: plot.maxY + PaxTheme.Spacing.tight), withAttributes: attrs)
    }
  }

  func drawCategoricalLine(_ layout: CategoricalLayout) {
    guard let line = valueLine else { return }
    let plot = layout.plot
    func point(_ index: Int, _ value: Double) -> NSPoint {
      NSPoint(x: layout.center(index), y: plot.maxY - value / line.ceiling * plot.height)
    }
    var run: [NSPoint] = []
    var previous: (index: Int, point: NSPoint)?
    func drawRun() {
      guard !run.isEmpty else { return }
      if run.count == 1, let first = run.first {
        PaxChartPlot.dot(first, radius: PaxTheme.ChartPlot.endpointRadius, color: line.color)
      } else {
        let path = PaxChartPlot.path(through: run)
        path.lineWidth = PaxTheme.ChartPlot.lineWidth
        line.color.setStroke()
        path.stroke()
      }
      run.removeAll(keepingCapacity: true)
    }
    for index in samples.indices {
      if line.values.indices.contains(index), let value = line.values[index] {
        let current = point(index, value)
        if line.connectsGaps, let previous, index > previous.index + 1 {
          let bridge = NSBezierPath()
          bridge.move(to: previous.point)
          bridge.line(to: current)
          bridge.lineWidth = PaxTheme.ChartPlot.lineWidth
          bridge.setLineDash(
            PaxTheme.Chart.analysisRequestDash,
            count: PaxTheme.Chart.analysisRequestDash.count, phase: 0)
          line.color.withAlphaComponent(PaxTheme.Chart.analysisAnnotationLineOpacity).setStroke()
          bridge.stroke()
        }
        run.append(current)
        previous = (index, current)
      } else {
        drawRun()
      }
    }
    drawRun()
    if let index = selectedIndex, line.values.indices.contains(index),
      let value = line.values[index]
    {
      let guide = NSBezierPath()
      guide.move(to: NSPoint(x: layout.center(index), y: plot.minY))
      guide.line(to: NSPoint(x: layout.center(index), y: plot.maxY))
      guide.lineWidth = PaxTheme.Layout.cardBorder
      line.color.withAlphaComponent(PaxTheme.Chart.analysisAnnotationLineOpacity).setStroke()
      guide.stroke()
      PaxChartPlot.selection(at: point(index, value), color: line.color)
    }
  }

  func showCategoricalReadout(_ sample: PaxChartSample, index: Int) {
    let layout = categoricalLayout
    let parts = sample.values.enumerated().map { category, value in
      let name = category < seriesLabels.count ? seriesLabels[category] : "分类"
      return "\(name) \(value.formatted(.number.precision(.fractionLength(2)))) \(unit)"
    }
    let total = sample.values.reduce(0, +)
    var rows =
      sample.readout
      ?? ([sample.label] + parts
        + ["合计 \(total.formatted(.number.precision(.fractionLength(2)))) \(unit)"])
    if let line = valueLine {
      let value = line.values.indices.contains(index) ? line.values[index] : nil
      rows.append(line.name + " " + (value.map { line.text($0) + " " + line.unit } ?? "—"))
    }
    let readout = rows.joined(separator: " · ")
    let anchor = NSRect(
      x: layout.center(index),
      y: layout.plot.maxY - total / layout.ceiling * layout.plot.height, width: 1, height: 1)
    let dismiss: () -> Void = { [weak self] in
      self?.hoverIndex = nil
      self?.pinnedIndex = nil
      self?.setAccessibilityValue(nil)
      self?.invalidateSelection()
    }
    if hoverDetails.indices.contains(index) {
      tooltipCoordinator?.show(hoverDetails[index], from: self, anchor: anchor, onDismiss: dismiss)
      setAccessibilityValue(hoverDetails[index].accessibilityText)
      return
    }
    tooltipCoordinator?.show(readout, from: self, anchor: anchor, onDismiss: dismiss)
    setAccessibilityValue(readout)
  }
}
