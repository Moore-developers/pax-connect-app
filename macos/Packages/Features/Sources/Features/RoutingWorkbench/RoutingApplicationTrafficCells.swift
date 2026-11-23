import AppKit
import PaxCore
import PaxUI

@MainActor func applicationExitCell(
  _ row: PaxCore.TrafficRow, context: PresentationContext, badges: Bool = false
)
  -> NSView
{
  let exits = row.exits.filter { $0.direction != "blocked" }
  let direct = exits.first { $0.direction == "direct" }
  let proxy = exits.first { $0.direction == "proxy" }
  let primary = direct == nil ? exits.first : proxy ?? exits.first
  let second =
    exits.first { $0.direction == "direct" && $0 != primary }
    ?? exits.first { $0 != primary }
  let shown = [primary, second].compactMap { $0 }
  let names = shown.map {
    TrafficFormatting.actualConnection(
      direction: $0.direction, route: $0.routeName, context: context)
  }
  let evidence = exits.map {
    TrafficFormatting.actualConnection(
      direction: $0.direction, route: $0.routeName, context: context)
  }
  return applicationExitCell(
    names: names, extra: exits.count - shown.count, blocked: row.blockedCount,
    evidence: evidence, context: context, directions: badges ? shown.map(\.direction) : [])
}

@MainActor func applicationExitCell(
  _ row: RoutingWorkbenchStore.TrafficRow, context: PresentationContext, badges: Bool = false
) -> NSView {
  let names = row.route.split(separator: "\n").map(String.init)
  return applicationExitCell(
    names: Array(names.prefix(2)), extra: max(0, names.count - 2), blocked: 0,
    evidence: names, context: context,
    directions: badges ? names.map { $0 == context.text("common.direct") ? "direct" : "proxy" } : []
  )
}

@MainActor private func applicationExitCell(
  names: [String], extra: Int, blocked: UInt64, evidence: [String], context: PresentationContext,
  directions: [String]
) -> NSView {
  var lines: [NSView] = []
  var tooltip = evidence
  if !names.isEmpty || blocked == 0 {
    let names = names.isEmpty ? [context.text("traffic.direction_missing")] : names
    let font = PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyFont)
    let line = NSStackView()
    line.orientation = .horizontal
    line.alignment = directions.isEmpty ? .firstBaseline : .centerY
    line.spacing = directions.isEmpty ? 0 : 4
    let separator = context.text("traffic_details.application_exit_separator")
    for (index, name) in names.enumerated() {
      if index > 0 && directions.isEmpty {
        line.addArrangedSubview(PaxText.label(separator, font: font))
      }
      if directions.indices.contains(index) {
        line.addArrangedSubview(
          overviewExitBadge(name, direction: directions[index], fillsCell: false))
        continue
      }
      let label = PaxText.label(name, font: font)
      label.lineBreakMode = .byTruncatingTail
      label.setContentCompressionResistancePriority(
        index == 0 ? .defaultLow : .defaultHigh, for: .horizontal)
      line.addArrangedSubview(label)
    }
    if extra > 0 {
      let label = PaxText.label(" +\(extra)", font: font, secondary: true)
      label.setContentCompressionResistancePriority(.required, for: .horizontal)
      line.addArrangedSubview(label)
    }
    lines.append(line)
  }
  if blocked > 0 {
    let count = String(blocked.formatted())
    let text = context.text("traffic_details.application_blocked_count", ["count": count])
    let font = PaxTheme.Typography.caption()
    let formatted = NSMutableAttributedString(
      string: text, attributes: [.font: font, .foregroundColor: PaxTheme.Color.textSecondary])
    let range = (text as NSString).range(of: count)
    if range.location != NSNotFound {
      formatted.addAttribute(.foregroundColor, value: PaxTheme.Color.error, range: range)
    }
    let label = NSTextField(labelWithAttributedString: formatted)
    label.lineBreakMode = .byTruncatingTail
    lines.append(label)
    tooltip.append(text)
  }
  return trafficSummaryCell(lines, tooltip: tooltip.joined(separator: "\n"))
}

@MainActor func recordedApplicationRules(_ row: PaxCore.TrafficRow) -> [RoutingTrafficCellKey.Rule]
{
  var seen = Set<RoutingTrafficCellKey.Rule>()
  return row.bases.compactMap { basis in
    guard !basis.rule.isEmpty, basis.rule != "未记录" else { return nil }
    let rule = RoutingTrafficCellKey.Rule(rule: basis.rule, source: basis.source)
    return seen.insert(rule).inserted ? rule : nil
  }
}

@MainActor func applicationRuleCell(
  _ row: PaxCore.TrafficRow, context: PresentationContext, leading: Bool = false
)
  -> NSView
{
  let rules = recordedApplicationRules(row)
  let match = row.latestMatch
  let title: String
  if match == nil, rules.count == 1, let rule = rules.first {
    title =
      TrafficRuleFormatting.package(rule: rule.rule, source: rule.source, context: context)
      + " · "
      + TrafficRuleFormatting.condition(rule: rule.rule, source: rule.source, context: context)
  } else {
    title = TrafficRuleFormatting.latestTitle(match, context: context)
  }
  let label = PaxText.label(title, font: PaxTheme.Typography.caption())
  label.lineBreakMode = .byTruncatingTail
  label.maximumNumberOfLines = 1
  label.alignment = leading ? .left : .center
  if leading { label.textColor = PaxTheme.Color.textSecondary }
  let evidence = rules.map {
    TrafficRuleFormatting.package(rule: $0.rule, source: $0.source, context: context)
      + " · " + TrafficRuleFormatting.condition(rule: $0.rule, source: $0.source, context: context)
  }
  let tooltip = [TrafficRuleFormatting.latestEvidence(match, context: context)] + evidence
  label.toolTip = tooltip.joined(separator: "\n")
  return label
}

@MainActor private func trafficSummaryCell(_ lines: [NSView], tooltip: String)
  -> NSView
{
  let stack = NSStackView(views: lines)
  stack.orientation = .vertical
  stack.alignment = .leading
  stack.spacing = 2
  stack.toolTip = tooltip
  for line in lines {
    stack.setVisibilityPriority(.mustHold, for: line)
    line.toolTip = tooltip
    line.widthAnchor.constraint(lessThanOrEqualTo: stack.widthAnchor).isActive = true
    for child in line.subviews { child.toolTip = tooltip }
  }
  let height =
    CGFloat(lines.count) * PaxTheme.RTT.textLine
    + CGFloat(max(0, lines.count - 1)) * stack.spacing
  let view = ApplicationTrafficSummaryView(height: height)
  view.toolTip = tooltip
  stack.translatesAutoresizingMaskIntoConstraints = false
  view.addSubview(stack)
  NSLayoutConstraint.activate([
    stack.leadingAnchor.constraint(equalTo: view.leadingAnchor),
    stack.trailingAnchor.constraint(equalTo: view.trailingAnchor),
    stack.centerYAnchor.constraint(equalTo: view.centerYAnchor),
    stack.topAnchor.constraint(greaterThanOrEqualTo: view.topAnchor),
    stack.bottomAnchor.constraint(lessThanOrEqualTo: view.bottomAnchor),
  ])
  return view
}

@MainActor private final class ApplicationTrafficSummaryView: NSView {
  private let height: CGFloat

  init(height: CGFloat) {
    self.height = height
    super.init(frame: .zero)
  }

  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  override var intrinsicContentSize: NSSize {
    NSSize(width: NSView.noIntrinsicMetric, height: height)
  }
}

@MainActor func overviewExitBadge(
  _ name: String, direction: String, fillsCell: Bool = true
)
  -> NSView
{
  guard direction == "proxy" || direction == "direct" else {
    return PaxText.label(name, secondary: true)
  }
  let badge = PaxToneBadge("● " + name, tone: direction == "proxy" ? .success : .warning)
  badge.leadingDotScale = 0.5
  badge.setContentHuggingPriority(.required, for: .horizontal)
  badge.setContentCompressionResistancePriority(
    direction == "direct" ? .required : .defaultLow, for: .horizontal)
  if !fillsCell { return badge }
  let holder = NSStackView(views: [badge, NSView()])
  holder.spacing = 0
  holder.alignment = .centerY
  holder.toolTip = name
  return holder
}
