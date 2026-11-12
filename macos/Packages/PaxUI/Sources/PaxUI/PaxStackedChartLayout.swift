import AppKit

/// 漏斗柱与柱顶标签样式的纯几何：由样本值与尺寸算出柱、列、面积带与标签矩形（flipped 坐标，minY 在视觉顶部）。
struct PaxStackedChartLayout {
  enum Mode { case funnel, labeled, summary }

  struct Column {
    let index: Int
    /// 缺测（无值）或合计为 0 时没有柱：不画假柱。
    let hasBar: Bool
    let isCurrent: Bool
    /// 过去时段画斜纹，当前时段实色。
    var hatched: Bool { hasBar && !isCurrent }
    /// 整列高度，悬停时高亮。
    let slot: NSRect
    /// 合计柱矩形；无柱时高度为 0，贴在基线。
    let bar: NSRect
    /// 各系列矩形，按系列序，第一个系列在最上。
    let segments: [NSRect]
    /// 列顶表头（日期 + 金额）的矩形，仅 labeled；柱贴列底，不跟随柱顶。
    let label: NSRect?
  }

  let plot: NSRect
  let ceiling: Double
  let columns: [Column]
  /// 柱顶中心点按连续有柱的列分段，供面积带；缺测处断开。
  let band: [[NSPoint]]

  private static func total(_ sample: PaxChartSample) -> Double {
    sample.values.reduce(0) { $0 + (($1.isFinite && $1 > 0) ? $1 : 0) }
  }

  static func ceiling(for samples: [PaxChartSample]) -> Double {
    let peak = samples.map(total).max() ?? 0
    return peak > 0 ? peak : 1
  }

  static func make(
    samples: [PaxChartSample], size: NSSize, mode: Mode, currentIndex: Int?, leftInset: CGFloat,
    barWidthFraction: CGFloat?, fixedBarWidth: CGFloat?
  ) -> PaxStackedChartLayout {
    let c = PaxTheme.Chart.self
    let left = leftInset
    let labeled = mode == .labeled
    let summary = mode == .summary
    let top = summary ? c.summaryTop : labeled ? c.labeledHeader : c.funnelTop
    let bottom = summary ? c.summaryFooter : labeled ? 0 : c.funnelFooter
    let fixedWidth = summary ? fixedBarWidth : nil
    // 固定宽度摘要柱首尾贴绘图区边缘；其他样式沿用原留白。
    let plot = NSRect(
      x: left, y: top,
      width: max(1, size.width - left - (labeled || fixedWidth != nil ? 0 : PaxTheme.Spacing.xs)),
      height: max(1, size.height - top - bottom))
    let ceiling = ceiling(for: samples)
    let step: CGFloat
    if let fixedWidth, samples.count > 1 {
      step = (plot.width - fixedWidth) / CGFloat(samples.count - 1)
    } else {
      step = samples.isEmpty ? plot.width : plot.width / CGFloat(samples.count)
    }
    let barWidth =
      summary
      ? fixedWidth ?? step * (barWidthFraction ?? c.summaryBarFraction)
      : labeled
        ? max(1, step - c.labeledDivider - c.labeledBarInset * 2) : step * c.funnelBarFraction
    let current = currentIndex ?? (samples.count - 1)
    // labeled 最高柱只占柱区的一部分，给浮层与图例留出空间。
    let scale = plot.height * (labeled ? c.labeledPeakFraction : 1)
    var columns: [Column] = []
    for (index, sample) in samples.enumerated() {
      // labeled 的列含表头，从顶部算起；funnel 的列只含绘图区。
      let slotTop = labeled || summary ? 0 : plot.minY
      let center = plot.minX + (fixedWidth ?? step) / 2 + CGFloat(index) * step
      let slotLeft = fixedWidth != nil && index == 0 ? plot.minX : center - step / 2
      let slotRight =
        fixedWidth != nil && index == samples.count - 1 ? plot.maxX : center + step / 2
      let slot = NSRect(
        x: slotLeft, y: slotTop, width: slotRight - slotLeft,
        height: (summary ? size.height : plot.maxY) - slotTop)
      let sum = total(sample)
      let barHeight = CGFloat(sum / ceiling) * scale
      let x = labeled ? slot.minX + c.labeledDivider + c.labeledBarInset : center - barWidth / 2
      let bar = NSRect(x: x, y: plot.maxY - barHeight, width: barWidth, height: barHeight)
      var segments: [NSRect] = []
      if sum > 0 {
        // 第一个系列在最上：自下而上从最后一个系列开始堆。
        var rects = [NSRect](repeating: .zero, count: sample.values.count)
        var y = plot.maxY
        for category in sample.values.indices.reversed() {
          let value = sample.values[category]
          let h = (value.isFinite && value > 0) ? CGFloat(value / ceiling) * scale : 0
          y -= h
          rects[category] = NSRect(x: x, y: y, width: barWidth, height: h)
        }
        segments = rects
      }
      let label: NSRect? =
        labeled
        ? NSRect(
          x: slot.minX + c.labeledDivider, y: 0, width: step - c.labeledDivider,
          height: c.labeledHeader) : nil
      columns.append(
        Column(
          index: index, hasBar: sum > 0, isCurrent: index == current, slot: slot, bar: bar,
          segments: segments, label: label))
    }
    var band: [[NSPoint]] = []
    var run: [NSPoint] = []
    for column in columns {
      if column.hasBar {
        run.append(NSPoint(x: column.bar.midX, y: column.bar.minY))
      } else {
        if run.count > 1 { band.append(run) }
        run = []
      }
    }
    if run.count > 1 { band.append(run) }
    return PaxStackedChartLayout(plot: plot, ceiling: ceiling, columns: columns, band: band)
  }
}
