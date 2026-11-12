import AppKit

/// One latency readout and one chart for both compact tables and full detail cards.
/// Only already-observed samples enter this view; selection never requests data.
@MainActor public final class PaxLatencyView: NSView {
  private let metric: NSTextField
  private let caption = PaxText.label("最新", font: PaxTheme.Typography.caption(), secondary: true)
  public let trend = PaxTrendView()
  private let compact: Bool
  private let latest: String
  private let context: String
  private let samples: [PaxTrendSample]
  private let format: (Double?) -> String
  public init(
    samples: [PaxTrendSample], latest: String, compact: Bool, aggregated: Bool = false,
    tooltip: PaxChartTooltipCoordinator? = nil, context: String = "最新",
    format: @escaping (Double?) -> String
  ) {
    let timeBuckets =
      samples.contains { $0.position != nil }
      && aggregated
    let ordered =
      timeBuckets ? samples : samples.map { PaxTrendSample(value: $0.value, label: $0.label) }
    self.samples = compact ? PaxCompactLatencySamples.reduce(ordered) : ordered
    self.latest = latest
    self.context = context
    self.compact = compact
    self.format = format
    metric = PaxText.label(
      latest,
      font: PaxTheme.Typography.metric(
        size: compact ? PaxTheme.Dashboard.bodyFont : PaxTheme.TrafficDetail.latencyValueFont,
        weight: .semibold))
    super.init(frame: .zero)
    metric.setAccessibilityIdentifier(
      compact ? "traffic-latest-latency" : "traffic-detail-latest-latency")
    trend.presentation = .latency
    trend.connectsMissingSamples = true
    trend.color = PaxTheme.DashboardColor.green
    trend.interactionColor = PaxTheme.DashboardColor.green
    trend.tooltipCoordinator = tooltip
    trend.samples = self.samples
    trend.setAccessibilityIdentifier("traffic-latency-trend")
    trend.isHidden =
      self.samples.compactMap(\.value).count
      < (compact ? PaxTheme.Trend.latencyCompactMinimumSamples : 2)
    trend.onSample = { [weak self] index in
      guard let self else { return }
      self.metric.stringValue = index.map { self.format(self.samples[$0].value) } ?? self.latest
      self.caption.stringValue = index.map { self.samples[$0].label } ?? self.defaultCaption
      self.needsLayout = true
    }
    for child in [metric, caption] { addSubview(child) }
    if !trend.isHidden { addSubview(trend) }
    metric.lineBreakMode = .byTruncatingTail
    caption.stringValue = defaultCaption
    caption.lineBreakMode = .byTruncatingTail
    trend.setAccessibilityHelp(
      (aggregated || timeBuckets)
        ? "按真实时间桶展示每桶最新响应；曲线跨缺测连续，缺测读数仍为未采集。" : "按观测顺序等距排列，不表示时间间隔；悬停可查看真实时间及延迟。")
  }
  private var defaultCaption: String {
    trend.isHidden
      ? samples.first?.label ?? "暂无有效延迟"
      : context + (samples.contains { $0.position != nil } ? " · 时间桶汇总" : " · 按观测顺序")
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var isFlipped: Bool { true }
  public override var intrinsicContentSize: NSSize {
    NSSize(
      width: NSView.noIntrinsicMetric,
      height: compact
        ? PaxTheme.Logs.eventRow
        : trend.isHidden
          ? PaxTheme.TrafficDetail.latencyReadoutHeight
          : PaxTheme.TrafficDetail.latencyReadoutHeight + PaxTheme.TrafficDetail.chartHeight)
  }
  public override func layout() {
    super.layout()
    let line = PaxTheme.RTT.textLine
    let label = PaxTheme.RTT.badgeTextHeight
    if compact {
      let width = min(bounds.width, PaxTheme.Dashboard.requestPeakWidth)
      let x = max(0, bounds.width - width)
      metric.frame = NSRect(
        x: x, y: (bounds.height - line) / 2, width: width,
        height: line)
      metric.alignment = .right
      caption.isHidden = context == "最新"
      if !caption.isHidden {
        caption.stringValue = context
        caption.frame = NSRect(
          x: x, y: metric.frame.minY - label / 2, width: metric.frame.width, height: label)
        metric.frame.origin.y += label / 2
        caption.alignment = .center
      }
      trend.frame = NSRect(
        x: 0, y: (bounds.height - PaxTheme.Trend.latencyCompactHeight) / 2,
        width: max(0, x - PaxTheme.Dashboard.requestGap),
        height: PaxTheme.Trend.latencyCompactHeight)
    } else {
      metric.frame = NSRect(
        x: 0, y: 0, width: bounds.width, height: PaxTheme.TrafficDetail.latencyReadoutHeight - label
      )
      caption.frame = NSRect(x: 0, y: metric.frame.maxY, width: bounds.width, height: label)
      trend.frame = NSRect(
        x: 0, y: caption.frame.maxY, width: bounds.width,
        height: max(0, bounds.height - caption.frame.maxY))
    }
  }
}
