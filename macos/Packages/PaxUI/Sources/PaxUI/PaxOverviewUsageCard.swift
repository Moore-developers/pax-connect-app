import AppKit

/// One filter and metric drive the daily composition and the ranking.
public final class PaxOverviewUsageCard: PaxCard {
  private let heading = PaxCardHeading("AI 模型用量", symbol: "chart.bar")
  private let summarySurface = NSView()
  private let summary = PaxText.label("", secondary: true)
  private let picker = PaxRoutePicker(style: .filter)
  private let tabs = PaxTabs(["费用", "Token"])
  private let link = PaxCompactButton("查看全部 ›", style: .text, target: nil, action: nil)
  private let chartTitle = PaxText.label("")
  private let rankTitle = PaxText.label("")
  private let order = PaxText.label("", secondary: true)
  private let footer = (0..<3).map { _ in PaxText.label("") }
  private let footerLine = NSBox()
  private let legend = PaxSeriesLegend()
  private let chart = PaxStackedChart(samples: [], seriesLabels: [], unit: "$")
  private let chartEmpty = PaxText.label("", secondary: true)
  private let rankLines = (0..<PaxModelRankModel.rowLimit).map { _ in NSBox() }
  private let divider = NSBox(), middle = NSBox()
  private var ranking:
    [(name: NSTextField, bar: PaxShareBar, amount: NSTextField, share: NSTextField)] =
      []
  private var trend = PaxModelTrendModel()
  private var rank = PaxModelRankModel()
  private var copy = PaxModelTrendCard.Copy()
  private var rankCopy = PaxModelRankCard.Copy()
  private var period = "近 10 日", leader = "主力"
  private var metric = 0
  public var onSelectFilter: ((PaxUsageFilter) -> Void)?
  public var onOpen: (() -> Void)?
  public override var isFlipped: Bool { true }

  public init(tooltip: PaxChartTooltipCoordinator?) {
    super.init(insets: NSEdgeInsets())
    for view in [
      heading, summarySurface, summary, picker, tabs, link, chartTitle, rankTitle, order,
      footerLine,
      legend, chart, chartEmpty, divider, middle,
    ] as [NSView] { addSubview(view) }
    footerLine.boxType = .separator
    for field in footer { addSubview(field) }
    footer[1].alignment = .center
    footer[2].alignment = .right
    for label in [chartTitle, rankTitle] {
      label.font = PaxTheme.Typography.label(size: 14, weight: .semibold)
    }
    summarySurface.wantsLayer = true
    summarySurface.layer?.backgroundColor = PaxTheme.Routes.canvas.cgColor
    summarySurface.layer?.cornerRadius = 6
    for line in rankLines {
      line.boxType = .separator
      addSubview(line)
    }
    divider.boxType = .separator
    middle.boxType = .separator
    order.alignment = .right
    chart.style = .summaryColumns
    chart.barWidthFraction = 0.38
    chart.colors = [
      PaxTheme.Chart.inputSeries, PaxTheme.Chart.outputSeries, PaxTheme.Chart.cacheSeries,
    ]
    chart.tooltipCoordinator = tooltip
    picker.target = self
    picker.action = #selector(selectFilter)
    link.target = self
    link.action = #selector(open)
    tabs.onSelect = { [weak self] index in
      self?.metric = index
      self?.render()
    }
    tabs.setAccessibilityIdentifier("model-trend-metric")
    picker.setAccessibilityIdentifier("model-trend-model")
    link.setAccessibilityIdentifier("model-rank-open")
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError() }
  @objc private func open() { onOpen?() }
  @objc private func selectFilter() {
    if let filter = PaxUsageFilterMenu.selection(of: picker) { onSelectFilter?(filter) }
  }

  public func applyCopy(
    _ copy: PaxModelTrendCard.Copy, _ rankCopy: PaxModelRankCard.Copy, period: String,
    leader: String
  ) {
    self.copy = copy
    self.rankCopy = rankCopy
    self.period = period
    self.leader = leader
    heading.text = copy.title
    rankTitle.stringValue = rankCopy.title
    link.title = rankCopy.open
    tabs.apply(labels: copy.metrics)
    chart.seriesLabels = copy.series
    chart.summarize = copy.chartSummary
    legend.items = zip(copy.series, chart.colors).map { .init($0, color: $1) }
    render()
  }
  public func apply(_ trend: PaxModelTrendModel, _ rank: PaxModelRankModel) {
    guard self.trend != trend || self.rank != rank else { return }
    self.trend = trend
    self.rank = rank
    render()
  }
  private func render() {
    PaxUsageFilterMenu.populate(
      picker, groups: trend.groups, selection: trend.selection, allTitle: copy.all,
      includeModels: false)
    tabs.selectedIndex = metric
    let series = metric == 0 ? trend.cost : trend.tokens
    chart.samples = series.days
    chart.currentIndex = trend.currentIndex
    chart.unit = metric == 0 ? "$" : "tokens"
    chartTitle.stringValue = copy.chart[metric]
    chartEmpty.stringValue = trend.empty ?? trend.selectionEmpty ?? ""
    chart.isHidden = !chartEmpty.stringValue.isEmpty
    let header = NSMutableAttributedString(
      string: "  " + period + " ",
      attributes: [
        .font: PaxTheme.Typography.caption(), .foregroundColor: PaxTheme.Color.textSecondary,
      ])
    header.append(
      NSAttributedString(
        string: rank.cost,
        attributes: [
          .font: PaxTheme.Typography.metric(size: 13, weight: .semibold),
          .foregroundColor: PaxTheme.Color.text,
        ]))
    if !rank.caption.isEmpty {
      header.append(
        NSAttributedString(
          string: "   |   " + rank.caption.replacingOccurrences(of: " · ", with: "   |   ") + "  ",
          attributes: [
            .font: PaxTheme.Typography.caption(), .foregroundColor: PaxTheme.Color.textSecondary,
          ]))
    }
    summary.attributedStringValue = header
    summary.toolTip = trend.updated
    let main = series.facts.count == 3 ? series.facts[2] : nil
    let facts = [
      (copy.yesterday[metric], series.yesterday), (copy.average, series.average),
      (leader, main.map { $0.label + " " + $0.value } ?? "—"),
    ]
    for (field, fact) in zip(footer, facts) {
      let text = NSMutableAttributedString(
        string: fact.0 + " ",
        attributes: [
          .foregroundColor: PaxTheme.Color.textSecondary, .font: PaxTheme.Typography.caption(),
        ])
      text.append(
        NSAttributedString(
          string: fact.1,
          attributes: [
            .foregroundColor: PaxTheme.Color.text,
            .font: PaxTheme.Typography.label(size: 12, weight: .semibold),
          ]))
      let paragraph = NSMutableParagraphStyle()
      paragraph.alignment = field === footer[2] ? .right : field === footer[1] ? .center : .left
      paragraph.lineBreakMode = .byTruncatingTail
      text.addAttribute(
        .paragraphStyle, value: paragraph, range: NSRange(location: 0, length: text.length))
      field.attributedStringValue = text
      field.toolTip = text.string
    }
    order.stringValue = copy.metrics[metric] + " ↓ / " + rankCopy.shares[metric]
    for row in ranking {
      for view in [row.name, row.bar, row.amount, row.share] as [NSView] {
        view.removeFromSuperview()
      }
    }
    ranking = []
    let values = metric == 0 ? rank.rows : rank.tokenRows
    for (index, row) in values.enumerated() {
      let name = PaxText.label(row.name, font: PaxTheme.Dashboard.readoutFont())
      name.toolTip = row.name
      let bar = PaxShareBar(
        segments: [.init(row.name, value: row.fraction)],
        colors: [PaxTheme.Dashboard.rankColors[index]], striped: [0], total: 1)
      let amount = PaxText.label(
        metric == 0 ? row.cost : row.tokens,
        font: PaxTheme.Dashboard.readoutFont(bold: true))
      let share = PaxText.label(
        row.percent, font: PaxTheme.Dashboard.readoutFont(size: 12), secondary: true)
      amount.alignment = .right
      share.alignment = .right
      for view in [name, bar, amount, share] as [NSView] { addSubview(view) }
      ranking.append((name, bar, amount, share))
    }
    needsLayout = true
  }
  public override func layout() {
    super.layout()
    let pad = PaxTheme.Dashboard.inset
    let width = bounds.width - 2 * pad
    func rect(_ x: CGFloat, _ y: CGFloat, _ w: CGFloat, _ h: CGFloat) -> NSRect {
      NSRect(x: x, y: y, width: max(0, w), height: max(0, h))
    }
    let wide = bounds.width >= 700
    let headY: CGFloat = 12
    let headHeight = PaxTheme.Dashboard.icon
    let headCenter = headY + headHeight / 2
    let controlHeight: CGFloat = 28
    let controlY = headCenter - controlHeight / 2
    heading.frame = rect(pad, headY, 134, headHeight)
    let linkW = link.intrinsicContentSize.width
    link.frame = rect(bounds.width - pad - linkW, controlY, linkW, controlHeight)
    let tabW = tabs.intrinsicContentSize.width
    tabs.frame = rect(link.frame.minX - tabW - 8, controlY, tabW, controlHeight)
    let pickW = min(
      PaxUsageFilterMenu.pickerWidth(for: picker) + 30,
      max(70, tabs.frame.minX - heading.frame.maxX - 16))
    picker.frame = rect(tabs.frame.minX - pickW - 8, controlY, pickW, controlHeight)
    let summarySize = summary.intrinsicContentSize
    let summaryWidth = min(summarySize.width + 6, picker.frame.minX - heading.frame.maxX - 6)
    let summaryHeight: CGFloat = 20
    summarySurface.frame = rect(
      heading.frame.maxX, headCenter - summaryHeight / 2, summaryWidth, summaryHeight)
    summary.frame = rect(
      summarySurface.frame.minX, headCenter - summarySize.height / 2, summaryWidth,
      summarySize.height)
    divider.frame = rect(pad, 49, width, 1)
    let half = wide ? (width - 28) / 2 : width
    let bottom = wide ? bounds.height - pad : bounds.height / 2
    chartTitle.frame = rect(pad, 60, half, 22)
    let lw = legend.intrinsicContentSize.width
    legend.frame = rect(pad + half - lw, 60, lw, 22)
    chart.frame = rect(pad, 86, half, bottom - 123)
    chartEmpty.frame = rect(pad, 120, half, 24)
    footerLine.frame = rect(pad, bottom - 30, half, 1)
    let first = half * 0.24
    let last = half * 0.39
    footer[0].frame = rect(pad, bottom - 20, first, 20)
    footer[1].frame = rect(pad + first, bottom - 20, half - first - last, 20)
    footer[2].frame = rect(pad + half - last, bottom - 20, last, 20)
    let rx = wide ? pad + half + 28 : pad
    let top = wide ? 60 : bottom + 12
    middle.frame = wide ? rect(rx - 14, 60, 1, bottom - 60) : rect(pad, bottom + 3, width, 1)
    rankTitle.frame = rect(rx, top, half * 0.65, 22)
    order.frame = rect(rx + half * 0.60, top, half * 0.40, 22)
    let step = (bounds.height - pad - top - 24) / CGFloat(rankLines.count)
    for (index, line) in rankLines.enumerated() {
      line.frame = rect(rx, top + 24 + CGFloat(index) * step, half, 1)
    }
    let shareWidth =
      (ranking.map { $0.share.intrinsicContentSize.width }.max() ?? 0).rounded(.up) + 4
    let amountWidth =
      (ranking.map { $0.amount.intrinsicContentSize.width }.max() ?? 0).rounded(.up) + 4
    for (i, row) in ranking.enumerated() {
      let y = top + 24 + CGFloat(i) * step
      let barX = rx + half * 0.34
      let shareX = rx + half - shareWidth
      let amountX = shareX - 8 - amountWidth
      row.name.frame = rect(rx, y + (step - 20) / 2, half * 0.34 - 6, 20)
      row.bar.frame = rect(barX, y + (step - 13.5) / 2, amountX - barX - 8, 13.5)
      row.amount.frame = rect(amountX, y + (step - 20) / 2, amountWidth, 20)
      row.share.frame = rect(shareX, y + (step - 20) / 2, shareWidth, 20)
    }
  }
}
