import AppKit
import PaxCore
import PaxUI

/// Two comparable headline values; token volume and unit price remain in the shared readout.
@MainActor final class AILimitActivityView: NSView {
  init(summary: AILimitSummary, readout: AILimitReadout) {
    super.init(frame: .zero)
    let observed = summary.observedThrough ?? .distantPast
    let elapsed = summary.days.filter { $0.at < observed }
    let days = Array(elapsed.suffix(7))
    let singleDay = summary.days.count == 1
    let period =
      days.first.flatMap { first in
        days.last.map { first.at == $0.at ? Self.dayTitle(first.at) : Self.period(first.at, $0.at) }
      } ?? "暂无每日记录"
    let title = PaxText.label("会话与开销", font: PaxTheme.Typography.caption(), secondary: true)
    let range = PaxText.label(period, font: PaxTheme.Typography.label(size: 10), secondary: true)
    let heading = AIUsageLayout.stack([title, NSView(), range], vertical: false)
    let metrics = AIUsageLayout.stack(
      (0..<2).map { index in
        Self.metric(index, summary: summary, days: days, singleDay: singleDay, readout: readout)
      }, vertical: false, equal: true)
    metrics.spacing = PaxTheme.Spacing.medium
    let complete = !elapsed.isEmpty && elapsed.allSatisfy(\.available) && summary.unpriced == 0
    let improved: Bool
    if singleDay, complete, let previous = summary.comparison,
      previous.sessions > 0, previous.input.official > 0
    {
      improved =
        summary.sessions > previous.sessions
        && summary.input.official < previous.input.official * 0.9995
    } else {
      improved = false
    }
    let status = PaxText.label(
      improved ? "会话增加，费用下降" : "费用含缓存与子代理",
      font: PaxTheme.Typography.label(size: 11), secondary: true)
    if improved { status.textColor = PaxTheme.DashboardColor.greenLabel }
    let hint = PaxText.label("详情 ⓘ", font: PaxTheme.Typography.label(size: 10), secondary: true)
    let footerRow = AIUsageLayout.stack([status, NSView(), hint], vertical: false)
    let footer = AILimitReadoutHost(footerRow, readout: readout) {
      Self.summaryDetail(summary, complete: complete, singleDay: singleDay)
    }
    footer.setAccessibilityElement(true)
    footer.setAccessibilityRole(.button)
    footer.setAccessibilityLabel("会话与开销详情")
    let rule = AIUsageLayout.separator()
    for child in [heading, metrics, rule, footer] {
      addSubview(child)
      child.translatesAutoresizingMaskIntoConstraints = false
    }
    NSLayoutConstraint.activate([
      heightAnchor.constraint(equalToConstant: AILimitKpiCard.height - PaxTheme.Spacing.card * 2),
      heading.leadingAnchor.constraint(equalTo: leadingAnchor),
      heading.trailingAnchor.constraint(equalTo: trailingAnchor),
      heading.topAnchor.constraint(equalTo: topAnchor),
      metrics.topAnchor.constraint(
        equalTo: heading.bottomAnchor, constant: PaxTheme.Spacing.medium),
      metrics.leadingAnchor.constraint(equalTo: leadingAnchor),
      metrics.trailingAnchor.constraint(equalTo: trailingAnchor),
      rule.leadingAnchor.constraint(equalTo: leadingAnchor),
      rule.trailingAnchor.constraint(equalTo: trailingAnchor),
      rule.bottomAnchor.constraint(equalTo: footer.topAnchor, constant: -10),
      footer.leadingAnchor.constraint(equalTo: leadingAnchor),
      footer.trailingAnchor.constraint(equalTo: trailingAnchor),
      footer.bottomAnchor.constraint(equalTo: bottomAnchor),
      footer.heightAnchor.constraint(equalToConstant: 16),
    ])
    setAccessibilityIdentifier("ai-session-usage-cost")
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  private static func metric(
    _ index: Int, summary: AILimitSummary, days: [AILimitDay], singleDay: Bool,
    readout: AILimitReadout
  ) -> NSView {
    let isCost = index == 0
    let label = PaxText.label(
      isCost ? (summary.unpriced > 0 ? "官方输入费 · 小计" : "官方输入费") : "活跃主会话",
      font: PaxTheme.Typography.label(size: 11), secondary: true)
    if !isCost { label.toolTip = "主会话按周期去重，包含其子代理活动。" }
    let costKnown = summary.requests == 0 || summary.unpriced < summary.requests
    let text =
      isCost
      ? AILimitStyle.money(costKnown ? summary.input.official : nil, precision: 2)
      : "\(summary.sessions) 个"
    let amount = PaxText.label(text, font: PaxTheme.Typography.metric(size: 22, weight: .light))
    amount.toolTip = text
    let evidence: NSView
    if singleDay {
      let current = isCost ? summary.input.official : Double(summary.sessions)
      let previous = summary.comparison.map { isCost ? $0.input.official : Double($0.sessions) }
      let delta = AILimitSummaryStrip.change(current, from: previous)
      let change = PaxText.label(
        delta ?? "上日无有效对照", font: PaxTheme.Typography.label(size: 11), secondary: true)
      if isCost, delta != nil, let previous {
        change.textColor =
          current < previous
          ? PaxTheme.DashboardColor.greenLabel
          : current > previous ? PaxTheme.DashboardColor.redLabel : PaxTheme.Color.textSecondary
      }
      change.toolTip = "今天比上日同一时刻；已结束日期比上日全天。缺价、缺历史或无请求时不比较。"
      evidence = change
    } else {
      let maximum = max(
        isCost ? 0.000001 : 1,
        days.filter(\.available).compactMap {
          Self.value($0, isCost: isCost)
        }.max() ?? 0)
      let columns = days.map { day -> NSView in
        let column = AILimitDayColumn(day: day, isCost: isCost, maximum: maximum)
        let host = AILimitReadoutHost(column, readout: readout) {
          Self.dayDetail(day, through: summary.observedThrough ?? .distantPast)
        }
        host.setAccessibilityElement(true)
        host.setAccessibilityRole(.button)
        host.setAccessibilityLabel(Self.dayTitle(day.at))
        host.setAccessibilityIdentifier(
          "ai-session-day-" + String(Int(day.at.timeIntervalSince1970))
            + (isCost ? "-cost" : "-sessions"))
        host.setAccessibilityValue(Self.lines(day).joined(separator: "，"))
        host.setAccessibilityHelp(
          "0起独立缩放，上限 " + (isCost ? AILimitStyle.money(maximum) : "\(Int(maximum)) 个"))
        return host
      }
      let chart = AIUsageLayout.stack(columns, vertical: false, equal: true)
      chart.spacing = PaxTheme.Spacing.xs / 2
      chart.heightAnchor.constraint(equalToConstant: 20).isActive = true
      chart.setAccessibilityLabel(isCost ? "最近7日官方输入费，独立缩放" : "最近7日活跃主会话，独立缩放")
      evidence = chart
    }
    let column = AIUsageLayout.stack([label, amount, evidence])
    column.spacing = PaxTheme.Spacing.xs
    return column
  }

  static func value(_ day: AILimitDay, isCost: Bool) -> Double? {
    if !isCost { return Double(day.sessions) }
    return day.requests > 0 && day.pricedRequests == 0 ? nil : day.input.official
  }
  static func period(_ from: Date, _ to: Date) -> String {
    dayTitle(from) + "–" + dayTitle(to)
  }
  private static func dayTitle(_ date: Date) -> String {
    AIUsageFormatting.monthDay(date).replacingOccurrences(of: "-", with: "/")
  }
  private static func lines(_ day: AILimitDay) -> [String] {
    guard day.available else { return ["数据不完整或超出保留期"] }
    var lines = [
      "活跃主会话  \(day.sessions) 个",
      "输入Token  " + (day.inputTokens.map(AIUsageFormatting.count) ?? "—"),
      "官方输入费  " + AILimitStyle.money(value(day, isCost: true), precision: 2),
    ]
    if day.pricedRequests < day.requests {
      lines.append("已定价小计 · \(day.requests - day.pricedRequests) 次未定价")
    }
    return lines
  }
  private static func dayDetail(_ day: AILimitDay, through observed: Date) -> NSView {
    var title = Self.dayTitle(day.at)
    if Calendar.current.isDate(day.at, inSameDayAs: observed) {
      title += " · 截至" + AIUsageFormatting.recentUse(observed, now: observed)
    }
    return AILimitReadout.explanation(title, lines(day).joined(separator: "\n"))
  }
  private static func summaryDetail(
    _ summary: AILimitSummary, complete: Bool, singleDay: Bool
  ) -> NSView {
    let rate =
      complete
      ? summary.inputTokens.flatMap { tokens in
        tokens > 0 ? summary.input.official / Double(tokens) * 1_000_000 : nil
      } : nil
    let tokenText = summary.inputTokens.map(AIUsageFormatting.tokens) ?? "—"
    let scope =
      summary.days.first.flatMap { first in
        summary.days.last.map { first.at == $0.at ? dayTitle(first.at) : period(first.at, $0.at) }
      } ?? "所选范围"
    let date = PaxText.label(scope, font: PaxTheme.Typography.label(size: 11), secondary: true)
    let header = AIUsageLayout.stack(
      [AIUsageLayout.heading("会话与开销"), NSView(), date], vertical: false)
    let price = PaxText.label(
      AILimitStyle.money(rate), font: PaxTheme.Typography.metric(size: 28, weight: .light))
    price.textColor = rate == nil ? AILimitStyle.dim : PaxTheme.DashboardColor.redLabel
    let unit = AIUsageLayout.stack([price, detailNote("每百万输入 Token")])
    unit.spacing = PaxTheme.Spacing.xs
    let token = PaxText.label(
      tokenText, font: PaxTheme.Typography.metric(size: 28, weight: .light))
    token.textColor =
      summary.inputTokens == nil ? AILimitStyle.dim : PaxTheme.DashboardColor.greenLabel
    token.alignment = .right
    token.toolTip = summary.inputTokens.map(AIUsageFormatting.count)
    let caption = detailNote("输入 Token")
    caption.alignment = .right
    let tokens = AIUsageLayout.stack([token, caption])
    tokens.spacing = PaxTheme.Spacing.xs
    let metrics = AIUsageLayout.stack([unit, tokens], vertical: false, equal: true)
    metrics.spacing = PaxTheme.Spacing.regular
    var items: [NSView] = [header, metrics]
    if rate == nil {
      items.append(detailNote("单位费用暂不可算"))
    }
    if singleDay, summary.comparison == nil {
      items.append(detailNote("上日暂无可比数据"))
    }
    let column = AIUsageLayout.stack(items)
    column.spacing = PaxTheme.Spacing.medium
    column.widthAnchor.constraint(
      equalToConstant: PaxTheme.AISession.selectorWidth + PaxTheme.Spacing.regular * 2
    ).isActive = true
    column.setAccessibilityIdentifier("ai-session-usage-cost-detail")
    column.setAccessibilityHelp("输入费用和 Token 均含缓存与子代理，不含输出。")
    return column
  }

  private static func detailNote(_ text: String) -> NSTextField {
    PaxText.label(text, font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  }
}

/// One day contributes only a bar; dates and values stay in the hover readout.
@MainActor private final class AILimitDayColumn: NSView {
  private let day: AILimitDay
  private let isCost: Bool
  private let maximum: Double
  override var isFlipped: Bool { true }

  init(day: AILimitDay, isCost: Bool, maximum: Double) {
    self.day = day
    self.isCost = isCost
    self.maximum = maximum
    super.init(frame: .zero)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func draw(_ dirtyRect: NSRect) {
    guard bounds.intersects(visibleRect), day.available,
      let value = AILimitActivityView.value(day, isCost: isCost)
    else { return }
    let width = min(12, max(1, bounds.width - 2))
    let height = CGFloat(value / maximum) * bounds.height
    (isCost ? PaxTheme.DashboardColor.green : PaxTheme.Color.accent).withAlphaComponent(0.8)
      .setFill()
    NSBezierPath(
      roundedRect: .init(
        x: (bounds.width - width) / 2, y: bounds.height - height, width: width, height: height),
      xRadius: 2, yRadius: 2
    ).fill()
  }
  override func setFrameSize(_ newSize: NSSize) {
    super.setFrameSize(newSize)
    needsDisplay = true
  }
}
