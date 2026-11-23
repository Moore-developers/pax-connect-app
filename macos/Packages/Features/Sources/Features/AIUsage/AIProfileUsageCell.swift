import AppKit
import PaxCore
import PaxUI

/// One period inside a workbench row. Every number comes from the same scope.
@MainActor
final class AIProfileUsageCell: NSView {
  private let amount = PaxText.tableLabel("", numeric: true, identity: true)
  private let activity = PaxText.tableLabel("", secondary: true, numeric: true)
  private let costs = PaxText.tableLabel("", secondary: true, numeric: true)

  init(alignment: NSTextAlignment = .left) {
    super.init(frame: .zero)
    let labels = [amount, activity, costs]
    let stack = AIUsageLayout.stack(labels)
    stack.spacing = PaxTheme.Spacing.tight
    for label in labels {
      label.alignment = alignment
      label.lineBreakMode = .byTruncatingTail
      label.setContentCompressionResistancePriority(.required, for: .vertical)
    }
    stack.translatesAutoresizingMaskIntoConstraints = false
    addSubview(stack)
    NSLayoutConstraint.activate([
      stack.leadingAnchor.constraint(equalTo: leadingAnchor),
      stack.trailingAnchor.constraint(equalTo: trailingAnchor),
      stack.centerYAnchor.constraint(equalTo: centerYAnchor),
      stack.heightAnchor.constraint(lessThanOrEqualTo: heightAnchor),
    ])
  }

  @available(*, unavailable) required init(coder: NSCoder) { fatalError("代码构建") }

  func apply(_ sum: AIUsageSummary, ready: Bool, showFees: Bool, context: PresentationContext) {
    let hasUsage = ready && sum.count > 0
    amount.stringValue =
      hasUsage
      ? AIUsageFormatting.tokens(sum.tokens) + " Token"
      : ready ? context.text("ai_projection.c41b0972aa") : "—"
    amount.font =
      hasUsage
      ? PaxTheme.Typography.metric(size: PaxTheme.Table.font + 2) : PaxTheme.Typography.caption()
    amount.textColor = hasUsage ? PaxTheme.Table.text : PaxTheme.Table.muted
    amount.toolTip = amount.stringValue
    activity.isHidden = !hasUsage
    if hasUsage {
      let count = AIUsageFormatting.tokens(sum.count)
      let rate = AIUsageFormatting.percent(sum.cache)
      activity.stringValue = context.text("ai_workbench.activity", ["count": count, "rate": rate])
      let requests = context.text("ai_workbench.requests", ["count": sum.count.formatted()])
      let cache = context.text("ai_workbench.cache", ["rate": rate])
      activity.toolTip = requests + " · " + cache
      activity.setAccessibilityLabel(activity.toolTip)
    }
    costs.isHidden = !showFees || !hasUsage
    if showFees && hasUsage {
      AIUsageCostDisplay.apply(
        costs, official: sum.officialCostLabel,
        channel: sum.hasChannelEstimate ? sum.costLabel : nil,
        context: context)
    }
  }
}
