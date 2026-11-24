import AppKit

/// 概览「当前线路」卡的值输入：线路身份、最近一次延迟与最近有效测量；缺测只写原因，不补 0。
public struct PaxCurrentRouteModel: Equatable {
  public var name: String
  public var badge: String
  public var tags: [String]
  public var exitIP: String?
  public var exitRegion: String?
  public var measurementTime: String?
  public var rtt: Double?
  /// 无延迟数字时显示在数字下方的原因。
  public var note: String
  /// 「较上次测量」的变化文本，如「↑ 18 ms」；nil 显示缺测。
  public var delta: String?
  public var series = PaxTimeSeries(
    domain: .init(start: .distantPast, end: .distantPast), points: [], axis: .samples)
  public var pinKey: String?
  /// 当前实际样本数量。
  public var axis: [String]
  public var trendEmpty: String
  public var firstMeasurement = false
  public init(
    name: String = "", badge: String = "当前线路", tags: [String] = [], rtt: Double? = nil,
    note: String = "", delta: String? = nil, axis: [String] = [],
    trendEmpty: String = ""
  ) {
    self.name = name
    self.badge = badge
    self.tags = tags
    self.rtt = rtt
    self.note = note
    self.delta = delta
    self.axis = axis
    self.trendEmpty = trendEmpty
  }
}

public final class PaxCurrentRouteCard: PaxCard {
  public struct Copy {
    public var noSelection = "未选择线路", comparison = "较上次", latencyTitle = "响应延迟"
    public var latencyCopy = PaxTimeSeriesCopy()
    public var protocolLabel = "协议", exitLabel = "上次测得出口", measuredPrefix = "测量时间 "
    public var region = "出口地区", switchRoute = "切换线路"
    public init() {}
  }
  private var copy = Copy()
  private var model = PaxCurrentRouteModel()
  private let icon = PaxIconTile("wifi")
  private let title = PaxText.label("")
  private let badge = PaxTag("", style: .currentRoute)
  private let link = PaxCompactButton("", style: .text, target: nil, action: nil)
  private let number = PaxText.label("", font: PaxTheme.Dashboard.readoutFont(size: 42, bold: true))
  private let unit = PaxText.label("ms", font: PaxTheme.Dashboard.readoutFont(), secondary: true)
  private let delta = PaxTag("", style: .currentRoute)
  private let comparison = PaxText.label(
    "", font: PaxTheme.Dashboard.readoutFont(), secondary: true)
  private let note = PaxText.label("", font: PaxTheme.Dashboard.readoutFont(), secondary: true)
  private let panel = NSView(), line = NSBox()
  private let splitLine = NSBox(), chartLine = NSBox()
  private let axis = PaxText.label("", secondary: true)
  private let emptySurface = NSView()
  private let emptyIcon = PaxIcon.view("waveform.path.ecg", size: 20)
  private let emptyMetric = PaxText.label(
    "", font: PaxTheme.Dashboard.readoutFont(size: 16))
  private let chartEmpty = PaxText.label("", secondary: true)
  private let trend = PaxTimeSeriesChart(
    series: .init(
      domain: .init(start: .distantPast, end: .distantPast), points: [], axis: .samples),
    style: .latency(), density: .compact)
  private let labels = (0..<3).map { _ in
    PaxText.label("", font: PaxTheme.Dashboard.readoutFont(), secondary: true)
  }
  private let values = (0..<3).map { _ in PaxText.label("", font: PaxTheme.Dashboard.readoutFont())
  }
  public var onSwitch: (() -> Void)?
  public override var isFlipped: Bool { true }
  public init(tooltip: PaxChartTooltipCoordinator? = nil) {
    super.init(insets: NSEdgeInsets(), style: .currentRoute)
    for view in [panel, icon, title, badge, link, number, unit, delta, comparison, note, line]
      + labels + values
    { addSubview(view) }
    panel.wantsLayer = true
    panel.layer?.cornerRadius = 12
    panel.layer?.backgroundColor = PaxTheme.Routes.canvas.cgColor
    for view in [
      splitLine, chartLine, axis, emptySurface, emptyIcon, chartEmpty, emptyMetric, trend,
    ] as [NSView] { addSubview(view) }
    for divider in [line, splitLine, chartLine] { divider.boxType = .separator }
    emptySurface.wantsLayer = true
    emptySurface.layer?.cornerRadius = 12
    emptySurface.layer?.backgroundColor = PaxTheme.Routes.canvas.cgColor
    emptyIcon.contentTintColor = PaxTheme.Color.textSecondary
    addSubview(note, positioned: .above, relativeTo: emptySurface)
    chartEmpty.alignment = .center
    axis.alignment = .right
    trend.tooltipCoordinator = tooltip
    trend.fadesAreaFill = true
    trend.setAccessibilityIdentifier("route-quality-trend")
    link.target = self
    link.action = #selector(open)
    delta.font = PaxTheme.Dashboard.readoutFont(size: PaxTheme.Dashboard.captionFont)
    title.font = PaxTheme.Typography.label(size: 16, weight: .semibold)
    for value in values { value.alignment = .right }
    number.setAccessibilityIdentifier("route-quality-rtt")
    title.setAccessibilityIdentifier("route-quality-name")
    setAccessibilityIdentifier("current-route-card")
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError() }
  @objc private func open() { onSwitch?() }
  public func applyCopy(_ copy: Copy) {
    self.copy = copy
    apply(model)
  }
  public func apply(_ model: PaxCurrentRouteModel) {
    self.model = model
    title.stringValue = model.name.isEmpty ? copy.noSelection : model.name
    title.toolTip = title.stringValue
    let noRoute = model.name.isEmpty
    badge.text = model.badge
    badge.isHidden = noRoute
    for view in [panel, splitLine] + labels + values { view.isHidden = noRoute }
    link.title = copy.switchRoute
    number.stringValue = model.rtt.map { String(Int($0.rounded())) } ?? "—"
    number.textColor = model.rtt.map(PaxRTTColor.solid) ?? .secondaryLabelColor
    delta.text = model.delta ?? "—"
    comparison.stringValue = [
      model.firstMeasurement ? nil : copy.comparison, model.measurementTime,
    ].compactMap { $0 }.joined(separator: " · ")
    note.stringValue = model.note
    var style = PaxTimeSeriesStyle.latency(
      title: copy.latencyTitle, color: number.textColor ?? PaxTheme.CurrentRoute.muted)
    style.copy = copy.latencyCopy
    trend.style = style
    trend.pinKey = model.pinKey
    trend.series = model.series
    trend.isHidden = model.series.points.isEmpty
    chartEmpty.isHidden = !trend.isHidden
    chartEmpty.stringValue = model.trendEmpty
    emptySurface.isHidden = !trend.isHidden
    emptyIcon.isHidden = !trend.isHidden
    emptyMetric.stringValue = copy.latencyTitle
    emptyMetric.isHidden = noRoute || model.rtt != nil
    chartLine.isHidden = noRoute
    note.alignment = noRoute ? .center : .left
    chartEmpty.font =
      noRoute
      ? PaxTheme.Typography.label(size: 16, weight: .semibold) : PaxTheme.Typography.caption()
    chartEmpty.textColor = noRoute ? PaxTheme.Color.text : PaxTheme.Color.textSecondary
    number.isHidden = model.rtt == nil
    unit.isHidden = number.isHidden
    comparison.isHidden = model.delta == nil
    delta.isHidden = comparison.isHidden
    note.toolTip = model.note
    axis.stringValue = model.axis.joined(separator: " · ")
    for (field, text) in zip(labels, [copy.protocolLabel, copy.exitLabel, copy.region]) {
      field.stringValue = text
    }
    for (field, text) in zip(values, [model.tags.first, model.exitIP, model.exitRegion]) {
      field.stringValue = text ?? "—"
      field.toolTip = text
    }
    needsLayout = true
  }
  public override func layout() {
    super.layout()
    let p = PaxTheme.Dashboard.inset
    let w = bounds.width - 2 * p
    icon.frame = NSRect(x: p, y: p, width: 28, height: 28)
    link.frame.size = link.intrinsicContentSize
    link.frame.origin = NSPoint(
      x: bounds.width - p - link.frame.width, y: p + (28 - link.frame.height) / 2)
    let tx = icon.frame.maxX + 8
    badge.frame.size = badge.intrinsicContentSize
    let tw = min(
      title.intrinsicContentSize.width, max(1, link.frame.minX - tx - badge.frame.width - 12))
    let titleHeight = title.intrinsicContentSize.height
    title.frame = NSRect(
      x: tx, y: icon.frame.midY - titleHeight / 2, width: tw, height: titleHeight)
    badge.frame.origin = NSPoint(x: tx + tw + 6, y: p + (28 - badge.frame.height) / 2)
    line.frame = NSRect(x: p, y: icon.frame.maxY + 10, width: w, height: 1)
    let center = line.frame.maxY + 65
    let split = p + w * 0.40
    panel.frame = NSRect(x: split, y: center - 49, width: bounds.width - p - split, height: 98)
    splitLine.frame = NSRect(x: split - 10, y: center - 35, width: 1, height: 70)
    let chartTop = center + 64 + (note.stringValue.isEmpty ? 0 : 14)
    chartLine.frame = NSRect(x: p, y: chartTop, width: w, height: 1)
    axis.frame = NSRect(x: p, y: chartTop + 4, width: w, height: 17)
    trend.frame = NSRect(
      x: p, y: axis.frame.maxY, width: w, height: max(1, bounds.height - p - axis.frame.maxY))
    emptySurface.frame = trend.frame
    emptyIcon.frame = NSRect(
      x: emptySurface.frame.midX - 10, y: emptySurface.frame.midY - 22, width: 20, height: 20)
    chartEmpty.frame = NSRect(x: p + 6, y: emptySurface.frame.midY + 3, width: w - 12, height: 20)
    emptyMetric.frame = NSRect(x: p, y: center - 25, width: split - p - 12, height: 23)
    for i in labels.indices {
      let y = center - 36 + CGFloat(i) * 26
      let lw = min(labels[i].intrinsicContentSize.width, panel.frame.width * 0.46)
      labels[i].frame = NSRect(x: split + 8, y: y, width: lw, height: 21)
      values[i].frame = NSRect(
        x: labels[i].frame.maxX + 3, y: y,
        width: max(1, panel.frame.maxX - labels[i].frame.maxX - 11), height: 21)
    }
    number.frame = NSRect(
      x: p, y: center - 44, width: min(number.intrinsicContentSize.width, w * 0.40 - 30), height: 54
    )
    unit.frame = NSRect(x: number.frame.maxX + 2, y: center - 17, width: 26, height: 20)
    delta.frame.size = delta.intrinsicContentSize
    delta.frame.origin = NSPoint(x: p, y: center + 15)
    comparison.frame = NSRect(
      x: delta.frame.maxX + 4, y: center + 19, width: max(1, split - delta.frame.maxX - 8),
      height: 22)
    note.maximumNumberOfLines = model.rtt == nil ? 2 : 1
    note.lineBreakMode = .byTruncatingTail
    note.frame = NSRect(
      x: p, y: model.rtt == nil ? center + 3 : center + 42, width: split - p - 12,
      height: model.rtt == nil ? 44 : 20)
    if model.name.isEmpty {
      emptySurface.frame = NSRect(
        x: p, y: line.frame.maxY + 12, width: w,
        height: max(1, bounds.height - line.frame.maxY - 12 - p))
      let mid = emptySurface.frame.midY
      emptyIcon.frame = NSRect(x: emptySurface.frame.midX - 17, y: mid - 40, width: 34, height: 34)
      chartEmpty.frame = NSRect(x: p + 12, y: mid, width: w - 24, height: 23)
      note.frame = NSRect(x: p + 12, y: mid + 26, width: w - 24, height: 22)
    }
  }
}
