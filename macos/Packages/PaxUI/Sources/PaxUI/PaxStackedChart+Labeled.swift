import AppKit

/// 列式柱图（Style.labeledColumns）的绘制：列顶表头、列间分隔线、贴底的分色堆叠柱与图内悬停浮层；几何见 `PaxStackedChartLayout`。
extension PaxStackedChart {
  /// 浮层各段：去掉时间行与合计行，只留系列拆分（输入 / 输出 / 缓存）。
  func labeledTipParts(_ sample: PaxChartSample) -> [String] {
    stackedReadoutRows(sample).filter { $0 != sample.label && !$0.hasPrefix("合计") }
  }

  func drawLabeled(_ layout: PaxStackedChartLayout) {
    NSGraphicsContext.saveGraphicsState()
    cornerClip().addClip()
    if let selectedIndex, selectedIndex < layout.columns.count {
      PaxTheme.Color.accent.withAlphaComponent(PaxTheme.Chart.labeledHoverOpacity).setFill()
      NSBezierPath(rect: layout.columns[selectedIndex].slot).fill()
    }
    PaxTheme.DashboardColor.divider.setFill()
    for column in layout.columns where needsToDraw(column.slot) {
      NSRect(
        x: column.slot.minX, y: column.slot.minY, width: PaxTheme.Chart.labeledDivider,
        height: column.slot.height
      ).fill()
    }
    for column in layout.columns where needsToDraw(column.slot) {
      if column.hasBar { drawLabeledBar(column) }
      if let header = column.label { drawHeader(samples[column.index], column, header) }
    }
    if let selectedIndex, selectedIndex < layout.columns.count {
      drawTip(samples[selectedIndex], layout.columns[selectedIndex], layout)
    }
    NSGraphicsContext.restoreGraphicsState()
  }

  private func cornerClip() -> NSBezierPath {
    let radius = bottomTrailingRadius
    guard radius > 0 else { return NSBezierPath(rect: bounds) }
    // 翻转坐标：maxY 是视觉底部。
    let path = NSBezierPath()
    path.move(to: NSPoint(x: bounds.minX, y: bounds.minY))
    path.line(to: NSPoint(x: bounds.maxX, y: bounds.minY))
    path.line(to: NSPoint(x: bounds.maxX, y: bounds.maxY - radius))
    path.appendArc(
      withCenter: NSPoint(x: bounds.maxX - radius, y: bounds.maxY - radius), radius: radius,
      startAngle: 0, endAngle: 90)
    path.line(to: NSPoint(x: bounds.minX, y: bounds.maxY))
    path.close()
    return path
  }

  private func drawLabeledBar(_ column: PaxStackedChartLayout.Column) {
    NSGraphicsContext.saveGraphicsState()
    // 裁剪下沿延到基线以下，只圆顶部两角。
    Self.topRounded(
      column.bar.union(
        NSRect(x: column.bar.minX, y: column.bar.maxY, width: column.bar.width, height: 8))
    ).addClip()
    for (index, rect) in column.segments.enumerated() where rect.height > 0 {
      colors[index % max(1, colors.count)].setFill()
      NSBezierPath(rect: rect).fill()
    }
    if column.hatched { PaxShareBar.stripes(in: column.bar) }
    NSGraphicsContext.restoreGraphicsState()
  }

  private func drawHeader(
    _ sample: PaxChartSample, _ column: PaxStackedChartLayout.Column, _ header: NSRect
  ) {
    let centered = NSMutableParagraphStyle()
    centered.alignment = .center
    let weight: NSFont.Weight = column.isCurrent ? .semibold : .regular
    let date: [NSAttributedString.Key: Any] = [
      .font: PaxTheme.Typography.label(size: PaxTheme.Chart.labeledDateFont, weight: weight),
      .foregroundColor: PaxTheme.Color.textSecondary, .paragraphStyle: centered,
    ]
    let amount: [NSAttributedString.Key: Any] = [
      .font: PaxTheme.Typography.metric(size: PaxTheme.Chart.labeledAmountFont, weight: weight),
      .foregroundColor: PaxTheme.Color.text, .paragraphStyle: centered,
    ]
    (sample.label as NSString).draw(
      in: NSRect(
        x: header.minX, y: header.minY + PaxTheme.Chart.labeledDateY, width: header.width,
        height: PaxTheme.Chart.tipLine), withAttributes: date)
    ((sample.amountLabel ?? "—") as NSString).draw(
      in: NSRect(
        x: header.minX, y: header.minY + PaxTheme.Chart.labeledAmountY, width: header.width,
        height: PaxTheme.Chart.tipLine), withAttributes: amount)
  }

  func labeledTipFrame(
    _ sample: PaxChartSample, _ column: PaxStackedChartLayout.Column,
    _ layout: PaxStackedChartLayout
  ) -> NSRect {
    let parts = labeledTipParts(sample)
    guard !parts.isEmpty else { return .zero }
    let c = PaxTheme.Chart.self
    let border = PaxTheme.Layout.cardBorder
    let attributes: [NSAttributedString.Key: Any] = [
      .font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyFont)
    ]
    let widths = parts.map { ceil(($0 as NSString).size(withAttributes: attributes).width) }
    let size = NSSize(
      width: widths.reduce(0, +) + (c.tipGap * 2 + border) * CGFloat(parts.count - 1)
        + (c.tipPaddingX + border) * 2,
      height: c.tipLine + (c.tipPaddingY + border) * 2)
    let x = min(max(0, column.slot.maxX - size.width), max(0, bounds.width - size.width))
    // 浮层压住它横跨的所有列：贴在这些列中最高的柱顶之上，引线连到所选列的柱顶。
    let spanned = layout.columns.filter { $0.slot.maxX > x && $0.slot.minX < x + size.width }
    let clearance = spanned.map(\.bar.minY).min() ?? column.bar.minY
    return NSRect(
      x: x, y: max(0, clearance - c.tipStem - size.height), width: size.width,
      height: size.height)
  }

  /// 单行胶囊「输入 | 输出 | 缓存」：右缘对齐所选列的右缘，贴在柱顶上方，一根引线连到柱顶。
  private func drawTip(
    _ sample: PaxChartSample, _ column: PaxStackedChartLayout.Column,
    _ layout: PaxStackedChartLayout
  ) {
    let parts = labeledTipParts(sample)
    guard !parts.isEmpty else { return }
    let c = PaxTheme.Chart.self
    let border = PaxTheme.Layout.cardBorder
    let attributes: [NSAttributedString.Key: Any] = [
      .font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyFont),
      .foregroundColor: PaxTheme.Color.text,
    ]
    let widths = parts.map { ceil(($0 as NSString).size(withAttributes: attributes).width) }
    let frame = labeledTipFrame(sample, column, layout)

    PaxTheme.Color.cardBorder.setFill()
    NSRect(
      x: column.bar.midX - border / 2, y: frame.maxY, width: border,
      height: max(c.tipStem, column.bar.minY - frame.maxY)
    ).fill()

    let pill = NSBezierPath(
      roundedRect: frame.insetBy(dx: border / 2, dy: border / 2), xRadius: c.tipRadius,
      yRadius: c.tipRadius)
    NSGraphicsContext.saveGraphicsState()
    let shadow = NSShadow()
    shadow.shadowColor = NSColor.black.withAlphaComponent(0.13)
    shadow.shadowBlurRadius = 6
    shadow.shadowOffset = NSSize(width: 0, height: -2)
    shadow.set()
    PaxTheme.Color.control.setFill()
    pill.fill()
    NSGraphicsContext.restoreGraphicsState()
    PaxTheme.Color.cardBorder.setStroke()
    pill.lineWidth = border
    pill.stroke()

    var cursor = frame.minX + border + c.tipPaddingX
    let top = frame.minY + border + c.tipPaddingY
    for (index, part) in parts.enumerated() {
      if index > 0 {
        cursor += c.tipGap
        PaxTheme.DashboardColor.divider.setFill()
        NSRect(x: cursor, y: top, width: border, height: c.tipLine).fill()
        cursor += border + c.tipGap
      }
      (part as NSString).draw(
        in: NSRect(x: cursor, y: top + 1, width: widths[index], height: c.tipLine),
        withAttributes: attributes)
      cursor += widths[index]
    }
  }
}
