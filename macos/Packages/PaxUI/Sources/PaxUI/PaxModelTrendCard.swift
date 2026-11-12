import AppKit

extension PaxTheme.Dashboard {
  /// 趋势卡片：左栏「标签 … 数值」行的行高（12pt × 1.5）与行距。
  static let factLine: CGFloat = 18
  static let rowGap: CGFloat = 6
  /// 图表上方图例行高与它到柱图的间距。
  static let legendHeight: CGFloat = 16
  static let legendGap: CGFloat = 8
}

public final class PaxModelTrendCard: PaxCard {
  private static let factCount = 3
  private let heading = PaxCardHeading("模型用量趋势", symbol: "chart.bar")
  private let tabs = PaxTabs(["费用", "Token"])
  private let modelPicker = PaxRoutePicker(style: .filter)
  public var onSelectFilter: ((PaxUsageFilter) -> Void)?
  private let legend = PaxSeriesLegend()
  private let value = PaxText.label(
    "", font: PaxTheme.Typography.metric(size: PaxTheme.Dashboard.largeMetric, weight: .medium))
  private let metricTitle = PaxText.label(
    "今日费用", font: PaxTheme.Typography.caption(), secondary: true)
  private let chartTitle = PaxText.label(
    "近 7 日费用构成",
    font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyTitle, weight: .medium))
  private let updated = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private let divider = NSBox()
  private let comparisonDivider = NSBox()
  private let factTitles = (0..<PaxModelTrendCard.factCount).map { _ in
    PaxModelTrendCard.rowLabel(secondary: true)
  }
  private let factValues = (0..<PaxModelTrendCard.factCount).map { _ in
    PaxModelTrendCard.rowLabel(secondary: false)
  }
  private let yesterdayTitle = PaxModelTrendCard.rowLabel("昨日", secondary: true)
  private let yesterdayValue = PaxModelTrendCard.rowLabel(secondary: false)
  private let averageTitle = PaxModelTrendCard.rowLabel("近 7 日日均", secondary: true)
  private let averageValue = PaxModelTrendCard.rowLabel(secondary: false)
  private let chart = PaxStackedChart(samples: [], seriesLabels: ["费用"], unit: "$")
  private var empty = PaxEmptyState(symbol: "chart.bar", title: "", caption: "")
  private var emptyTitle = ""
  private var model = PaxModelTrendModel()
  /// 0 费用，1 Token；卡片自己记住，周期刷新不重置。
  private(set) var metric = 0
  public override var isFlipped: Bool { true }

  /// 柱内与图例同序同色：输入、输出、缓存。
  private static let seriesColors = [
    PaxTheme.Chart.inputSeries, PaxTheme.Chart.outputSeries, PaxTheme.Chart.cacheSeries,
  ]

  private static func rowLabel(_ text: String = "", secondary: Bool) -> NSTextField {
    PaxText.label(
      text,
      font: PaxTheme.Typography.label(
        size: PaxTheme.Dashboard.bodyFont, weight: secondary ? .regular : .medium),
      secondary: secondary)
  }

  private var rows: [NSView] {
    factTitles + factValues + [yesterdayTitle, yesterdayValue, averageTitle, averageValue]
  }

  public init(tooltip: PaxChartTooltipCoordinator? = nil) {
    super.init(insets: NSEdgeInsets())
    for v in [
      heading, modelPicker, tabs, metricTitle, chartTitle, value, updated, legend, chart, empty,
      divider, comparisonDivider,
    ] + rows {
      addSubview(v)
    }
    divider.boxType = .separator
    comparisonDivider.boxType = .separator
    for field in factValues + [yesterdayValue, averageValue] { field.alignment = .right }
    chart.style = .summaryColumns
    chart.colors = Self.seriesColors
    chart.tooltipCoordinator = tooltip
    tabs.onSelect = { [weak self] in self?.showMetric($0) }
    tabs.setAccessibilityIdentifier("model-trend-metric")
    modelPicker.target = self
    modelPicker.action = #selector(selectModel)
    modelPicker.setAccessibilityIdentifier("model-trend-model")
    modelPicker.setAccessibilityLabel("Profile 与模型筛选")
    updateModelPicker()
    setAccessibilityIdentifier("model-trend-card")
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }

  public func apply(_ model: PaxModelTrendModel) {
    self.model = model
    updateModelPicker()
    if let text = model.empty ?? model.selectionEmpty, text != emptyTitle {
      empty.removeFromSuperview()
      empty = PaxEmptyState(symbol: "chart.bar", title: text, caption: "")
      addSubview(empty)
      emptyTitle = text
    }
    render()
  }

  private func updateModelPicker() {
    PaxUsageFilterMenu.populate(modelPicker, groups: model.groups, selection: model.selection)
  }

  @objc private func selectModel() {
    guard let value = PaxUsageFilterMenu.selection(of: modelPicker) else { return }
    onSelectFilter?(value)
  }

  func showMetric(_ index: Int) {
    metric = index
    render()
  }

  private func render() {
    let showsEmpty = model.empty != nil || model.selectionEmpty != nil
    let series = metric == 0 ? model.cost : model.tokens
    tabs.selectedIndex = metric
    for v in [metricTitle, chartTitle, value, legend, chart, divider, comparisonDivider] + rows
      as [NSView]
    { v.isHidden = showsEmpty }
    tabs.isHidden = model.empty != nil
    modelPicker.isHidden = model.empty != nil
    empty.isHidden = !showsEmpty
    updated.stringValue = model.updated
    updated.isHidden = model.updated.isEmpty
    value.stringValue = series.value
    metricTitle.stringValue = metric == 0 ? "今日费用" : "今日 Token"
    chartTitle.stringValue = metric == 0 ? "近 7 日费用构成" : "近 7 日 Token 构成"
    yesterdayTitle.stringValue = metric == 0 ? "昨日费用" : "昨日 Token"
    for index in 0..<Self.factCount {
      let fact = index < series.facts.count ? series.facts[index] : nil
      factTitles[index].stringValue = fact?.label ?? ""
      factValues[index].stringValue = fact?.value ?? ""
      factTitles[index].isHidden = showsEmpty || fact == nil
      factValues[index].isHidden = showsEmpty || fact == nil
    }
    yesterdayValue.stringValue = series.yesterday
    averageValue.stringValue = series.average
    legend.items = zip(["输入", "输出", "缓存"], Self.seriesColors).map { .init($0, color: $1) }
    chart.unit = metric == 0 ? "$" : "tokens"
    chart.currentIndex = model.currentIndex
    chart.samples = series.days
    needsLayout = true
  }

  private func place(_ title: NSTextField, _ field: NSTextField, y: CGFloat, width: CGFloat) {
    let inset = PaxTheme.Dashboard.inset
    // 西文末字常被取整裁掉，多留 2pt。
    let titleWidth = ceil(title.intrinsicContentSize.width) + 2
    let line = PaxTheme.Dashboard.factLine
    title.frame = NSRect(x: inset, y: y, width: titleWidth, height: line)
    let valueX = inset + titleWidth + PaxTheme.Spacing.medium
    field.frame = NSRect(x: valueX, y: y, width: max(1, inset + width - valueX), height: line)
  }

  public override func layout() {
    super.layout()
    let d = PaxTheme.Dashboard.self
    let inset = d.inset
    let inner = max(1, bounds.width - inset * 2)
    let tabSize = tabs.intrinsicContentSize
    tabs.frame = NSRect(
      x: bounds.width - inset - tabSize.width, y: inset + (d.icon - tabSize.height) / 2,
      width: tabSize.width, height: tabSize.height)
    let headingWidth =
      d.icon + d.titleGap + PaxTheme.Spacing.tight
      + ceil(
        (heading.text as NSString).size(withAttributes: [.font: heading.titleFont]).width)
    let pickerWidth = min(
      PaxUsageFilterMenu.pickerWidth(for: modelPicker),
      max(1, inner - tabSize.width - headingWidth - PaxTheme.Spacing.tight * 2))
    let pickerHeight = modelPicker.intrinsicContentSize.height
    modelPicker.frame = NSRect(
      x: tabs.frame.minX - PaxTheme.Spacing.tight - pickerWidth,
      y: inset + (d.icon - pickerHeight) / 2, width: pickerWidth, height: pickerHeight)
    let tag = updated.intrinsicContentSize
    let left = min(
      d.modelHeadlineWidth, max(d.modelHeadlineMinimum, inner * d.modelHeadlineFraction))
    let top = inset + d.icon + PaxTheme.Spacing.regular
    let showsEmpty = model.empty != nil || model.selectionEmpty != nil
    let trailing = model.empty == nil ? modelPicker.frame.minX : bounds.width - inset
    heading.frame = NSRect(
      x: inset, y: inset, width: max(1, trailing - inset - PaxTheme.Spacing.tight), height: d.icon)
    empty.frame = NSRect(
      x: inset, y: top, width: inner, height: bounds.height - top - inset)
    if showsEmpty {
      updated.frame = NSRect(x: inset, y: top, width: min(inner, tag.width), height: d.factLine)
      return
    }
    metricTitle.frame = NSRect(x: inset, y: top, width: left, height: d.factLine)
    value.font = PaxTheme.Typography.metric(size: d.largeMetric, weight: .medium)
    let naturalWidth = max(1, value.intrinsicContentSize.width)
    let fontSize = max(
      d.modelMinimumMetric, min(d.largeMetric, d.largeMetric * left / naturalWidth))
    value.font = PaxTheme.Typography.metric(size: fontSize, weight: .medium)
    let numberHeight = ceil(d.largeMetric * 1.1)
    value.frame = NSRect(x: inset, y: metricTitle.frame.maxY, width: left, height: numberHeight)
    updated.frame = NSRect(
      x: inset, y: value.frame.maxY + PaxTheme.Spacing.xs, width: left, height: d.factLine)
    var y = updated.frame.maxY + PaxTheme.Spacing.regular
    for index in 0..<Self.factCount {
      place(factTitles[index], factValues[index], y: y, width: left)
      y += d.factLine + d.rowGap
    }
    let averageY = bounds.height - inset - d.factLine
    let yesterdayY = averageY - d.factLine - d.rowGap
    comparisonDivider.frame = NSRect(
      x: inset, y: yesterdayY - PaxTheme.Spacing.regular, width: left,
      height: PaxTheme.Layout.cardBorder)
    place(averageTitle, averageValue, y: averageY, width: left)
    place(yesterdayTitle, yesterdayValue, y: yesterdayY, width: left)
    let chartX = inset + left + PaxTheme.Spacing.regular * 2
    let chartWidth = max(1, bounds.width - inset - chartX)
    divider.frame = NSRect(
      x: chartX - PaxTheme.Spacing.regular, y: top,
      width: PaxTheme.Layout.cardBorder, height: bounds.height - top - inset)
    let legendSize = legend.intrinsicContentSize
    // 窄卡把图例放在构成标题下方，保证标题和三项图例完整。
    let stackedLegend =
      chartTitle.intrinsicContentSize.width + legendSize.width + PaxTheme.Spacing.regular
      > chartWidth
    chartTitle.frame = NSRect(x: chartX, y: top, width: chartWidth, height: d.factLine)
    legend.frame = NSRect(
      x: stackedLegend ? chartX : bounds.width - inset - legendSize.width,
      y: stackedLegend ? chartTitle.frame.maxY + PaxTheme.Spacing.tight : top,
      width: min(chartWidth, legendSize.width), height: d.legendHeight)
    let chartTop = max(chartTitle.frame.maxY, legend.frame.maxY) + d.legendGap
    chart.frame = NSRect(
      x: chartX, y: chartTop + d.modelChartOffset, width: chartWidth,
      height: max(1, bounds.height - chartTop - inset))
  }
}
