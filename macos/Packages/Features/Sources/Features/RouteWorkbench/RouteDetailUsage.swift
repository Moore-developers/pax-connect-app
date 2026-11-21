import AppKit
import PaxCore
import PaxUI

extension RouteDetailController {
  func usageRecords() {
    let upload = PaxRouteMetric(
      title: self.interfaceCopy.text("dashboard.upload"), symbol: "arrow.up.right", value: "—",
      color: PaxTheme.DashboardColor.amber,
      style: .usage)
    let download = PaxRouteMetric(
      title: self.interfaceCopy.text("dashboard.download"), symbol: "arrow.down.left", value: "—",
      color: PaxTheme.DashboardColor.green,
      style: .usage)
    let connections = PaxRouteMetric(
      title: self.interfaceCopy.text("interface_detail.a5574109f0"), symbol: "arrow.triangle.swap",
      value: "—", unit: self.interfaceCopy.text("interface_detail.3a116815bc"),
      caption: self.interfaceCopy.text("interface_detail.b5eb7f2d76"),
      color: PaxTheme.Color.textSecondary, style: .usage)
    let active = PaxRouteMetric(
      title: self.interfaceCopy.text("interface_detail.e403ba5798"), symbol: "network", value: "—",
      unit: self.interfaceCopy.text("interface_detail.3a116815bc"),
      caption: self.interfaceCopy.text("interface_detail.fd68bb41e8"),
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
      self.interfaceCopy.routeLabel(
        "action.traffic.trend", font: PaxTheme.Typography.caption(), secondary: true), NSView(),
      PaxRouteLegend([
        (self.interfaceCopy.text("dashboard.upload"), PaxTheme.DashboardColor.amber),
        (self.interfaceCopy.text("dashboard.download"), PaxTheme.DashboardColor.green),
      ]
      ),
    ])
    let coverage = PaxRouteText.label("", font: PaxTheme.Typography.caption(), secondary: true)
    let more = PaxRouteButton(
      self.interfaceCopy.text("action.usage.load_more"), symbol: "clock", style: .quiet,
      target: self, action: #selector(moreUsage))
    items += [legend, chart, coverage, more]
    usageUpdater = { [weak self] in
      guard let self else { return }
      let p = self.store.info(self.id, context: self.interfaceCopy.context)
      let hasData = p.uploadMB != nil && p.downloadMB != nil
      metrics.isHidden = !hasData
      status.isHidden = hasData
      status.stringValue = self.store.dataCaption(self.id, context: self.interfaceCopy.context)
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
      connections.updateValue(
        p.windowConnections.map(String.init) ?? "—",
        unit: self.interfaceCopy.text("interface_detail.3a116815bc"))
      active.updateValue(
        p.activeConnections.map(String.init) ?? "—",
        unit: self.interfaceCopy.text("interface_detail.3a116815bc"))
      range.apply(
        zip(
          periods,
          [
            self.interfaceCopy.text("dashboard_value.today"),
            self.interfaceCopy.text("interface_detail.6082bd2a63"),
            self.interfaceCopy.text("interface_detail.afcd5fc44b"),
          ]
        ).map { .init(id: $0.0, title: $0.1) },
        selected: periods[self.usageRange])
      chart.pinKey = "route-usage-\(self.id)-\(self.usageRange)"
      let context = self.interfaceCopy.context
      if let series = p.transferSamples {
        var style = PaxTimeSeriesStyle.traffic(
          unit: routeUsageUnit(series.domain, context: context),
          title: context.text("action.traffic.trend"), upload: context.text("dashboard.upload"),
          download: context.text("dashboard.download"), formatValue: TrafficFormatting.megabytes)
        style.copy = timeSeriesCopy(context)
        chart.style = style
        chart.series = series
      } else {
        chart.series = emptySeries
      }
      chart.isHidden = p.transferSamples == nil
      legend.isHidden = chart.isHidden
      coverage.stringValue =
        self.store.isLive
        ? self.store.usageCoverageCaption(self.id, context: context)
        : self.interfaceCopy.text("interface_detail.4fd86b9f0a")
      coverage.isHidden = !hasData
      more.isHidden = !self.store.hasMoreUsage(self.id)
    }
    usageUpdater?()
    section(
      self.interfaceCopy.text("interface_detail.a57107ba0f"), symbol: "arrow.up.arrow.down",
      views: items,
      detail: store.isLive ? "" : self.interfaceCopy.text("interface_detail.4fd86b9f0a"))
  }
}
