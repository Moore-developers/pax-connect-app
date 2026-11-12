import AppKit
import PaxCore
import PaxUI

/// Analysis cards share one height, set by whichever content needs more; this is only the chart floor.
private let trendChartFloor: CGFloat = 160
private let loadingHeight: CGFloat = 88
private let trendWidthFraction: CGFloat = 3.0 / 5

extension AIUsageViewController {
  func renderOverview() {
    let loading = loadingCard()
    let sections = [summaryStrip(), analysisPanels(), rankingPanel()]
    add(loading)
    for section in sections { add(section) }
    let update: () -> Void = { [weak self, weak loading] in
      guard let self else { return }
      let waiting =
        self.store.overviewPhase == .loading
        || (!self.store.preview && self.store.data.map { !$0.bucketsLoaded } == true)
      loading?.isHidden = !waiting || self.store.data?.error != nil
      for section in sections { section.isHidden = waiting }
    }
    contentRefreshers.append(update)
    update()
  }

  func statusItems() -> [NSView] {
    let note = PaxInlineStatus()
    let failure = PaxInlineStatus()
    let config = PaxInlineStatus()
    failure.onAction = { [weak self] in self?.store.data?.retry() }
    config.onAction = { [weak self] in self?.refreshConfiguration() }
    let update: () -> Void = { [weak self, weak note, weak failure, weak config] in
      guard let self else { return }
      note?.isHidden = self.store.preview
      note?.apply(
        .idle,
        text: self.store.data?.state.backfilling == true
          ? "补录中 · 正在读取本机 CLI 历史用量，可能包含安装前记录" : "用量来自本机 CLI 会话日志；费用为本地估算，非平台账单。")
      let code = self.store.data?.state.errorCode ?? ""
      let error =
        self.store.data?.error ?? (code.isEmpty ? nil : AIUsageFormatting.collectionError(code))
      failure?.isHidden = error == nil
      failure?.apply(.failure, text: error ?? "", action: error == nil ? nil : "重试")
      config?.isHidden = self.store.configuration.error == nil
      config?.apply(.failure, text: self.store.configuration.error ?? "", action: "重试")
    }
    contentRefreshers.append(update)
    update()
    return [note, failure, config, usageGettingStarted()]
  }

  private func loadingCard() -> NSView {
    let loading = PaxLoadingStateView()
    loading.apply(.loading, message: "正在读取本地用量…")
    let card = PaxCard()
    card.contentView = loading
    card.heightAnchor.constraint(
      equalToConstant: loadingHeight + PaxTheme.Spacing.card * 2
    ).isActive = true
    loading.heightAnchor.constraint(equalToConstant: loadingHeight).isActive = true
    return card
  }

  func overlay(_ button: NSButton, on card: NSView, below: Bool = false) {
    button.translatesAutoresizingMaskIntoConstraints = false
    card.addSubview(button, positioned: below ? .below : .above, relativeTo: nil)
    NSLayoutConstraint.activate([
      button.leadingAnchor.constraint(equalTo: card.leadingAnchor),
      button.trailingAnchor.constraint(equalTo: card.trailingAnchor),
      button.topAnchor.constraint(equalTo: card.topAnchor),
      button.bottomAnchor.constraint(equalTo: card.bottomAnchor),
    ])
  }
  func measureTabs(cost: Bool, onSelect: @escaping (Bool) -> Void) -> PaxTabs {
    let tabs = PaxTabs(["Token", "费用"])
    tabs.selectedIndex = cost ? 1 : 0
    tabs.onSelect = { onSelect($0 == 1) }
    return tabs
  }

  private func legend() -> NSView {
    let items = zip(
      ["输入", "输出", "缓存"],
      [PaxTheme.Chart.inputSeries, PaxTheme.Chart.outputSeries, PaxTheme.Chart.cacheSeries]
    ).map { name, color in
      let pair = AIUsageLayout.stack(
        [PaxLegendDot(color: color), AIUsageLayout.label(name, secondary: true)], vertical: false)
      pair.spacing = PaxTheme.Spacing.xs
      return pair
    }
    let average = AIUsageLayout.stack(
      [
        PaxLegendDot(color: PaxTheme.Chart.unitCostSeries),
        AIUsageLayout.label("输入成本", secondary: true),
      ], vertical: false)
    average.spacing = PaxTheme.Spacing.xs
    average.toolTip = "四项官方费用总和 ÷ 总输入Token × 一百万；单位成本下降不代表总费用下降"
    let row = AIUsageLayout.stack(items + [average], vertical: false)
    row.spacing = PaxTheme.Spacing.medium
    return row
  }

  private func trendContent() -> NSStackView {
    let measure = measureTabs(cost: store.measureCost) { [weak self] cost in
      guard let self, self.store.measureCost != cost else { return }
      self.refreshMeasuresOnly = true
      self.store.perform(.measureCost(cost))
    }
    contentRefreshers.append { [weak self, weak measure] in
      measure?.selectedIndex = self?.store.measureCost == true ? 1 : 0
    }
    let meta = AIUsageLayout.label(store.trendMeta, secondary: true)
    let picker = PaxRoutePicker(style: .filter)
    picker.target = self
    picker.action = #selector(selectTrendModel(_:))
    picker.setAccessibilityIdentifier("ai-trend-model")
    picker.setAccessibilityLabel("趋势 Profile 与模型筛选")
    let width = picker.widthAnchor.constraint(equalToConstant: 112)
    width.isActive = true
    trendModelPicker = picker
    trendModelWidth = width
    trendMetaLabel = meta
    refreshTrendHeader()
    let header = AIUsageTrendHeader(meta: meta, legend: legend(), picker: picker, measure: measure)
    let content = AIUsageLayout.stack([header, trendBody()])
    content.distribution = .fill
    content.spacing = PaxTheme.Spacing.regular
    return content
  }

  private func trendBody() -> NSView {
    let chart = trendChart()
    let empty = PaxEmptyState(
      symbol: "chart.bar", title: "暂无用量趋势", caption: "", identifier: "ai-trend-empty")
    chart.heightAnchor.constraint(greaterThanOrEqualToConstant: trendChartFloor).isActive = true
    empty.heightAnchor.constraint(greaterThanOrEqualToConstant: trendChartFloor).isActive = true
    let holder = AIUsageLayout.stack([chart, empty])
    holder.distribution = .fill
    chart.setContentHuggingPriority(.init(1), for: .vertical)
    empty.setContentHuggingPriority(.init(1), for: .vertical)
    holder.setContentHuggingPriority(.init(1), for: .vertical)
    holder.heightAnchor.constraint(greaterThanOrEqualToConstant: trendChartFloor).isActive = true
    let update: () -> Void = { [weak self, weak chart, weak empty] in
      guard let self else { return }
      self.refreshTrendHeader()
      let (samples, unit) = self.store.trendSeries(cost: self.store.measureCost)
      chart?.valueLine = self.trendUnitCostLine()
      chart?.samples = samples
      chart?.minimumColumnCount =
        self.store.trendFillsWidth ? samples.count : PaxTheme.Chart.usageMinimumColumns
      chart?.automaticallySpacesCategoryLabels = self.store.trendFillsWidth
      chart?.unit = unit
      chart?.hoverDetails = self.store.trendHoverDetails
      let reason = self.store.trendEmptyReason
      chart?.isHidden = reason != nil
      empty?.isHidden = reason == nil
      empty?.update(caption: reason ?? "")
    }
    contentRefreshers.append(update)
    update()
    return holder
  }

  func refreshTrendHeader() {
    trendMetaLabel?.stringValue = store.trendMeta
    guard let picker = trendModelPicker else { return }
    PaxUsageFilterMenu.populate(picker, groups: store.trendGroups, selection: store.trendFilter)
    trendModelWidth?.constant = PaxUsageFilterMenu.pickerWidth(for: picker)
    picker.superview?.needsLayout = true
  }

  @objc private func selectTrendModel(_ picker: PaxRoutePicker) {
    guard let value = PaxUsageFilterMenu.selection(of: picker) else { return }
    refreshMeasuresOnly = true
    store.perform(.trendFilter(value))
  }

  private func trendChart() -> PaxStackedChart {
    let (samples, unit) = store.trendSeries(cost: store.measureCost)
    let chart = PaxStackedChart(samples: samples, seriesLabels: ["输入", "输出", "缓存"], unit: unit)
    chart.colors = [
      PaxTheme.Chart.inputSeries, PaxTheme.Chart.outputSeries, PaxTheme.Chart.cacheSeries,
    ]
    chart.tooltipCoordinator = tooltip
    chart.style = .gradientRounded
    chart.hatched = true
    chart.barWidthFraction = PaxTheme.Chart.usageBarFraction
    chart.minimumBarColumnCount = PaxTheme.Chart.usageMinimumColumns
    chart.valueLine = trendUnitCostLine()
    chart.hoverDetails = store.trendHoverDetails
    chart.setAccessibilityIdentifier("ai-usage-trend-chart")
    return chart
  }

  private func trendUnitCostLine() -> PaxStackedChart.ValueLine {
    var line = PaxStackedChart.ValueLine(
      values: store.trendUnitCosts, name: "综合输入成本",
      unit: "USD / M 输入", color: PaxTheme.Chart.unitCostSeries)
    line.connectsGaps = true
    line.showsAxis = false
    return line
  }

  func analysisPanels() -> NSView {
    let client = clientContent()
    if let existing = clientAnalysisContent, let existingTrend = trendAnalysisContent {
      refreshTrendHeader()
      // Keep the existing header controls attached to the window so their thumb animation survives.
      replaceAnalysisBody(existing, with: Array(client.arrangedSubviews.dropFirst()))
      replaceAnalysisBody(existingTrend, with: [trendBody()])
      return NSView()
    }
    let trend = trendContent()
    clientAnalysisContent = client
    trendAnalysisContent = trend
    let inset = PaxTheme.Spacing.card / 2
    let insets = NSEdgeInsets(
      top: inset, left: PaxTheme.Spacing.card, bottom: inset, right: PaxTheme.Spacing.card)
    let trendCard = PaxCard(insets: insets)
    let clientCard = PaxCard(insets: insets)
    trendCard.contentView = trend
    clientCard.contentView = client
    let pair = NSView()
    for card in [trendCard, clientCard] {
      card.translatesAutoresizingMaskIntoConstraints = false
      pair.addSubview(card)
    }
    let gap = PaxTheme.Spacing.cardGap
    let minimum = trendCard.heightAnchor.constraint(equalToConstant: 0)
    minimum.priority = .defaultLow
    let fill = trend.bottomAnchor.constraint(
      equalTo: trendCard.bottomAnchor, constant: -inset)
    fill.priority = .defaultHigh
    NSLayoutConstraint.activate([
      trendCard.leadingAnchor.constraint(equalTo: pair.leadingAnchor),
      trendCard.topAnchor.constraint(equalTo: pair.topAnchor),
      trendCard.bottomAnchor.constraint(equalTo: pair.bottomAnchor),
      trendCard.widthAnchor.constraint(
        equalTo: pair.widthAnchor, multiplier: trendWidthFraction,
        constant: -gap * trendWidthFraction),
      clientCard.leadingAnchor.constraint(equalTo: trendCard.trailingAnchor, constant: gap),
      clientCard.trailingAnchor.constraint(equalTo: pair.trailingAnchor),
      clientCard.topAnchor.constraint(equalTo: trendCard.topAnchor),
      clientCard.heightAnchor.constraint(equalTo: trendCard.heightAnchor),
      trendCard.heightAnchor.constraint(
        greaterThanOrEqualTo: trend.heightAnchor, constant: inset * 2),
      trendCard.heightAnchor.constraint(
        greaterThanOrEqualTo: client.heightAnchor, constant: inset * 2),
      minimum, fill,
    ])
    return pair
  }
  private func replaceAnalysisBody(_ target: NSStackView, with bodies: [NSView]) {
    for item in target.arrangedSubviews.dropFirst() {
      target.removeArrangedSubview(item)
      item.removeFromSuperview()
    }
    for item in bodies {
      (item.superview as? NSStackView)?.removeArrangedSubview(item)
      item.removeFromSuperview()
      target.addArrangedSubview(item)
      item.widthAnchor.constraint(equalTo: target.widthAnchor).isActive = true
    }
  }
}
