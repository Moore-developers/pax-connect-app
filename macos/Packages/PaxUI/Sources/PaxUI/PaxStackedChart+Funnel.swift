import AppKit

/// 漏斗柱与柱顶标签样式的绘制与浮层；几何见 `PaxStackedChartLayout`。
extension PaxStackedChart {
  var stackedMode: PaxStackedChartLayout.Mode? {
    guard presentation == .categorical else { return nil }
    switch style {
    case .funnel: return .funnel
    case .labeledColumns: return .labeled
    case .summaryColumns: return .summary
    default: return nil
    }
  }

  var stackedLayout: PaxStackedChartLayout? {
    guard let mode = stackedMode, !samples.isEmpty else { return nil }
    return .make(
      samples: samples, size: bounds.size, mode: mode, currentIndex: currentIndex,
      leftInset: leftInset, barWidthFraction: barWidthFraction, fixedBarWidth: fixedBarWidth)
  }

  /// Only custom axes grow: drawing and hit testing share this measured inset.
  var funnelAxisInset: CGFloat {
    guard axisLabel != nil else { return PaxTheme.Chart.funnelAxis }
    let labels = funnelAxisLabels(ceiling: PaxStackedChartLayout.ceiling(for: samples))
    let width =
      labels.map {
        ($0 as NSString).size(withAttributes: [.font: PaxTheme.Typography.caption()]).width
      }.max() ?? 0
    return max(PaxTheme.Chart.funnelAxis, ceil(width) + PaxTheme.Spacing.tight)
  }

  /// 浮层分行：时间、各系列、合计；缺统计写「—」。调用方可用 `sample.readout` 覆盖。
  func stackedReadoutRows(_ sample: PaxChartSample) -> [String] {
    if let rows = sample.readout { return rows }
    guard sample.values.contains(where: { $0.isFinite }) else { return [sample.label, "缺统计 —"] }
    func text(_ value: Double) -> String {
      value.formatted(.number.precision(.fractionLength(2))) + " " + unit
    }
    let lines = sample.values.enumerated().map { index, value in
      (index < seriesLabels.count ? seriesLabels[index] : "分类") + " " + text(value)
    }
    return [sample.label] + lines + ["合计 " + text(sample.values.reduce(0) { $0 + max(0, $1) })]
  }

  func showStackedReadout(_ sample: PaxChartSample, _ column: PaxStackedChartLayout.Column) {
    let readout = stackedReadoutRows(sample).joined(separator: " · ")
    if stackedMode == .labeled {
      tooltipCoordinator?.close(from: self)
      setAccessibilityValue(readout)
      return
    }
    tooltipCoordinator?.show(
      readout, from: self,
      anchor: NSRect(x: column.bar.midX, y: column.bar.minY, width: 1, height: 1)
    ) { [weak self] in
      self?.hoverIndex = nil
      self?.pinnedIndex = nil
      self?.setAccessibilityValue(nil)
      self?.invalidateSelection()
    }
    setAccessibilityValue(readout)
  }

  static func topRounded(
    _ rect: NSRect, radius: CGFloat = PaxTheme.Chart.barTopRadius
  ) -> NSBezierPath {
    let radius = min(radius, rect.width / 2, rect.height)
    return NSBezierPath(
      roundedRect: rect, xRadius: radius, yRadius: radius)
  }

  func drawStacked(_ layout: PaxStackedChartLayout) {
    if stackedMode == .summary {
      drawSummary(layout)
      return
    }
    if stackedMode == .labeled {
      drawLabeled(layout)
      return
    }
    let attributes: [NSAttributedString.Key: Any] = [
      .font: PaxTheme.Typography.caption(), .foregroundColor: PaxTheme.Color.textSecondary,
    ]
    drawFunnelAxis(layout, attributes)
    if let tint = colors.last {
      for run in layout.band {
        let path = NSBezierPath()
        path.move(to: NSPoint(x: run[0].x, y: layout.plot.maxY))
        path.line(to: run[0])
        PaxChartPlot.appendSmooth(to: path, through: run)
        path.line(to: NSPoint(x: run[run.count - 1].x, y: layout.plot.maxY))
        path.close()
        NSGradient(
          starting: tint.withAlphaComponent(PaxTheme.MiniChart.areaOpacity),
          ending: tint.withAlphaComponent(0))?.draw(in: path, angle: 90)
      }
    }
    if let selectedIndex, selectedIndex < layout.columns.count {
      PaxTheme.Color.text.withAlphaComponent(PaxTheme.Chart.funnelColumnOpacity).setFill()
      NSBezierPath(rect: layout.columns[selectedIndex].slot).fill()
    }
    for column in layout.columns where needsToDraw(column.slot) {
      if column.hasBar {
        NSGraphicsContext.saveGraphicsState()
        // Extend the clip below the baseline so only the top corners are rounded.
        let clipRect = column.bar.union(
          NSRect(x: column.bar.minX, y: column.bar.maxY, width: column.bar.width, height: 8))
        Self.topRounded(clipRect, radius: PaxTheme.Chart.funnelTopRadius).addClip()
        for (index, rect) in column.segments.enumerated() where rect.height > 0 {
          colors[index % max(1, colors.count)].setFill()
          NSBezierPath(rect: rect).fill()
        }
        if column.hatched { PaxShareBar.stripes(in: column.bar) }
        NSGraphicsContext.restoreGraphicsState()
      }
    }
  }

  /// Compute labels from the current ceiling on every draw, including incremental sample updates.
  /// Omit a middle label that rounds to either endpoint rather than showing duplicate ticks.
  func funnelAxisLabels(ceiling: Double) -> [String] {
    let labels = [0.0, 0.5, 1.0].map { fraction in
      axisLabel?(ceiling * fraction)
        ?? (ceiling * fraction).formatted(.number.precision(.fractionLength(2)))
    }
    guard axisLabel != nil, labels[1] == labels[0] || labels[1] == labels[2] else { return labels }
    return [labels[0], "", labels[2]]
  }

  private func drawFunnelAxis(
    _ layout: PaxStackedChartLayout, _ attributes: [NSAttributedString.Key: Any]
  ) {
    let plot = layout.plot
    let labels = funnelAxisLabels(ceiling: layout.ceiling)
    for (index, fraction) in [0.0, 0.5, 1.0].enumerated() {
      PaxTheme.Color.cardBorder.setStroke()
      let y = plot.maxY - fraction * plot.height
      let path = NSBezierPath()
      path.move(to: NSPoint(x: plot.minX, y: y))
      path.line(to: NSPoint(x: plot.maxX, y: y))
      path.lineWidth = PaxTheme.Layout.cardBorder
      path.stroke()
      let tick = labels[index] as NSString
      let size = tick.size(withAttributes: attributes)
      tick.draw(
        at: NSPoint(x: plot.minX - PaxTheme.Spacing.tight - size.width, y: y - size.height / 2),
        withAttributes: attributes)
    }
    for index in Set([0, samples.count / 2, samples.count - 1]).sorted() {
      let text = samples[index].label as NSString
      let size = text.size(withAttributes: attributes)
      let x = layout.columns[index].slot.midX - size.width / 2
      text.draw(
        at: NSPoint(
          x: min(plot.maxX - size.width, max(plot.minX, x)),
          y: plot.maxY + PaxTheme.Spacing.xs), withAttributes: attributes)
    }
  }
}
