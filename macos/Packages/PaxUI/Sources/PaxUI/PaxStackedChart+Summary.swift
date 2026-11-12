import AppKit

/// 概览共用的摘要柱图；沿用堆叠几何、系列色、斜纹和共享读数交互。
extension PaxStackedChart {
  func drawSummary(_ layout: PaxStackedChartLayout) {
    let c = PaxTheme.Chart.self
    let plot = layout.plot
    let text: [NSAttributedString.Key: Any] = [
      .font: PaxTheme.Typography.label(size: c.labeledDateFont),
      .foregroundColor: PaxTheme.Color.textSecondary,
    ]
    if let current = layout.columns.first(where: \.isCurrent) {
      PaxTheme.Color.accent.withAlphaComponent(c.summaryCurrentOpacity).setFill()
      NSBezierPath(
        roundedRect: current.slot.insetBy(dx: PaxTheme.Spacing.xs, dy: 0),
        xRadius: PaxTheme.Radius.control, yRadius: PaxTheme.Radius.control
      ).fill()
    }
    let gridLines = axisLabel == nil ? [0.0] : [0.0, 0.5, 1.0]
    for fraction in gridLines {
      let y = plot.maxY - fraction * plot.height
      PaxTheme.Color.cardBorder.setStroke()
      let path = NSBezierPath()
      path.move(to: NSPoint(x: plot.minX, y: y))
      path.line(to: NSPoint(x: plot.maxX, y: y))
      path.lineWidth = PaxTheme.Layout.cardBorder
      path.stroke()
    }
    if axisLabel != nil {
      for (index, label) in funnelAxisLabels(ceiling: layout.ceiling).enumerated() {
        let value = label as NSString
        let size = value.size(withAttributes: text)
        value.draw(
          at: NSPoint(
            x: plot.minX - PaxTheme.Spacing.tight - size.width,
            y: plot.maxY - CGFloat(index) / 2 * plot.height - size.height / 2),
          withAttributes: text)
      }
    }
    if let selectedIndex, selectedIndex < layout.columns.count {
      PaxTheme.Color.accent.withAlphaComponent(c.labeledHoverOpacity).setFill()
      NSBezierPath(rect: layout.columns[selectedIndex].slot).fill()
    }
    let dense = axisLabel != nil && (layout.columns.first?.slot.width ?? 0) < c.summaryLabelWidth
    let peak = layout.columns.max { $0.bar.height < $1.bar.height }?.index
    let tickIndices = Set([0, samples.count / 2, samples.count - 1])
    for column in layout.columns where needsToDraw(column.slot) {
      if column.hasBar {
        NSGraphicsContext.saveGraphicsState()
        let clip = column.bar.union(
          NSRect(x: column.bar.minX, y: column.bar.maxY, width: column.bar.width, height: 8))
        Self.topRounded(clip, radius: c.funnelTopRadius).addClip()
        for (index, rect) in column.segments.enumerated() where rect.height > 0 {
          colors[index % max(1, colors.count)].setFill()
          rect.fill()
        }
        if column.hatched { PaxShareBar.stripes(in: column.bar) }
        NSGraphicsContext.restoreGraphicsState()
      }
      if showsSummaryAmounts && (!dense || column.index == peak || column.isCurrent) {
        let width = dense ? c.summaryLabelWidth : column.slot.width
        // 密集小时柱只标峰值和当前；两者太近时优先保留当前读数。
        let current = layout.columns.first(where: \.isCurrent)
        let overlaps =
          dense && !column.isCurrent
          && abs((current?.bar.midX ?? 0) - column.bar.midX) < width
        if !overlaps {
          summaryText(
            samples[column.index].amountLabel ?? "—", x: column.bar.midX,
            y: max(0, column.bar.minY - c.tipLine - PaxTheme.Spacing.xs), width: width,
            attributes: [
              .font: PaxTheme.Typography.metric(size: c.labeledDateFont, weight: .medium),
              .foregroundColor: PaxTheme.Color.text,
            ])
        }
      }
      if !dense || tickIndices.contains(column.index) {
        summaryText(
          samples[column.index].label, x: column.bar.midX,
          y: plot.maxY + PaxTheme.Spacing.xs,
          width: dense ? c.summaryLabelWidth : column.slot.width, attributes: text)
      }

    }
  }

  private func summaryText(
    _ text: String, x: CGFloat, y: CGFloat, width: CGFloat,
    attributes: [NSAttributedString.Key: Any]
  ) {
    let paragraph = NSMutableParagraphStyle()
    paragraph.alignment = .center
    paragraph.lineBreakMode = .byClipping
    var attributes = attributes
    attributes[.paragraphStyle] = paragraph
    let width = min(width, bounds.width)
    (text as NSString).draw(
      in: NSRect(
        x: min(max(0, x - width / 2), bounds.width - width), y: y,
        width: width, height: PaxTheme.Chart.tipLine), withAttributes: attributes)
  }
}
