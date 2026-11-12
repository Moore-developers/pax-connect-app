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

/// 最新参照稿：线路身份、双列测量与出口摘要、通栏趋势。
public final class PaxCurrentRouteCard: PaxCard {
  private let icon = PaxIconTile("antenna.radiowaves.left.and.right")
  private let heading = PaxText.label("")
  private let badge = PaxTag("", style: .currentRoute)
  private let number = PaxText.label("")
  private let unit = PaxText.label("ms")
  private let delta = PaxTag("", style: .currentRoute)
  private let comparison = PaxText.label("较上次测量")
  private let measured = PaxText.label("")
  private let note = PaxText.label("")
  private let protocolLabel = PaxText.label("协议")
  private let exitLabel = PaxText.label("上次测得出口")
  private let proto = PaxText.label("")
  private let exitIP = PaxText.label("")
  private let region = PaxText.label("")
  private let axis = PaxText.label("")
  private let empty = PaxText.label("")
  private let dividers = PaxTheme.CurrentRoute.dividers.map { _ in NSView() }
  private let trend = PaxTimeSeriesChart(
    series: .init(
      domain: .init(start: .distantPast, end: .distantPast), points: [], axis: .samples),
    style: .latency { "\(Int($0.rounded())) ms" }, density: .compact)
  public override var isFlipped: Bool { true }

  public init(tooltip: PaxChartTooltipCoordinator? = nil) {
    super.init(insets: NSEdgeInsets(), style: .currentRoute)
    for view in [
      icon, heading, badge, number, unit, delta, comparison, measured, note,
      protocolLabel, proto, exitLabel, exitIP, region, trend, axis, empty,
    ] + dividers {
      addSubview(view)
    }
    for label in [
      heading, number, unit, comparison, measured, note,
      protocolLabel, proto, exitLabel, exitIP, region, axis, empty,
    ] {
      label.textColor = PaxTheme.CurrentRoute.ink
      label.maximumNumberOfLines = 1
      label.lineBreakMode = .byTruncatingTail
    }
    for label in [protocolLabel, exitLabel, measured, note, empty] {
      label.textColor = PaxTheme.CurrentRoute.muted
    }
    for divider in dividers {
      divider.wantsLayer = true
      divider.layer?.backgroundColor = PaxTheme.CurrentRoute.line.cgColor
    }
    empty.alignment = .center
    axis.alignment = .right
    axis.textColor = PaxTheme.CurrentRoute.muted
    trend.fadesAreaFill = true
    trend.tooltipCoordinator = tooltip
    number.setAccessibilityIdentifier("route-quality-rtt")
    heading.setAccessibilityIdentifier("route-quality-name")
    trend.setAccessibilityIdentifier("route-quality-trend")
    setAccessibilityIdentifier("current-route-card")
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }

  public func apply(_ model: PaxCurrentRouteModel) {
    heading.stringValue = model.name.isEmpty ? "未选择线路" : model.name
    heading.toolTip = heading.stringValue
    badge.text = model.badge
    proto.stringValue = model.tags.first ?? "—"
    exitIP.stringValue = model.exitIP ?? "—"
    region.stringValue = model.exitRegion ?? "—"
    exitIP.toolTip = exitIP.stringValue
    region.toolTip = model.exitRegion
    number.stringValue = model.rtt.map { String(Int($0.rounded())) } ?? "—"
    number.setAccessibilityLabel(number.stringValue + " ms")
    let color =
      model.rtt.map {
        PaxRTTColor.band($0) == 1 ? PaxTheme.CurrentRoute.amber : PaxRTTColor.solid($0)
      } ?? PaxTheme.CurrentRoute.muted
    number.textColor = color
    unit.textColor = color
    delta.text = model.delta ?? "—"
    comparison.isHidden = model.delta == "首次测量"
    measured.stringValue = "测量时间 " + (model.measurementTime ?? "—")
    note.stringValue = model.note
    note.toolTip = model.note
    note.isHidden = model.note.isEmpty
    let drawable = !model.series.points.isEmpty
    trend.isHidden = !drawable
    empty.isHidden = drawable
    empty.stringValue = model.trendEmpty
    trend.style = .latency(color: color) { "\(Int($0.rounded())) ms" }
    trend.pinKey = model.pinKey
    trend.series = model.series
    axis.stringValue = drawable ? model.axis.joined(separator: " · ") : ""
    needsLayout = true
  }

  public override func layout() {
    super.layout()
    let t = PaxTheme.CurrentRoute.self
    let scale = bounds.width / t.referenceWidth
    let inset = PaxTheme.Dashboard.inset
    let content = bounds.insetBy(dx: inset, dy: inset)
    let horizontalScale = content.width / t.referenceContent.width
    let verticalScale = content.height / t.referenceContent.height
    func scaled(_ rect: NSRect) -> NSRect {
      NSRect(
        x: inset + (rect.minX - t.referenceContent.minX) * horizontalScale,
        y: inset + (rect.minY - t.referenceContent.minY) * verticalScale,
        width: rect.width * horizontalScale, height: rect.height * verticalScale)
    }
    let labels: [(NSTextField, (NSRect, CGFloat))] = [
      (heading, t.heading), (number, t.number), (unit, t.unit),
      (comparison, t.comparison), (measured, t.measured), (note, t.note),
      (protocolLabel, t.protocolLabel), (proto, t.proto), (exitLabel, t.exitLabel),
      (exitIP, t.exitIP), (region, t.region), (axis, t.axis),
    ]
    for (label, token) in labels {
      let size = max(t.minimumFont, token.1 * scale)
      label.font =
        label === number
        ? PaxTheme.Typography.metric(size: size, weight: .bold)
        : PaxTheme.Typography.label(
          size: size,
          weight: label === heading ? .semibold : label === axis ? .regular : .medium)
      label.frame = scaled(token.0)
      label.frame.size.height = max(label.frame.height, label.intrinsicContentSize.height)
    }
    if let font = number.font {
      let width = (number.stringValue as NSString).size(withAttributes: [.font: font]).width
      let available = max(1, number.frame.width - PaxTheme.Spacing.tight)
      if width > available {
        number.font = PaxTheme.Typography.metric(
          size: font.pointSize * available / width, weight: .bold)
      }
    }
    // 两种字号沿同一基线摆放，数值位数变化只缩小数字，不遮挡单位。
    if let numberFont = number.font, let unitFont = unit.font {
      unit.frame.origin.y = number.frame.minY + numberFont.ascender - unitFont.ascender
    }
    icon.frame = NSRect(
      x: inset, y: inset,
      width: PaxTheme.Dashboard.icon, height: PaxTheme.Dashboard.icon)
    heading.frame.origin.x = icon.frame.maxX + PaxTheme.Dashboard.titleGap
    heading.frame.origin.y = icon.frame.midY - heading.intrinsicContentSize.height / 2
    heading.frame.size.height = heading.intrinsicContentSize.height
    for label in [number, measured, note] { label.frame.origin.x = inset }
    for (tag, token) in [(badge, t.badge), (delta, t.delta)] {
      tag.font = t.font(max(t.minimumFont, token.1 * scale))
      tag.frame = scaled(token.0)
      tag.frame.size.height = max(tag.frame.height, tag.intrinsicContentSize.height)
      tag.frame.size.width = max(tag.frame.width, tag.intrinsicContentSize.width)
    }
    badge.frame.origin.x = content.maxX - badge.frame.width
    badge.frame.origin.y = icon.frame.midY - badge.frame.height / 2
    delta.frame.origin.x = inset
    heading.frame.size.width = max(
      1, badge.frame.minX - PaxTheme.Spacing.tight - heading.frame.minX)
    comparison.frame.origin.x = max(
      comparison.frame.minX, delta.frame.maxX + PaxTheme.Spacing.tight)
    comparison.frame.size.width = max(
      1, scaled(t.dividers[1]).minX - PaxTheme.Spacing.tight - comparison.frame.minX)
    for (view, frame) in zip(dividers, t.dividers) { view.frame = scaled(frame) }
    for index in [0, 2] {
      dividers[index].frame.origin.x = inset
      dividers[index].frame.size.width = content.width
    }
    axis.frame.origin.x = inset
    axis.frame.size.width = content.width
    let chart = scaled(t.chart)
    trend.frame = chart.insetBy(dx: -PaxTheme.Trend.inset, dy: -PaxTheme.Trend.inset)
    empty.font = t.font(max(t.minimumFont, t.measured.1 * scale))
    empty.frame = NSRect(
      x: chart.minX, y: chart.midY,
      width: chart.width, height: empty.intrinsicContentSize.height)
  }
}
