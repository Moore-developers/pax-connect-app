import AppKit

extension PaxContextTrendChart {
  func drawDanger(_ g: Geometry) {
    guard let danger = content.danger, danger.start < g.tokens.max else { return }
    let y = g.tokenY(danger.start)
    danger.color.withAlphaComponent(0.07).setFill()
    NSRect(x: g.left, y: g.top1, width: g.right - g.left, height: y - g.top1).fill()
  }

  func drawGrid(_ g: Geometry) {
    let grid = PaxTheme.Chart.analysisGrid
    for value in g.tokens.ticks {
      let y = g.tokenY(value)
      stroke(.init(x: g.left, y: y), .init(x: g.right, y: y), color: grid)
      drawText(
        tokenFormat(value), x: g.left - 6, centerY: y, align: .right)
    }
    for value in g.costs.ticks.dropFirst() {
      let y = g.costY(value)
      stroke(.init(x: g.left, y: y), .init(x: g.right, y: y), color: grid)
      drawText(costFormat(value), x: g.left - 6, centerY: y, align: .right)
    }
    drawText(tokenTitle, x: g.left + 4, centerY: g.top1 + 8)
    drawText(costTitle, x: g.left + 4, centerY: g.top2 + 8)
  }

  /// Each layer is a 14% fill with a 2pt top line on its cumulative height; every unbroken run
  /// of requests is one smooth curve and a lone request is a dot.
  func drawLayers(_ g: Geometry) {
    let height = g.bottom1 - g.top1
    var strokes: [(NSBezierPath, NSColor)] = []
    for run in runs() {
      var lower = run.map { NSPoint(x: g.x($0), y: g.bottom1) }
      for layer in content.layers {
        let upper = run.enumerated().map { offset, index in
          NSPoint(
            x: lower[offset].x,
            y: lower[offset].y - CGFloat((layer.values[index] ?? 0) / g.tokens.max) * height)
        }
        defer { lower = upper }
        guard run.count > 1 else {
          layer.color.setFill()
          let at = upper[0]
          NSBezierPath(ovalIn: .init(x: at.x - 2.5, y: at.y - 2.5, width: 5, height: 5)).fill()
          continue
        }
        let boundary = PaxChartPlot.path(through: upper, above: lower)
        let area = NSBezierPath()
        area.append(boundary)
        if let last = lower.last { area.line(to: last) }
        PaxChartPlot.appendSmooth(to: area, through: lower, reversed: true)
        area.close()
        layer.color.withAlphaComponent(0.14).setFill()
        area.fill()
        boundary.lineWidth = PaxTheme.Chart.analysisRequestStroke
        strokes.append((boundary, layer.color))

      }
    }
    for (path, color) in strokes {
      color.setStroke()
      path.stroke()
    }
  }

  func drawLines(_ g: Geometry) {
    for line in content.lines {
      let y = g.tokenY(line.value)
      stroke(
        .init(x: g.left, y: y), .init(x: g.right, y: y), color: line.color, width: 1.4,
        dash: line.dash)
    }
  }

  func drawFirstOver(_ g: Geometry) {
    guard let over = content.firstOver, over.index >= 0, over.index < g.count else { return }
    let x = g.x(over.index)
    stroke(
      .init(x: x, y: g.top1), .init(x: x, y: g.bottom2), color: over.color, width: 0.8)
    let size = width(over.text, Self.axisFont)
    let fitsLeft = x - 5 - size > g.left
    drawText(
      over.text, x: fitsLeft ? x - 5 : x + 5, centerY: g.top1 + 18, color: over.color,
      align: fitsLeft ? .right : .left, halo: true)
  }

  /// One 2pt stick with a dot per request, coloured by band. Sticks above the axis top are cut
  /// off and labelled "↑$1.07" on a card-coloured plate drawn after every stick, so no line
  /// crosses a label; labels never overlap each other.
  func drawCosts(_ g: Geometry) {
    var capped: [(text: String, x: CGFloat, color: NSColor)] = []
    for (index, cost) in content.costs.enumerated() where index < g.count {
      let x = g.x(index)
      guard let value = cost.value else {
        let ring = NSBezierPath(
          ovalIn: .init(x: x - 2.5, y: g.bottom2 - 6.5, width: 5, height: 5))
        PaxTheme.Color.textSecondary.setStroke()
        ring.lineWidth = 1
        ring.stroke()
        continue
      }
      let y = max(g.top2, g.costY(value))
      stroke(.init(x: x, y: y), .init(x: x, y: g.bottom2), color: cost.color, width: 2)
      let dot = NSBezierPath(ovalIn: .init(x: x - 2.8, y: y - 2.8, width: 5.6, height: 5.6))
      cost.color.setFill()
      dot.fill()
      PaxTheme.Color.card.setStroke()
      dot.lineWidth = 1
      dot.stroke()
      if value > g.costs.max * 1.0001 {
        capped.append(("↑" + Self.cappedCost(value), x, cost.color))
      }
    }
    var used: [NSRect] = []
    for item in capped {
      let size = width(item.text, Self.boldFont)
      let onRight = item.x + 5 + size <= g.right
      let frame = NSRect(
        x: onRight ? item.x + 5 : item.x - 5 - size, y: g.top2 + 3, width: size, height: 13)
      guard !used.contains(where: { $0.insetBy(dx: -4, dy: 0).intersects(frame) }) else {
        continue
      }
      used.append(frame)
      PaxTheme.Color.card.setFill()
      NSBezierPath(roundedRect: frame.insetBy(dx: -3, dy: -1), xRadius: 4, yRadius: 4).fill()
      drawText(
        item.text, x: onRight ? frame.minX : frame.maxX, centerY: frame.midY,
        font: Self.boldFont, color: item.color, align: onRight ? .left : .right)
    }
  }

  /// Two significant digits below $0.10, otherwise two decimals ("$0.11", "$1.07"); axis ticks
  /// keep `costFormat`.
  static func cappedCost(_ value: Double) -> String {
    value >= 0.1 ? String(format: "$%.2f", value) : "$" + String(format: "%.2g", value)
  }

  func drawXAxis(_ g: Geometry) {
    stroke(
      .init(x: g.left, y: g.bottom2), .init(x: g.right, y: g.bottom2),
      color: PaxTheme.Chart.analysisGrid)
    let step = [10, 20, 50, 100, 200, 500, 1000, 5000].first { CGFloat($0) * g.step >= 44 } ?? 5000
    for number in stride(from: step, through: g.count, by: step) {
      drawText(
        "#\(number)", x: g.x(number - 1), centerY: g.bottom2 + 13, align: .center)
    }
  }

  func drawPills(_ g: Geometry) {
    for pill in g.pills {
      let frame = NSRect(
        x: g.right + 6, y: pill.y - Self.pillHeight / 2, width: pill.width,
        height: Self.pillHeight)
      let shape = NSBezierPath(
        roundedRect: frame, xRadius: Self.pillHeight / 2, yRadius: Self.pillHeight / 2)
      PaxTheme.Color.card.setFill()
      shape.fill()
      pill.color.setStroke()
      shape.lineWidth = 1
      shape.stroke()
      drawText(
        pill.text, x: frame.minX + 7, centerY: frame.midY, font: Self.boldFont,
        color: pill.textColor)
    }
  }
}
