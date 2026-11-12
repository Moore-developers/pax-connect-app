import AppKit

extension PaxAnalysisChart {
  /// Callers own the event rule and label; the chart positions the annotations.
  public struct Annotation {
    public let index: Int
    public let series: Int?
    public let value: Double
    public let label: String
    public let detail: String
    public let color: NSColor
    public init(
      index: Int, series: Int?, value: Double, label: String, detail: String, color: NSColor
    ) {
      self.index = index
      self.series = series
      self.value = value
      self.label = label
      self.detail = detail
      self.color = color
    }
  }

  /// Full per-sample totals keep the cost scale fixed when a legend item is hidden.
  var linkedCostCeiling: Double {
    let peak =
      values.compactMap { row -> Double? in
        let costs = row.dropFirst()
        guard !costs.isEmpty, costs.allSatisfy({ $0 != nil }) else { return nil }
        return costs.compactMap { $0 }.reduce(0, +)
      }.max() ?? 0
    return PaxTimeSeriesLayout.niceCeiling(
      peak * (1 + PaxTheme.ChartPlot.rangePadding), minimum: peak)
  }

  func linkedX(_ index: Int, in rect: NSRect) -> CGFloat {
    rect.minX
      + (labels.count > 1
        ? CGFloat(index) / CGFloat(labels.count - 1) * rect.width : rect.width / 2)
  }

  var linkedLegendFrames: [NSRect] {
    var left = chartRect.minX
    let available = chartRect.width / CGFloat(max(1, names.count))
    return names.map { name in
      let width = min(
        available,
        (name as NSString).size(withAttributes: attributes).width
          + legendGlyphWidth + PaxTheme.Spacing.tight + PaxTheme.Spacing.roomy)
      defer { left += width }
      return .init(
        x: left, y: chartRect.minY - PaxTheme.Spacing.roomy,
        width: width, height: PaxTheme.Chart.analysisLegendHeight)
    }
  }

  func drawLineStackedAreas() {
    let plot = chartRect
    let selected = (hoveredIndex ?? selectedIndex).flatMap {
      labels.indices.contains($0) ? $0 : nil
    }
    if showLegend { drawLegend() }
    drawOverlayAxes(in: plot)
    drawOverlayAreas(in: plot)
    if let selected {
      let x = linkedX(selected, in: plot)
      line(
        .init(x: x, y: plot.minY), .init(x: x, y: plot.maxY),
        color: PaxTheme.Table.muted.withAlphaComponent(0.6))
    }
    if !hiddenSeries.contains(0) {
      drawLineSeries(0, in: plot, ceiling: ceiling)
      if let selected, values.indices.contains(selected), let first = values[selected].first,
        let value = first
      {
        PaxChartPlot.selection(
          at: .init(
            x: linkedX(selected, in: plot), y: plot.maxY - CGFloat(value / ceiling) * plot.height),
          color: colors[0])
      }
    }
    drawLinkedAnnotations(in: plot)
    drawXLabels(below: plot)
  }

  private func drawOverlayAxes(in rect: NSRect) {
    for step in 0...tickCount {
      let fraction = Double(step) / Double(tickCount)
      let y = rect.maxY - CGFloat(fraction) * rect.height
      line(
        .init(x: rect.minX, y: y), .init(x: rect.maxX, y: y), color: PaxTheme.Chart.analysisGrid)
      let label = format(ceiling * fraction)
      let width = (label as NSString).size(withAttributes: attributes).width
      text(
        label,
        at: .init(x: rect.minX - width - PaxTheme.Spacing.tight, y: y - PaxTheme.Spacing.tight))
    }
    for step in 0...4 {
      let fraction = Double(step) / 4
      text(
        secondaryFormat(linkedCostCeiling * fraction),
        at: .init(
          x: rect.maxX + PaxTheme.Spacing.tight,
          y: rect.maxY - CGFloat(fraction) * rect.height - PaxTheme.Spacing.tight))
    }
  }

  private func drawOverlayAreas(in rect: NSRect) {
    // Unknown prices break the entire stack, without connecting across the missing sample.
    var runs: [[Int]] = []
    var run: [Int] = []
    for index in labels.indices {
      if values.indices.contains(index), values[index].dropFirst().allSatisfy({ $0 != nil }) {
        run.append(index)
      } else if !run.isEmpty {
        runs.append(run)
        run = []
      }
    }
    if !run.isEmpty { runs.append(run) }
    let top = linkedCostCeiling
    for run in runs where run.count > 1 {
      var lower = run.map { NSPoint(x: linkedX($0, in: rect), y: rect.maxY) }
      for level in names.indices.dropFirst() where !hiddenSeries.contains(level) {
        let upper = run.enumerated().map { offset, index in
          NSPoint(
            x: lower[offset].x,
            y: lower[offset].y - CGFloat((values[index][level] ?? 0) / top) * rect.height)
        }
        let boundary = PaxChartPlot.path(through: upper, above: lower)
        let area = NSBezierPath()
        area.append(boundary)
        if let last = lower.last { area.line(to: last) }
        PaxChartPlot.appendSmooth(to: area, through: lower, reversed: true)
        area.close()
        let color = colors[level % colors.count]
        NSGraphicsContext.saveGraphicsState()
        area.addClip()
        NSGradient(
          starting: color.withAlphaComponent(PaxTheme.Chart.analysisStackedAreaOpacity),
          ending: color.withAlphaComponent(0)
        )?.draw(in: area.bounds, angle: 90)
        NSGraphicsContext.restoreGraphicsState()
        color.setStroke()
        boundary.lineWidth = PaxTheme.ChartPlot.lineWidth
        boundary.stroke()
        lower = upper
      }
    }
  }

  private func drawLinkedAnnotations(in rect: NSRect) {
    let theme = PaxTheme.Chart.self
    let attrs: [NSAttributedString.Key: Any] = [
      .font: PaxTheme.Typography.time, .foregroundColor: PaxTheme.Color.text,
    ]
    let selected = hoveredIndex ?? selectedIndex
    let ordered = annotations.sorted {
      if ($0.index == selected) != ($1.index == selected) { return $0.index == selected }
      return $0.index < $1.index
    }
    var occupied: [NSRect] = []
    let top = linkedCostCeiling
    for annotation in ordered
    where labels.indices.contains(annotation.index)
      && !(annotation.series.map { hiddenSeries.contains($0) } ?? false)
    {
      let color = annotation.color
      let x = linkedX(annotation.index, in: rect)
      let marker = NSBezierPath()
      marker.move(to: .init(x: x, y: rect.minY))
      marker.line(to: .init(x: x, y: rect.maxY))
      marker.lineWidth = theme.analysisAnnotationStroke
      let dash = theme.analysisRequestDash
      marker.setLineDash(dash, count: dash.count, phase: 0)
      color.withAlphaComponent(theme.analysisAnnotationLineOpacity).setStroke()
      marker.stroke()

      let size = (annotation.label as NSString).size(withAttributes: attrs)
      let detailAttrs: [NSAttributedString.Key: Any] = [
        .font: PaxTheme.Typography.caption(), .foregroundColor: PaxTheme.Table.muted,
      ]
      let detailSize = (annotation.detail as NSString).size(withAttributes: detailAttrs)
      let width = max(size.width, detailSize.width) + theme.tipPaddingX * 2
      let height = size.height + detailSize.height + theme.tipPaddingY * 2
      let left = min(rect.maxX - width, max(rect.minX, x - width / 2))
      let y = rect.maxY - CGFloat(annotation.value / top) * rect.height
      let desired = max(rect.minY, min(rect.maxY - height, y - height - theme.tipStem))
      let step = height + PaxTheme.Spacing.xs
      var frame: NSRect?
      // Keep every event line. Dense labels move vertically; the focused event gets first choice.
      for distance in 0...Int(rect.height / step) {
        for direction in distance == 0 ? [0] : [-1, 1] {
          let candidate = NSRect(
            x: left, y: desired + CGFloat(distance * direction) * step,
            width: width, height: height)
          guard candidate.minY >= rect.minY, candidate.maxY <= rect.maxY,
            !occupied.contains(where: {
              $0.insetBy(dx: -PaxTheme.Spacing.xs, dy: -PaxTheme.Spacing.xs).intersects(candidate)
            })
          else { continue }
          frame = candidate
          break
        }
        if frame != nil { break }
      }
      guard let frame else { continue }
      occupied.append(frame)
      let pill = NSBezierPath(
        roundedRect: frame, xRadius: theme.tipRadius, yRadius: theme.tipRadius)
      NSGraphicsContext.saveGraphicsState()
      let shadow = NSShadow()
      shadow.shadowColor = PaxTheme.Color.shadow.withAlphaComponent(
        theme.analysisAnnotationShadowOpacity)
      shadow.shadowBlurRadius = theme.analysisAnnotationShadowBlur
      shadow.shadowOffset = .init(width: 0, height: -PaxTheme.Layout.cardBorder)
      shadow.set()
      let fill = color.blended(
        withFraction: 1 - theme.analysisAnnotationFillOpacity, of: PaxTheme.Color.card)
      (fill ?? PaxTheme.Color.card).withAlphaComponent(theme.analysisAnnotationBackgroundOpacity)
        .setFill()
      pill.fill()
      NSGraphicsContext.restoreGraphicsState()
      PaxTheme.Color.cardBorder.setStroke()
      pill.lineWidth = PaxTheme.Layout.cardBorder
      pill.stroke()
      (annotation.label as NSString).draw(
        at: .init(x: frame.minX + theme.tipPaddingX, y: frame.minY + theme.tipPaddingY),
        withAttributes: attrs)
      (annotation.detail as NSString).draw(
        at: .init(
          x: frame.minX + theme.tipPaddingX, y: frame.minY + theme.tipPaddingY + size.height),
        withAttributes: detailAttrs)
    }
  }
}
