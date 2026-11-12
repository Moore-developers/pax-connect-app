import AppKit

/// Draws the H1 hover card for every analysis chart. Call from a flipped view's `draw`.
@MainActor public enum PaxAnalysisHoverCard {
  private typealias Hover = PaxAnalysisChart.Hover
  private typealias Chart = PaxTheme.Chart

  /// Draws the card inside `bounds`: on the left when `anchorX` is in the right half, else right.
  /// With `anchorY` the card is centred on that height (clamped inside); nil sticks to the bottom.
  public static func draw(
    _ hover: PaxAnalysisChart.Hover, anchorX: CGFloat, anchorY: CGFloat? = nil, in bounds: NSRect
  ) {
    let pad = Chart.analysisHoverPadding
    let width = min(Chart.analysisHoverWidth, max(0, bounds.width - 8))
    let height = layout(hover, width: width, draw: false, at: .zero)
    let left = anchorX > bounds.midX ? bounds.minX + 4 : bounds.maxX - width - 4
    let top =
      anchorY.map { min(max(bounds.minY + 4, $0 - height / 2), bounds.maxY - height - 4) }
      ?? max(bounds.minY, bounds.maxY - height - 4)
    let rect = NSRect(x: left, y: top, width: width, height: height)
    NSGraphicsContext.saveGraphicsState()
    let shadow = NSShadow()
    shadow.shadowColor = NSColor.black.withAlphaComponent(Chart.analysisHoverShadowOpacity)
    shadow.shadowBlurRadius = Chart.analysisHoverShadowBlur
    shadow.shadowOffset = NSSize(width: 0, height: -4)
    shadow.set()
    let shape = NSBezierPath(
      roundedRect: rect, xRadius: Chart.analysisHoverRadius, yRadius: Chart.analysisHoverRadius)
    PaxTheme.Color.card.setFill()
    shape.fill()
    NSGraphicsContext.restoreGraphicsState()
    PaxTheme.Table.line.setStroke()
    shape.lineWidth = 1
    shape.stroke()
    _ = layout(hover, width: width, draw: true, at: .init(x: rect.minX + pad, y: rect.minY + pad))
  }

  /// Height of the card for `hover` at the standard width.
  public static func height(of hover: PaxAnalysisChart.Hover) -> CGFloat {
    layout(hover, width: Chart.analysisHoverWidth, draw: false, at: .zero)
  }

  // MARK: Layout

  /// One pass for both measuring and drawing; returns the full card height.
  private static func layout(_ hover: Hover, width: CGFloat, draw: Bool, at origin: NSPoint)
    -> CGFloat
  {
    let pad = Chart.analysisHoverPadding
    let gap = Chart.analysisHoverGap
    let row = Chart.analysisHoverRow
    let small = Chart.analysisHoverSmall
    let inner = width - pad * 2
    let muted = PaxTheme.Color.textSecondary
    let smallFont = PaxTheme.Typography.label(size: small)
    let monoSmall = PaxTheme.Typography.metric(size: small)
    var y = origin.y
    var first = true
    func block(_ height: CGFloat, spacing: CGFloat? = nil) -> CGFloat {
      if !first { y += spacing ?? gap }
      first = false
      defer { y += height }
      return y
    }
    func text(
      _ value: String, _ x: CGFloat, _ top: CGFloat, _ w: CGFloat, _ h: CGFloat,
      font: NSFont, color: NSColor, align: NSTextAlignment = .left
    ) {
      guard draw, w > 0 else { return }
      let style = NSMutableParagraphStyle()
      style.alignment = align
      style.lineBreakMode = .byTruncatingTail
      let size = (value as NSString).size(withAttributes: [.font: font])
      (value as NSString).draw(
        in: .init(x: x, y: top + (h - size.height) / 2, width: w, height: size.height),
        withAttributes: [.font: font, .foregroundColor: color, .paragraphStyle: style])
    }
    func measure(_ value: String, _ font: NSFont) -> CGFloat {
      (value as NSString).size(withAttributes: [.font: font]).width
    }

    // Header: title left, time right.
    let header = block(18)
    let subtitleWidth = min(inner / 2, measure(hover.subtitle, smallFont) + 1)
    text(
      hover.title, origin.x, header, inner - subtitleWidth - gap, 18,
      font: PaxTheme.Typography.label(size: Chart.analysisHoverTitle, weight: .semibold),
      color: PaxTheme.Color.text)
    text(
      hover.subtitle, origin.x + inner - subtitleWidth, header, subtitleWidth, 18,
      font: smallFont, color: muted, align: .right)

    // Model / effort as text with a colour dot; the tag moves here when there is no gauge.
    let inlineTag = hover.summaryTagInline && hover.gauge == nil && !hover.summary.isEmpty
    let tagHere = hover.gauge == nil && !inlineTag ? hover.tag : nil
    if !hover.badges.isEmpty || tagHere != nil {
      let top = block(16, spacing: 3)
      let tagWidth = tagHere.map { drawTag($0, right: origin.x + inner, top: top, draw: draw) } ?? 0
      let available = inner - tagWidth - (tagHere == nil ? 0 : gap)
      var x = origin.x
      for badge in hover.badges {
        if draw {
          badge.color.setFill()
          NSBezierPath(ovalIn: .init(x: x, y: top + 5, width: 6, height: 6)).fill()
        }
        let w = min(available - (x - origin.x) - 10, measure(badge.title, smallFont) + 1)
        guard w > 0 else { break }
        text(badge.title, x + 10, top, w, 16, font: smallFont, color: muted)
        x += 10 + w + gap
      }
    }

    if let gauge = hover.gauge {
      let top = block(16 + 6 + 4)
      let tagWidth = hover.tag.map { drawTag($0, right: origin.x + inner, top: top, draw: draw) }
      text(
        gauge.label, origin.x, top, inner - (tagWidth ?? 0) - gap, 16,
        font: PaxTheme.Typography.label(size: 12), color: PaxTheme.Color.text)
      if draw { drawGauge(gauge, in: .init(x: origin.x, y: top + 22, width: inner, height: 4)) }
    }

    for (index, item) in hover.summary.enumerated() {
      let labelHeight: CGFloat = item.label.isEmpty ? 0 : 14
      let top = block(labelHeight + 28, spacing: 6)
      if !item.label.isEmpty {
        text(item.label, origin.x, top, inner, labelHeight, font: smallFont, color: muted)
      }
      let font = PaxTheme.Typography.metric(size: Chart.analysisHoverMetric, weight: .light)
      let suffixWidth = measure(hover.summarySuffix, smallFont)
      let suffixGap = hover.summarySuffix.isEmpty ? 0 : gap
      let metricTop = top + labelHeight
      let tagWidth =
        inlineTag && index == 0
        ? hover.tag.map {
          drawTag($0, right: origin.x + inner, top: metricTop + (28 - 16) / 2, draw: draw)
        } ?? 0 : 0
      let metricWidth = inner - tagWidth - (tagWidth > 0 ? gap : 0)
      let valueWidth = min(measure(item.value, font) + 1, metricWidth - suffixWidth - suffixGap)
      text(
        item.value, origin.x, top + labelHeight, valueWidth, 28,
        font: font, color: hover.summaryColor ?? PaxTheme.Color.text)
      if !hover.summarySuffix.isEmpty {
        let valueHeight = (item.value as NSString).size(withAttributes: [.font: font]).height
        let suffixHeight = (hover.summarySuffix as NSString).size(withAttributes: [.font: smallFont]
        ).height
        let suffixTop = metricTop + (28 + valueHeight) / 2 - suffixHeight
        text(
          hover.summarySuffix, origin.x + valueWidth + suffixGap, suffixTop,
          suffixWidth, suffixHeight, font: smallFont, color: muted)
      }
    }

    if !hover.breakdown.isEmpty {
      let weights = hover.breakdown.map(\.weight)
      if weights.allSatisfy({ $0 > 0 }) {
        let top = block(6)
        if draw {
          drawStack(hover.breakdown, in: .init(x: origin.x, y: top, width: inner, height: 6))
        }
      }
      let shareWidth: CGFloat = 36
      for (i, item) in hover.breakdown.enumerated() {
        let top = block(row, spacing: i == 0 ? 6 : 0)
        if draw {
          item.color.setFill()
          NSBezierPath(
            roundedRect: .init(x: origin.x, y: top + (row - 8) / 2, width: 8, height: 8),
            xRadius: 2, yRadius: 2
          ).fill()
        }
        let amountWidth = min(inner * 0.45, measure(item.amount, monoSmall) + 1)
        let shareX = origin.x + inner - shareWidth
        text(
          item.name, origin.x + 14, top, shareX - amountWidth - gap - origin.x - 14, row,
          font: smallFont, color: PaxTheme.Color.text)
        text(
          item.amount, shareX - amountWidth - 6, top, amountWidth, row, font: monoSmall,
          color: PaxTheme.Color.text, align: .right)
        text(item.share, shareX, top, shareWidth, row, font: monoSmall, color: muted, align: .right)
      }
    }

    if !hover.proportionLabel.isEmpty {
      let top = block(row + 4 + 4, spacing: 6)
      text(hover.proportionLabel, origin.x, top, inner / 2, row, font: smallFont, color: muted)
      text(
        hover.proportionValue, origin.x + inner / 2, top, inner / 2, row, font: monoSmall,
        color: PaxTheme.Color.text, align: .right)
      if draw {
        drawBar(
          hover.proportion ?? 0, color: Chart.analysisRequestSeries[0],
          in: .init(x: origin.x, y: top + row + 4, width: inner, height: 4))
      }
    }

    for (i, fact) in hover.details.enumerated() {
      if fact.value.isEmpty {
        // A note without a value spans the full width and wraps to at most two lines.
        let attrs: [NSAttributedString.Key: Any] = [
          .font: smallFont, .foregroundColor: PaxTheme.Color.text,
        ]
        let box = (fact.label as NSString).boundingRect(
          with: .init(width: inner, height: 1000), options: [.usesLineFragmentOrigin],
          attributes: attrs)
        let h = min(ceil(box.height), ceil(smallFont.boundingRectForFont.height) * 2)
        let top = block(h, spacing: i == 0 ? 6 : 2)
        if draw {
          (fact.label as NSString).draw(
            with: .init(x: origin.x, y: top, width: inner, height: h),
            options: [.usesLineFragmentOrigin, .truncatesLastVisibleLine], attributes: attrs)
        }
        continue
      }
      let top = block(row, spacing: i == 0 ? 6 : 0)
      text(fact.label, origin.x, top, inner * 0.55, row, font: smallFont, color: muted)
      text(
        fact.value, origin.x + inner * 0.45, top, inner * 0.55, row, font: monoSmall,
        color: PaxTheme.Color.text, align: .right)
    }

    if !hover.context.isEmpty {
      let attrs: [NSAttributedString.Key: Any] = [.font: smallFont, .foregroundColor: muted]
      let box = (hover.context as NSString).boundingRect(
        with: .init(width: inner, height: 1000), options: [.usesLineFragmentOrigin],
        attributes: attrs)
      let h = min(ceil(box.height), ceil(smallFont.boundingRectForFont.height) * 2)
      let top = block(h)
      if draw {
        (hover.context as NSString).draw(
          with: .init(x: origin.x, y: top, width: inner, height: h),
          options: [.usesLineFragmentOrigin, .truncatesLastVisibleLine], attributes: attrs)
      }
    }
    return y + pad - origin.y + pad
  }

  // MARK: Parts

  /// Draws the tag right-aligned at `right`; returns its width.
  @discardableResult
  private static func drawTag(_ tag: Hover.Tag, right: CGFloat, top: CGFloat, draw: Bool) -> CGFloat
  {
    let font = PaxTheme.Typography.label(size: Chart.analysisHoverSmall, weight: .semibold)
    let size = (tag.text as NSString).size(withAttributes: [.font: font])
    let width = ceil(size.width) + 16
    guard draw else { return width }
    let frame = NSRect(x: right - width, y: top, width: width, height: 16)
    let fill = tag.color.blended(withFraction: 1 - 0.16, of: PaxTheme.Color.card) ?? tag.color
    fill.setFill()
    NSBezierPath(roundedRect: frame, xRadius: 8, yRadius: 8).fill()
    (tag.text as NSString).draw(
      at: .init(x: frame.minX + 8, y: frame.midY - size.height / 2),
      withAttributes: [.font: font, .foregroundColor: tag.color])
    return width
  }

  private static func drawBar(_ fraction: Double, color: NSColor, in rect: NSRect) {
    let shape = NSBezierPath(roundedRect: rect, xRadius: rect.height / 2, yRadius: rect.height / 2)
    PaxTheme.Table.line.setFill()
    shape.fill()
    guard fraction > 0 else { return }
    NSGraphicsContext.saveGraphicsState()
    defer { NSGraphicsContext.restoreGraphicsState() }
    shape.addClip()
    color.setFill()
    NSRect(
      x: rect.minX, y: rect.minY, width: CGFloat(min(1, fraction)) * rect.width, height: rect.height
    ).fill()
  }

  private static func drawGauge(_ gauge: Hover.Gauge, in rect: NSRect) {
    let total = max(gauge.total, 1)
    drawBar(gauge.value / total, color: gauge.color, in: rect)
    for tick in gauge.ticks {
      let x = rect.minX + CGFloat(min(1, max(0, tick.value / total))) * rect.width
      tick.color.setFill()
      NSBezierPath(
        roundedRect: .init(x: x - 0.75, y: rect.minY - 2, width: 1.5, height: rect.height + 4),
        xRadius: 0.75, yRadius: 0.75
      ).fill()
    }
  }

  private static func drawStack(_ items: [Hover.Breakdown], in rect: NSRect) {
    NSGraphicsContext.saveGraphicsState()
    defer { NSGraphicsContext.restoreGraphicsState() }
    NSBezierPath(roundedRect: rect, xRadius: rect.height / 2, yRadius: rect.height / 2).addClip()
    let total = items.reduce(0) { $0 + $1.weight }
    let seams = CGFloat(items.count - 1)
    var x = rect.minX
    for item in items {
      let w = CGFloat(item.weight / total) * (rect.width - seams)
      item.color.setFill()
      NSRect(x: x, y: rect.minY, width: w, height: rect.height).fill()
      x += w + 1
    }
  }
}
