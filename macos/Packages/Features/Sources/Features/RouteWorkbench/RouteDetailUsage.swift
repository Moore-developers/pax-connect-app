import AppKit
import PaxCore
import PaxUI

extension RouteDetailController {
  func usageRecords() {
    let upload = PaxRouteMetric(
      title: "上传", symbol: "arrow.up.right", value: "—", color: PaxTheme.DashboardColor.amber,
      style: .usage)
    let download = PaxRouteMetric(
      title: "下载", symbol: "arrow.down.left", value: "—", color: PaxTheme.DashboardColor.green,
      style: .usage)
    let connections = PaxRouteMetric(
      title: "连接", symbol: "arrow.triangle.swap", value: "—", unit: "个", caption: "时间窗内新建",
      color: PaxTheme.Color.textSecondary, style: .usage)
    let active = PaxRouteMetric(
      title: "当前连接", symbol: "network", value: "—", unit: "个", caption: "最近实际采样",
      color: PaxTheme.Color.healthy, style: .usage)
    let metrics = NSStackView()
    metrics.orientation = .vertical
    metrics.alignment = .leading
    var metricStructure: [Bool] = []
    let status = PaxRouteText.label(
      "", font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
    var items: [NSView] = [metrics, status]
    let periods = ["today", "week", "month"]
    let range = PaxTabBar(style: .groups)
    range.setAccessibilityIdentifier("route-usage-range")
    range.onSelect = { [weak self] key in
      guard let self, let selected = periods.firstIndex(of: key) else { return }
      self.usageRange = selected
      self.usageRefreshTask?.cancel()
      self.usageRefreshTask = Task { [store = self.store, id = self.id] in
        await store.setUsagePeriod(id, period: key)
      }
    }
    if store.isLive { items.append(range) }
    let emptySeries = PaxTimeSeries(domain: .init(start: Date(), end: Date()), points: [])
    let chart = PaxTimeSeriesChart(
      series: emptySeries, style: .traffic(unit: "MB", formatValue: TrafficFormatting.megabytes),
      density: .regular)
    chart.tooltipCoordinator = tooltip
    chart.heightAnchor.constraint(equalToConstant: PaxTheme.Routes.chartHeight).isActive = true
    let legend = NSStackView(views: [
      PaxRouteText.label("流量趋势", font: PaxTheme.Typography.caption(), secondary: true), NSView(),
      PaxRouteLegend([("上传", PaxTheme.DashboardColor.amber), ("下载", PaxTheme.DashboardColor.green)]
      ),
    ])
    let coverage = PaxRouteText.label("", font: PaxTheme.Typography.caption(), secondary: true)
    let more = PaxRouteButton(
      "加载更多用量", symbol: "clock", style: .quiet, target: self, action: #selector(moreUsage))
    items += [legend, chart, coverage, more]
    usageUpdater = { [weak self] in
      guard let self else { return }
      let p = self.store.info(self.id)
      let hasData = p.uploadMB != nil && p.downloadMB != nil
      metrics.isHidden = !hasData
      status.isHidden = hasData
      status.stringValue = self.store.dataCaption(self.id)
      upload.updateValue(
        p.uploadMB.map { TrafficFormatting.bytes(UInt64(max(0, $0 * 1_000_000))) } ?? "—")
      download.updateValue(
        p.downloadMB.map { TrafficFormatting.bytes(UInt64(max(0, $0 * 1_000_000))) } ?? "—")
      let hasConnections = p.windowConnections != nil
      let hasActive =
        self.store.isCurrent(self.id) && self.store.isRunning && p.activeConnections != nil
      let structure = [hasConnections, hasActive]
      if metricStructure != structure {
        metricStructure = structure
        for metric in [upload, download, connections, active] { metric.removeFromSuperview() }
        for child in metrics.arrangedSubviews {
          metrics.removeArrangedSubview(child)
          child.removeFromSuperview()
        }
        var children: [NSView] = [upload, download]
        if hasConnections { children.append(connections) }
        if hasActive { children.append(active) }
        let row = PaxRouteMetricRow(children)
        row.spacing = PaxTheme.Spacing.regular
        metrics.addArrangedSubview(row)
        row.widthAnchor.constraint(equalTo: metrics.widthAnchor).isActive = true
      }
      connections.updateValue(p.windowConnections.map(String.init) ?? "—", unit: "个")
      active.updateValue(p.activeConnections.map(String.init) ?? "—", unit: "个")
      range.apply(
        zip(periods, ["今日", "近7天", "近30天"]).map { .init(id: $0.0, title: $0.1) },
        selected: periods[self.usageRange])
      chart.pinKey = "route-usage-\(self.id)-\(self.usageRange)"
      if let series = p.transferSamples {
        chart.style = .traffic(
          unit: routeUsageUnit(series.domain), formatValue: TrafficFormatting.megabytes)
        chart.series = series
      } else {
        chart.series = emptySeries
      }
      chart.isHidden = p.transferSamples == nil
      legend.isHidden = chart.isHidden
      coverage.stringValue = self.store.isLive ? self.store.usageCoverageCaption(self.id) : "今日累计"
      coverage.isHidden = !hasData
      more.isHidden = !self.store.hasMoreUsage(self.id)
    }
    usageUpdater?()
    section(
      "使用记录", symbol: "arrow.up.arrow.down", views: items,
      detail: store.isLive ? "" : "今日累计")
  }
}
