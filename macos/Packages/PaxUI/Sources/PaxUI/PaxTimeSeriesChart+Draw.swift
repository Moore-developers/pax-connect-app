import AppKit

extension PaxTimeSeriesChart {
  private var levels: Int { style.isStacked ? 2 : 1 }

  private func xPosition(_ index: Int) -> CGFloat {
    plot.minX + CGFloat(model.fraction(at: index)) * plot.width
  }

  private func yPosition(_ value: Double) -> CGFloat {
    plot.maxY - CGFloat(model.yFraction(value)) * plot.height
  }

  /// Level 0 is the base series; level 1 is base + stacked.
  private func point(_ index: Int, level: Int) -> NSPoint {
    let point = model.points[index]
    let base = point.value ?? 0
    return NSPoint(
      x: xPosition(index), y: yPosition(level == 0 ? base : base + (point.stacked ?? 0)))
  }

  public override func draw(_ dirtyRect: NSRect) {
    updateSelectionDamage()
    guard hasData, plot.width > 0, plot.height > 0 else { return }
    if density == .regular { drawGrid() }
    if density == .regular, let hit = currentHit { drawCrosshair(hit) }
    for level in 0..<levels { drawSeries(level: level) }
    if let hit = currentHit, let index = hit.index {
      for level in 0..<levels {
        PaxChartPlot.selection(at: point(index, level: level), color: style.colors[level])
      }
    }
    if density == .regular { drawTicks() }
  }

  private func drawSeries(level: Int) {
    let color = style.colors[level]
    let baseline = { (index: Int) in NSPoint(x: self.xPosition(index), y: self.plot.maxY) }
    for segment in model.segments {
      guard case .bridge(let from, let to) = segment else { continue }
      let path = NSBezierPath()
      path.move(to: point(from, level: level))
      path.line(to: point(to, level: level))
      path.lineWidth = PaxTheme.TimeSeries.bridgeLineWidth
      let dash = PaxTheme.Routes.chartGapDash
      path.setLineDash(dash, count: dash.count, phase: 0)
      color.withAlphaComponent(PaxTheme.TimeSeries.bridgeOpacity).setStroke()
      path.stroke()
    }
    let lastKnown = model.knownIndices.last
    for segment in model.segments {
      switch segment {
      case .run(let indices):
        let top = indices.map { point($0, level: level) }
        let lower = level == 0 ? indices.map(baseline) : indices.map { point($0, level: 0) }
        PaxChartPlot(
          bounds: plot,
          series: [
            .init(
              points: top, color: color, lowerBoundary: lower,
              showsEndpoint: indices.last == lastKnown)
          ], fadesAreaFill: fadesAreaFill
        ).draw()
      case .dot(let index):
        PaxChartPlot.dot(
          point(index, level: level), radius: PaxTheme.TimeSeries.isolatedDotRadius, color: color)
      case .bridge:
        continue
      }
    }
  }

  private func drawGrid() {
    let attrs: [NSAttributedString.Key: Any] = [
      .font: PaxTheme.Typography.caption(), .foregroundColor: PaxTheme.Routes.muted,
    ]
    for value in model.gridValues {
      let y = yPosition(value)
      let line = NSBezierPath()
      line.move(to: NSPoint(x: plot.minX, y: y))
      line.line(to: NSPoint(x: plot.maxX, y: y))
      line.lineWidth = PaxTheme.Layout.cardBorder
      PaxTheme.Routes.line.setStroke()
      line.stroke()
      let text = style.formatTick(value) as NSString
      let size = text.size(withAttributes: attrs)
      text.draw(
        at: NSPoint(x: plot.minX - PaxTheme.Spacing.tight - size.width, y: y - size.height / 2),
        withAttributes: attrs)
    }
    guard !style.unit.isEmpty else { return }
    let unitAttrs: [NSAttributedString.Key: Any] = [
      .font: NSFont.systemFont(ofSize: PaxTheme.TimeSeries.unitFont),
      .foregroundColor: PaxTheme.Routes.muted,
    ]
    (style.unit as NSString).draw(at: NSPoint(x: plot.minX, y: 0), withAttributes: unitAttrs)
  }

  private func drawCrosshair(_ hit: PaxTimeSeriesLayout.Hit) {
    let x = plot.minX + CGFloat(fraction(of: hit)) * plot.width
    let line = NSBezierPath()
    line.move(to: NSPoint(x: x, y: plot.minY))
    line.line(to: NSPoint(x: x, y: plot.maxY))
    PaxTheme.Routes.muted.withAlphaComponent(PaxTheme.Color.noticeFillOpacity).setStroke()
    line.stroke()
  }

  /// Endpoints are placed first; an interior label that would touch one is dropped.
  func tickFrames() -> [(text: String, rect: NSRect)] {
    updatePlot()
    let attrs: [NSAttributedString.Key: Any] = [.font: PaxTheme.Typography.caption()]
    func labelWidth(_ tick: PaxTimeSeriesLayout.Tick) -> CGFloat {
      (tick.label as NSString).size(withAttributes: attrs).width
    }
    // Edge labels are pulled inside the plot, so spacing is judged on where they are drawn.
    if model.axis == .samples { return [] }
    let ticks = PaxTimeSeriesLayout.thinned(
      PaxTimeSeriesLayout.ticks(domain: model.domain, width: plot.width),
      minimumSpacing: PaxTheme.TimeSeries.minimumTickSpacing,
      position: { tick in
        let half = labelWidth(tick) / 2
        let center = self.plot.minX + CGFloat(tick.fraction) * self.plot.width
        return min(self.plot.maxX - half, max(self.plot.minX + half, center))
      })
    var placed: [(text: String, rect: NSRect)] = []
    for tick in ticks.filter(\.isEndpoint) + ticks.filter({ !$0.isEndpoint }) {
      let size = (tick.label as NSString).size(withAttributes: attrs)
      let center = plot.minX + CGFloat(tick.fraction) * plot.width
      let left = min(plot.maxX - size.width, max(plot.minX, center - size.width / 2))
      let rect = NSRect(
        x: left, y: bounds.height - size.height, width: size.width, height: size.height)
      let gap = PaxTheme.Spacing.tight
      guard !placed.contains(where: { $0.rect.insetBy(dx: -gap, dy: 0).intersects(rect) }) else {
        continue
      }
      placed.append((tick.label, rect))
    }
    return placed
  }

  private func drawTicks() {
    let attrs: [NSAttributedString.Key: Any] = [
      .font: PaxTheme.Typography.caption(), .foregroundColor: PaxTheme.Routes.muted,
    ]
    for frame in tickFrames() {
      (frame.text as NSString).draw(at: frame.rect.origin, withAttributes: attrs)
    }
  }
}
