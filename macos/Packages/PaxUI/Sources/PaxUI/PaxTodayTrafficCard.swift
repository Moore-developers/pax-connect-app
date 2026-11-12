import AppKit

/// 概览「今日流量」卡的值输入。字符串已由调用方格式化（「220 MB」）；缺统计用「—」。
public struct PaxTodayTrafficModel: Equatable {
  public var upload: String
  public var download: String
  public var asOf: String
  /// 代理 / 直连 / 未归类字节；全 0 时画空态并显示 `shareEmpty`。
  public var shares: [Double]
  public var shareEmpty: String
  public var hourly: [PaxChartSample]
  public var unit: String
  /// 柱图无任何可画数据时盖在图上的提示。
  public var hourlyNote: String
  public init(
    upload: String = "—", download: String = "—", asOf: String = "", shares: [Double] = [0, 0, 0],
    shareEmpty: String = "", hourly: [PaxChartSample] = [], unit: String = "MB",
    hourlyNote: String = ""
  ) {
    self.upload = upload
    self.download = download
    self.asOf = asOf
    self.shares = shares
    self.shareEmpty = shareEmpty
    self.hourly = hourly
    self.unit = unit
    self.hourlyNote = hourlyNote
  }
}

/// 代理 / 直连 / 未归类占比条 + 三项百分比图例；零总量只画空槽，不画假比例。
public final class PaxTrafficShareRow: NSView {
  public static let labels = ["代理", "直连", "未归类"]
  private var values: [Double] = [0, 0, 0]
  private let title = PaxText.label(
    "流量去向", font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyFont))
  private let legend = PaxSeriesLegend()
  private let emptyLabel = PaxText.label(
    "", font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyFont), secondary: true)
  public override var isFlipped: Bool { true }
  public override init(frame: NSRect) {
    super.init(frame: frame)
    for v in [title, legend, emptyLabel] { addSubview(v) }
    setAccessibilityElement(true)
    setAccessibilityRole(.image)
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }

  nonisolated public static func percentages(_ values: [Double]) -> [Int]? {
    let valid = values.map { $0.isFinite && $0 > 0 ? $0 : 0 }
    let total = valid.reduce(0, +)
    guard total > 0 else { return nil }
    return valid.map { Int(($0 / total * 100).rounded()) }
  }

  public func apply(shares: [Double], empty: String) {
    values = (0..<3).map { shares.indices.contains($0) ? shares[$0] : 0 }
    let percent = Self.percentages(values)
    legend.items =
      percent.map { percentages in
        Self.labels.enumerated().map { index, label in
          .init("\(label) \(percentages[index])%", color: PaxTheme.Color.series[index])
        }
      } ?? []
    legend.isHidden = percent == nil
    emptyLabel.stringValue = percent == nil ? empty : ""
    emptyLabel.isHidden = percent != nil
    let text =
      percent.map { zip(Self.labels, $0).map { "\($0) \($1)%" }.joined(separator: "，") } ?? empty
    setAccessibilityLabel("流量去向，" + text)
    needsDisplay = true
    needsLayout = true
  }

  public override func layout() {
    super.layout()
    let y = PaxTheme.Chart.trafficShareBar + PaxTheme.Spacing.tight
    let titleWidth = ceil(title.intrinsicContentSize.width)
    title.frame = NSRect(x: 0, y: y, width: titleWidth, height: PaxTheme.RTT.textLine)
    let contentX = titleWidth + PaxTheme.Spacing.regular
    let contentWidth = max(1, bounds.width - contentX)
    emptyLabel.frame = NSRect(x: contentX, y: y, width: contentWidth, height: PaxTheme.RTT.textLine)
    legend.frame = NSRect(
      x: contentX, y: y - PaxTheme.Dashboard.trafficLegendOffset,
      width: contentWidth, height: PaxTheme.RTT.textLine)
  }

  public override func draw(_ dirtyRect: NSRect) {
    let height = PaxTheme.Chart.trafficShareBar
    let track = NSRect(x: 0, y: 0, width: bounds.width, height: height)
    effectiveAppearance.performAsCurrentDrawingAppearance {
      NSGraphicsContext.saveGraphicsState()
      NSBezierPath(roundedRect: track, xRadius: height / 2, yRadius: height / 2).addClip()
      PaxTheme.Color.navigationHover.setFill()
      track.fill()
      let valid = values.map { $0.isFinite && $0 > 0 ? $0 : 0 }
      let total = valid.reduce(0, +)
      if total > 0 {
        let gap = PaxTheme.Chart.proportionGap
        let shown = valid.filter { $0 > 0 }.count
        let usable = max(0, bounds.width - gap * CGFloat(max(0, shown - 1)))
        var x: CGFloat = 0
        for (index, value) in valid.enumerated() where value > 0 {
          let width = max(PaxTheme.Chart.trafficShareMinimum, usable * CGFloat(value / total))
          PaxTheme.Color.series[index].setFill()
          NSRect(x: x, y: 0, width: width, height: height).fill()
          x += width + gap
        }
      }
      NSGraphicsContext.restoreGraphicsState()
    }
  }
}

/// 概览「今日流量」：上传 / 下载大数、占比条与填满剩余空间的每小时柱。
public final class PaxTodayTrafficCard: PaxCard {
  private let heading = PaxCardHeading("今日流量", symbol: "arrow.up.arrow.down", trailing: "")
  private let uploadCaption = PaxText.label("↑ 上传", font: PaxTheme.Typography.caption())
  private let downloadCaption = PaxText.label("↓ 下载", font: PaxTheme.Typography.caption())
  private let upload = PaxText.label("")
  private let download = PaxText.label("")
  private let share = PaxTrafficShareRow()
  private let metricDivider = NSBox()
  private let shareDivider = NSBox()
  private let chart = PaxStackedChart(samples: [], seriesLabels: ["上传", "下载"], unit: "MB")
  private let note = PaxText.label(
    "", font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.captionFont), secondary: true)
  public override var isFlipped: Bool { true }

  public init(
    tooltip: PaxChartTooltipCoordinator? = nil, axisLabel: ((Double) -> String)? = nil
  ) {
    super.init(insets: NSEdgeInsets())
    for v in [
      heading, uploadCaption, downloadCaption, upload, download, share, chart, note,
      metricDivider, shareDivider,
    ] {
      addSubview(v)
    }
    uploadCaption.textColor = PaxTheme.DashboardColor.uploadLabel
    downloadCaption.textColor = PaxTheme.DashboardColor.greenLabel
    for divider in [metricDivider, shareDivider] { divider.boxType = .separator }
    chart.style = .summaryColumns
    chart.fixedBarWidth = PaxTheme.Chart.trafficSummaryBarWidth
    chart.showsSummaryAmounts = false
    chart.axisLabel = axisLabel
    chart.colors = [PaxTheme.DashboardColor.amber, PaxTheme.DashboardColor.green]
    chart.tooltipCoordinator = tooltip
    note.alignment = .center
    setAccessibilityIdentifier("summary-traffic")
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }

  private func metric(_ text: String) -> NSAttributedString {
    let parts = text.split(separator: " ", maxSplits: 1)
    let value = NSMutableAttributedString(
      string: String(parts.first ?? ""),
      attributes: [
        .font: PaxTheme.Typography.metric(
          size: PaxTheme.Dashboard.secondaryMetric, weight: .medium),
        .foregroundColor: PaxTheme.Color.text,
      ])
    if parts.count > 1 {
      value.append(
        NSAttributedString(
          string: " " + parts[1],
          attributes: [
            .font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyFont),
            .foregroundColor: PaxTheme.Color.textSecondary,
          ]))
    }
    return value
  }

  public func apply(_ model: PaxTodayTrafficModel) {
    heading.trailing = model.asOf
    upload.attributedStringValue = metric(model.upload)
    download.attributedStringValue = metric(model.download)
    share.apply(shares: model.shares, empty: model.shareEmpty)
    chart.unit = model.unit
    chart.samples = model.hourly
    chart.isHidden = model.hourly.isEmpty
    note.stringValue = model.hourlyNote
    note.isHidden = !model.hourly.isEmpty
    needsLayout = true
  }

  public override func layout() {
    super.layout()
    let d = PaxTheme.Dashboard.self
    let inset = d.inset
    let inner = max(1, bounds.width - inset * 2)
    heading.frame = NSRect(x: inset, y: inset, width: inner, height: d.icon)
    let captionY = inset + d.icon + PaxTheme.Spacing.tight
    let half = inner / 2
    let columnGap = PaxTheme.Spacing.regular
    uploadCaption.frame = NSRect(x: inset, y: captionY, width: half, height: PaxTheme.RTT.textLine)
    downloadCaption.frame = NSRect(
      x: inset + half + columnGap, y: captionY, width: half - columnGap,
      height: PaxTheme.RTT.textLine)
    let metricY = captionY + PaxTheme.RTT.textLine
    let metricHeight = PaxTheme.RTT.numberHeight / 2
    upload.frame = NSRect(x: inset, y: metricY, width: half - columnGap, height: metricHeight)
    download.frame = NSRect(
      x: inset + half + columnGap, y: metricY, width: half - columnGap, height: metricHeight)
    metricDivider.frame = NSRect(
      x: inset + half, y: captionY, width: PaxTheme.Layout.cardBorder,
      height: PaxTheme.RTT.textLine + metricHeight)
    let shareY = metricY + metricHeight + PaxTheme.Spacing.regular
    shareDivider.frame = NSRect(
      x: inset, y: shareY, width: inner, height: PaxTheme.Layout.cardBorder)
    let shareTop = shareY + PaxTheme.Spacing.regular
    share.frame = NSRect(
      x: inset, y: shareTop, width: inner,
      height: PaxTheme.Chart.trafficShareBar + PaxTheme.Spacing.tight + PaxTheme.RTT.textLine)
    let chartTop = share.frame.maxY + PaxTheme.Spacing.tight
    chart.frame = NSRect(
      x: inset, y: chartTop, width: inner,
      height: max(1, bounds.height - chartTop - inset))
    note.frame = NSRect(
      x: inset, y: chart.frame.midY - PaxTheme.RTT.textLine / 2, width: inner,
      height: PaxTheme.RTT.textLine)
  }
}
