import AppKit

/// Latency readout plus shared time-series chart, for the traffic list (compact) and the
/// traffic detail overview (regular). They place samples by sequence. In regular density hover
/// mirrors the point into the readout. Compact density shows the latest real latency (SCT-09).
@MainActor public final class PaxTimeLatencyView: NSView {
  public let chart: PaxTimeSeriesChart
  private let metric: NSTextField
  private let caption = PaxText.label("最新", font: PaxTheme.Typography.caption(), secondary: true)
  private let emptyCaption: String?
  private let compact: Bool
  private var latest: String
  private var context: String
  private var hover: PaxTimeSeriesSelection?
  /// Compact only: left edge of the visible value text, in this view's coordinates.
  private(set) var metricTextLeft: CGFloat = 0

  public init(
    series: PaxTimeSeries, compact: Bool,
    tooltip: PaxChartTooltipCoordinator? = nil,
    context: String = "最新", title: String = "响应延迟", pinKey: String? = nil,
    emptyCaption: String? = nil, copy: PaxTimeSeriesCopy = .init()
  ) {
    self.emptyCaption = emptyCaption
    self.compact = compact
    let latest = PaxTimeSeriesStyle.latencyText(series.points.last?.value)
    self.latest = latest
    self.context = context
    var style = PaxTimeSeriesStyle.latency(title: title)
    style.copy = copy
    chart = PaxTimeSeriesChart(
      series: series, style: style,
      density: compact ? .compact : .regular)
    metric = PaxText.label(
      latest,
      font: PaxTheme.Typography.metric(
        size: compact ? PaxTheme.Dashboard.bodyFont : PaxTheme.TrafficDetail.latencyValueFont,
        weight: .semibold))
    super.init(frame: .zero)
    metric.setAccessibilityIdentifier(
      compact ? "traffic-latest-latency" : "traffic-detail-latest-latency")
    chart.setAccessibilityIdentifier("traffic-latency-trend")
    chart.tooltipCoordinator = tooltip
    chart.pinKey = pinKey
    chart.isHidden = !chart.hasData
    if !compact {
      chart.onSelect = { [weak self] selection in
        guard let self else { return }
        self.hover = selection
        self.metric.stringValue =
          selection.map { PaxTimeSeriesStyle.latencyText($0.value) } ?? self.latest
        self.caption.stringValue = selection?.timeText ?? self.defaultCaption
        self.needsLayout = true
      }
    }
    for child in [metric, caption] { addSubview(child) }
    if !chart.isHidden { addSubview(chart) }
    metric.toolTip = compact || tooltip != nil ? context : nil
    metric.setAccessibilityValue(latest + " · " + context)
    metric.lineBreakMode = .byTruncatingTail
    caption.stringValue = defaultCaption
    caption.lineBreakMode = .byTruncatingTail
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }

  /// Refresh the existing compact chart for the same object and query.
  public func updateCompact(series: PaxTimeSeries, context: String) {
    guard compact else { return }
    let latest = PaxTimeSeriesStyle.latencyText(series.points.last?.value)
    let latestChanged = self.latest != latest
    let contextChanged = self.context != context
    if latestChanged {
      self.latest = latest
      metric.stringValue = latest
      needsLayout = true
    }
    if contextChanged {
      self.context = context
      metric.toolTip = context
    }
    if latestChanged || contextChanged { metric.setAccessibilityValue(latest + " · " + context) }

    let previous = chart.series
    let changed =
      series.axis == .samples && previous.axis == .samples
      ? series.points != previous.points : series != previous
    if changed {
      let inset = chart.drawnTrailingInset
      chart.series = series
      let isHidden = !chart.hasData
      if chart.isHidden != isHidden { chart.isHidden = isHidden }
      if !isHidden, chart.superview == nil { addSubview(chart) }
      if chart.drawnTrailingInset != inset { needsLayout = true }
    }
    if contextChanged || changed { caption.stringValue = defaultCaption }
  }

  private var defaultCaption: String {
    chart.isHidden ? emptyCaption ?? context : context
  }

  public override var isFlipped: Bool { true }
  public override var intrinsicContentSize: NSSize {
    NSSize(
      width: NSView.noIntrinsicMetric,
      height: compact
        ? PaxTheme.Logs.eventRow
        : chart.isHidden
          ? PaxTheme.TrafficDetail.latencyReadoutHeight
          : PaxTheme.TrafficDetail.latencyReadoutHeight + PaxTheme.TrafficDetail.chartHeight)
  }

  public override func layout() {
    super.layout()
    let line = PaxTheme.RTT.textLine
    let label = PaxTheme.RTT.badgeTextHeight
    if compact {
      // The value is sized to its text, right-aligned at the column edge; the chart's drawn
      // right edge ends a fixed gap before the text (caption never shows in the list).
      let textWidth = metric.attributedStringValue.size().width
      let cellWidth = metric.cell?.cellSize.width ?? textWidth
      let padding = cellWidth - textWidth
      let width = min(bounds.width, PaxTheme.Dashboard.requestPeakWidth, ceil(cellWidth))
      let x = max(0, bounds.width - width)
      metric.frame = NSRect(x: x, y: (bounds.height - line) / 2, width: width, height: line)
      metric.alignment = .right
      caption.isHidden = true
      metricTextLeft = metric.frame.maxX - padding / 2 - min(textWidth, width - padding)
      let trailing = chart.drawnTrailingInset
      let chartFrame = NSRect(
        x: 0, y: (bounds.height - PaxTheme.Trend.latencyCompactHeight) / 2,
        width: max(0, metricTextLeft - PaxTheme.TimeSeries.compactValueGap + trailing),
        height: PaxTheme.Trend.latencyCompactHeight)
      if chart.frame != chartFrame {
        chart.frame = chartFrame
        chart.repositionVisibleTooltip()
      }
    } else {
      metric.frame = NSRect(
        x: 0, y: 0, width: bounds.width, height: PaxTheme.TrafficDetail.latencyReadoutHeight - label
      )
      caption.frame = NSRect(x: 0, y: metric.frame.maxY, width: bounds.width, height: label)
      chart.frame = NSRect(
        x: 0, y: caption.frame.maxY, width: bounds.width,
        height: max(0, bounds.height - caption.frame.maxY))
    }
  }
}
