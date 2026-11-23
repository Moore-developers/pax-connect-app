import AppKit
import PaxCore
import PaxUI

@MainActor enum WorkbenchConnectionGroupEvidence {
  private typealias Layout = WorkbenchConnectionReadoutLayout

  static func latestLatencyText(_ group: TrafficConnectionGroup) -> String {
    guard let latest = group.recentResponses.last else { return "—" }
    return PaxTimeSeriesStyle.latencyText(Double(latest.milliseconds))
  }

  static func latency(
    _ group: TrafficConnectionGroup, context: PresentationContext,
    onHoverLatency: @escaping (String?) -> Void
  ) -> NSView {
    let theme = PaxTheme.TrafficDetail.self
    let responses = group.recentResponses
    let missing =
      group.connections >= group.validLatencyCount ? group.connections - group.validLatencyCount : 0
    let defaultMeta: String = {
      guard !responses.isEmpty else { return "" }
      let base = context.text(
        "connection_groups.latency_compact",
        ["shown": String(responses.count), "total": String(group.validLatencyCount)])
      guard missing > 0 else { return base }
      return base + " · "
        + context.text("connection_groups.latency_unrecorded_short", ["count": String(missing)])
    }()

    let header = Layout.sectionHeader(
      context.text("connection_groups.latency"),
      trailing: defaultMeta.isEmpty ? nil : defaultMeta)
    let metaLabel = header.trailingLabel

    guard !responses.isEmpty else {
      let empty = PaxText.label(
        context.text("connection_groups.latency_missing"),
        font: PaxTheme.Typography.label(size: theme.hoverMetaFont), secondary: true, wrapping: true)
      empty.setAccessibilityIdentifier("connection-group-latency")
      return Layout.stack([header.view, empty], gap: 6)
    }

    let from = responses.first?.startedAt ?? group.lastAt
    let until = max(group.lastAt, responses.last?.startedAt ?? group.lastAt)
    let series = workbenchResponseSeries(
      responses, from: from, asOf: until, limit: 60, context: context)
    var style = PaxTimeSeriesStyle.latency(title: context.text("connection_groups.latency"))
    style.copy = timeSeriesCopy(context)
    let chart = PaxTimeSeriesChart(series: series, style: style, density: .regular)
    chart.fadesAreaFill = true
    header.view.heightAnchor.constraint(equalToConstant: ceil(header.view.fittingSize.height))
      .isActive = true
    chart.heightAnchor.constraint(greaterThanOrEqualToConstant: theme.hoverLatencyChartHeight)
      .isActive = true
    chart.setAccessibilityIdentifier("connection-group-latency")
    chart.onSelect = { selection in
      if let selection {
        onHoverLatency(PaxTimeSeriesStyle.latencyText(selection.value))
        metaLabel?.stringValue = context.text(
          "connection_groups.observed_at", ["time": selection.timeText])
      } else {
        onHoverLatency(nil)
        metaLabel?.stringValue = defaultMeta
      }
    }
    return Layout.stack([header.view, chart], gap: 4)
  }

  static func exits(_ group: TrafficConnectionGroup, context: PresentationContext) -> NSView {
    let header = Layout.sectionHeader(
      context.text("connection_groups.exit_distribution"),
      trailing: context.text("connection_groups.count_share")
    ).view
    guard !group.exits.isEmpty else {
      let empty = PaxText.label(
        context.text("connection_groups.unrecorded"),
        font: PaxTheme.Typography.label(size: PaxTheme.TrafficDetail.hoverMetaFont),
        secondary: true)
      return Layout.stack([header, empty], gap: 6)
    }
    let total = max(group.connections, group.exits.reduce(0) { $0 + $1.connections })
    let rows = group.exits.map { value in
      let name = TrafficFormatting.actualConnection(
        direction: value.direction, route: value.routeName, context: context)
      return Layout.exitShareRow(
        name: name, count: value.connections, total: total, tone: exitTone(value.direction))
    }
    return Layout.stack([header] + rows, gap: 6)
  }

  static func rules(_ group: TrafficConnectionGroup, context: PresentationContext, width: CGFloat)
    -> NSView
  {
    let theme = PaxTheme.TrafficDetail.self
    let header = Layout.sectionHeader(
      context.text("connection_groups.rule_basis"),
      trailing: context.text("connection_groups.count_short")
    ).view
    guard !group.rules.isEmpty else {
      let empty = PaxText.label(
        context.text("connection_groups.unrecorded"),
        font: PaxTheme.Typography.label(size: theme.hoverMetaFont), secondary: true)
      return Layout.stack([header, empty], gap: 6)
    }

    var views: [NSView] = [header]
    let listStack = NSStackView()
    listStack.orientation = .vertical
    listStack.alignment = .leading
    listStack.spacing = 0
    for (index, rule) in group.rules.enumerated() {
      if index > 0 {
        let divider = Layout.hairline()
        listStack.addArrangedSubview(divider)
        divider.widthAnchor.constraint(equalTo: listStack.widthAnchor).isActive = true
      }
      let row = ruleRow(rule, context: context, width: width)
      listStack.addArrangedSubview(row)
      row.widthAnchor.constraint(equalTo: listStack.widthAnchor).isActive = true
    }
    views.append(listStack)
    return Layout.stack(views, gap: 4)
  }

  private static func exitTone(_ direction: String) -> Layout.Tone {
    switch direction {
    case "proxy", "代理": return .green
    case "direct", "直连": return .amber
    case "blocked", "已阻断": return .red
    default: return .neutral
    }
  }

  private static func ruleRow(
    _ rule: TrafficBasis, context: PresentationContext, width: CGFloat
  ) -> NSView {
    let theme = PaxTheme.TrafficDetail.self
    let missing = rule.rule.isEmpty || rule.rule == "未记录"
    let fallback = rule.rule == "final" || rule.rule == "未命中规则"
    let title =
      missing
      ? context.text("connection_groups.unrecorded")
      : fallback
        ? context.text("connection_groups.unmatched")
        : TrafficRuleFormatting.package(rule: rule.rule, source: rule.source, context: context)
    let condition =
      missing || fallback
      ? ""
      : TrafficRuleFormatting.condition(rule: rule.rule, source: rule.source, context: context)
    let recorded = missing || fallback ? "" : TrafficRuleFormatting.recordedRuleText(rule.rule)
    let badgeText: String = {
      guard !missing && !fallback else { return "" }
      if !recorded.isEmpty && recorded != title { return recorded }
      if !condition.isEmpty && condition != title { return condition }
      return ""
    }()
    let source = rule.source == "Pax 包：" + title ? "" : rule.source
    let subtitle: String = {
      if fallback { return context.text("connection_groups.unmatched_detail") }
      guard !missing else { return "" }
      return [condition, source]
        .filter { !$0.isEmpty && $0 != title && $0 != badgeText }
        .joined(separator: " · ")
    }()

    let countText = String(rule.connections)
    let countFont = PaxTheme.Typography.metric(size: theme.hoverBodyFont, weight: .semibold)
    let countLabel = PaxText.label(countText, font: countFont)
    countLabel.alignment = .right
    countLabel.setContentHuggingPriority(.required, for: .horizontal)
    countLabel.setContentCompressionResistancePriority(.required, for: .horizontal)
    let countWidth = max(
      28, ceil((countText as NSString).size(withAttributes: [.font: countFont]).width))
    let leftWidth = max(120, width - countWidth - 12)

    let titleFont = PaxTheme.Typography.label(size: theme.hoverBodyFont, weight: .medium)
    let titleLabel = PaxText.label(title, font: titleFont, wrapping: true)
    titleLabel.lineBreakMode = .byWordWrapping

    var leftItems: [NSView] = []
    if !badgeText.isEmpty {
      let titleSize = (title as NSString).size(withAttributes: [.font: titleFont]).width
      let badgeFont = NSFont.monospacedSystemFont(ofSize: theme.hoverCodeFont, weight: .regular)
      let badgeNatural = (badgeText as NSString).size(withAttributes: [.font: badgeFont]).width + 12
      if titleSize + 8 + badgeNatural <= leftWidth {
        let badge = Layout.codeBadge(badgeText, maxWidth: leftWidth - titleSize - 8)
        let inline = NSView()
        titleLabel.translatesAutoresizingMaskIntoConstraints = false
        badge.translatesAutoresizingMaskIntoConstraints = false
        titleLabel.setContentHuggingPriority(.required, for: .horizontal)
        inline.addSubview(titleLabel)
        inline.addSubview(badge)
        NSLayoutConstraint.activate([
          titleLabel.leadingAnchor.constraint(equalTo: inline.leadingAnchor),
          titleLabel.centerYAnchor.constraint(equalTo: inline.centerYAnchor),
          badge.leadingAnchor.constraint(equalTo: titleLabel.trailingAnchor, constant: 8),
          badge.trailingAnchor.constraint(lessThanOrEqualTo: inline.trailingAnchor),
          badge.topAnchor.constraint(equalTo: inline.topAnchor),
          badge.bottomAnchor.constraint(equalTo: inline.bottomAnchor),
        ])
        leftItems.append(inline)
      } else {
        leftItems.append(titleLabel)
        let badge = Layout.codeBadge(badgeText, maxWidth: leftWidth)
        let badgeRow = NSView()
        badge.translatesAutoresizingMaskIntoConstraints = false
        badgeRow.addSubview(badge)
        NSLayoutConstraint.activate([
          badge.leadingAnchor.constraint(equalTo: badgeRow.leadingAnchor),
          badge.trailingAnchor.constraint(lessThanOrEqualTo: badgeRow.trailingAnchor),
          badge.topAnchor.constraint(equalTo: badgeRow.topAnchor),
          badge.bottomAnchor.constraint(equalTo: badgeRow.bottomAnchor),
        ])
        leftItems.append(badgeRow)
      }
    } else {
      leftItems.append(titleLabel)
    }

    if !subtitle.isEmpty {
      let subLabel = PaxText.label(
        subtitle, font: PaxTheme.Typography.label(size: theme.hoverMetaFont), secondary: true,
        wrapping: true)
      subLabel.lineBreakMode = .byWordWrapping
      leftItems.append(subLabel)
    }

    let leftStack = Layout.stack(leftItems, gap: 4)
    let row = NSView()
    leftStack.translatesAutoresizingMaskIntoConstraints = false
    countLabel.translatesAutoresizingMaskIntoConstraints = false
    row.addSubview(leftStack)
    row.addSubview(countLabel)
    NSLayoutConstraint.activate([
      leftStack.leadingAnchor.constraint(equalTo: row.leadingAnchor),
      leftStack.topAnchor.constraint(equalTo: row.topAnchor, constant: 7),
      leftStack.bottomAnchor.constraint(equalTo: row.bottomAnchor, constant: -7),
      leftStack.trailingAnchor.constraint(equalTo: countLabel.leadingAnchor, constant: -12),
      countLabel.trailingAnchor.constraint(equalTo: row.trailingAnchor),
      countLabel.topAnchor.constraint(equalTo: row.topAnchor, constant: 7),
    ])
    return row
  }
}
