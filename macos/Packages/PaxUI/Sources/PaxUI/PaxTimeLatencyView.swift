import AppKit

/// Latency readout plus shared time-series chart, for the traffic list (compact) and the
/// traffic detail overview (regular). Both receive the same domain and points, so the same
/// data lands at the same relative x. In regular density hover mirrors the bucket into the
/// readout; in compact (list) density the value stays the latest real latency (SCT-09).
@MainActor public final class PaxTimeLatencyView: NSView {
  public let chart: PaxTimeSeriesChart
  private let metric: NSTextField
  private let caption = PaxText.label("最新", font: PaxTheme.Typography.caption(), secondary: true)
  private let compact: Bool
  private let latest: String
  private let context: String
  private let format: (Double?) -> String
  private var hover: PaxTimeSeriesSelection?
  /// Compact only: left edge of the visible value text, in this view's coordinates.
  private(set) var metricTextLeft: CGFloat = 0

  public init(
    series: PaxTimeSeries, latest: String, compact: Bool,
    tooltip: PaxChartTooltipCoordinator? = nil,
    context: String = "最新", title: String = "响应延迟", pinKey: String? = nil,
    format: @escaping (Double?) -> String
  ) {
    self.compact = compact
    self.latest = latest
    self.context = context
    self.format = format
    chart = PaxTimeSeriesChart(
      series: series, style: .latency(title: title) { format($0) },
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
        self.metric.stringValue = selection.map { self.format($0.value) } ?? self.latest
        self.caption.stringValue = selection?.timeText ?? self.defaultCaption
        self.needsLayout = true
      }
    }
    for child in [metric, caption] { addSubview(child) }
    if !chart.isHidden { addSubview(chart) }
    metric.toolTip = context
    metric.setAccessibilityValue(latest + " · " + context)
    metric.lineBreakMode = .byTruncatingTail
    caption.stringValue = defaultCaption
    caption.lineBreakMode = .byTruncatingTail
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }

  private var defaultCaption: String {
    chart.isHidden && context == "最新" ? "暂无有效延迟" : context
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
      chart.frame = NSRect(
        x: 0, y: (bounds.height - PaxTheme.Trend.latencyCompactHeight) / 2,
        width: max(0, metricTextLeft - PaxTheme.TimeSeries.compactValueGap + trailing),
        height: PaxTheme.Trend.latencyCompactHeight)
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
