import AppKit

extension PaxContextTrendChart {
  /// Model switches: a line down both panels under a bordered floating card. Effort switches: a
  /// short top mark with small text. A card or text that would overlap another is dropped, the
  /// mark itself stays.
  func drawMarkers(_ g: Geometry) {
    var placed: [NSRect] = []
    let markers = content.markers.filter { $0.index >= 0 && $0.index < g.count }
      .sorted { $0.index < $1.index }
    let muted = PaxTheme.Color.textSecondary
    for (offset, marker) in markers.enumerated() {
      let x = g.x(marker.index)
      switch marker.kind {
      case .label(let title, let annotation, let color, let details):
        let heading = NSMutableAttributedString(
          string: title, attributes: [.font: Self.boldFont, .foregroundColor: PaxTheme.Color.text])
        heading.append(
          NSAttributedString(
            string: "  " + annotation,
            attributes: [.font: Self.axisFont, .foregroundColor: color]))
        let summary = NSMutableAttributedString(string: "")
        for (index, detail) in details.enumerated() {
          summary.append(
            NSAttributedString(
              string: (index == 0 ? "" : " · ") + detail.text,
              attributes: [.font: Self.axisFont, .foregroundColor: muted]))
          summary.append(
            NSAttributedString(
              string: detail.value,
              attributes: [
                .font: NSFont.monospacedDigitSystemFont(
                  ofSize: Self.axisFont.pointSize, weight: .medium),
                .foregroundColor: detail.color,
              ]))
        }
        let lines: [NSAttributedString] = [summary]
        var start = g.top1 - Self.tickHeight
        if let box = card(heading, details: lines, centerX: x, avoiding: placed) {
          placed.append(box)
          start = box.maxY
          drawCard(heading, details: lines, in: box)
        }
        stroke(.init(x: x, y: start), .init(x: x, y: g.bottom2), color: muted, width: 1)
      case .tick(let text):
        stroke(
          .init(x: x, y: g.top1 - Self.tickHeight), .init(x: x, y: g.top1),
          color: PaxTheme.Color.text, width: 1.5)
        let size = width(text, Self.axisFont)
        let next =
          offset + 1 < markers.count ? g.x(markers[offset + 1].index) : g.right
        let frame = NSRect(
          x: x + 4, y: g.top1 - Self.tickHeight - 13, width: size, height: 13)
        guard next - x > size + 14, frame.maxX <= g.right,
          !placed.contains(where: { $0.insetBy(dx: -3, dy: -3).intersects(frame) })
        else { continue }
        drawText(text, x: frame.minX, centerY: frame.midY)
      }
    }
  }

  private func card(
    _ heading: NSAttributedString, details: [NSAttributedString], centerX: CGFloat,
    avoiding placed: [NSRect]
  ) -> NSRect? {
    let widest = max(heading.size().width, details.map { $0.size().width }.max() ?? 0)
    let size = NSSize(
      width: ceil(widest) + 16, height: CGFloat(details.count + 1) * Self.labelLine + 8)
    let left = min(bounds.width - size.width - 2, max(2, centerX - size.width / 2))
    let frame = NSRect(origin: .init(x: left, y: 4), size: size)
    return placed.contains(where: { $0.insetBy(dx: -4, dy: 0).intersects(frame) }) ? nil : frame
  }

  private func drawCard(
    _ heading: NSAttributedString, details: [NSAttributedString], in frame: NSRect
  ) {
    let shape = NSBezierPath(roundedRect: frame, xRadius: 6, yRadius: 6)
    PaxTheme.Color.card.setFill()
    shape.fill()
    PaxTheme.Color.cardBorder.setStroke()
    shape.lineWidth = 1
    shape.stroke()
    for (row, line) in ([heading] + details).enumerated() {
      line.draw(
        at: .init(
          x: frame.minX + 8,
          y: frame.minY + 4 + Self.labelLine * CGFloat(row) + (Self.labelLine - line.size().height)
            / 2))
    }
  }

  /// Full-height guide line, white-core dots on every layer's top line, then the hover card.
  func drawHoverOverlay(_ g: Geometry) {
    guard let index = hoveredIndex, index < g.count else { return }
    let x = g.x(index)
    stroke(
      .init(x: x, y: g.top1), .init(x: x, y: g.bottom2),
      color: PaxTheme.Color.text.withAlphaComponent(0.3))
    if let tops = stack(at: index) {
      for (layer, total) in zip(content.layers, tops) {
        // 7pt outer: 5pt white core inside a 2pt layer-colour ring.
        let dot = NSBezierPath(
          ovalIn: .init(x: x - 2.5, y: g.tokenY(total) - 2.5, width: 5, height: 5))
        NSColor.white.setFill()
        dot.fill()
        layer.color.setStroke()
        dot.lineWidth = 2
        dot.stroke()
      }
    }
    if content.hovers.indices.contains(index) {
      let plot = NSRect(
        x: g.left, y: g.top1, width: g.right - g.left, height: g.bottom2 - g.top1)
      PaxAnalysisHoverCard.draw(content.hovers[index], anchorX: x, anchorY: hoverY, in: plot)
    }
  }
}
