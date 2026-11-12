import AppKit
import PaxCore
import PaxUI

/// One stat card: small secondary title over a coloured value.
@MainActor
final class AIStatCardView: NSView {
  let title = AIUsageLayout.label("", secondary: true)
  let value = PaxText.label("", font: PaxTheme.Typography.metric(size: 18, weight: .semibold))
  init() {
    super.init(frame: .zero)
    let card = PaxCard(
      insets: .init(
        top: PaxTheme.Spacing.tight, left: PaxTheme.Spacing.regular,
        bottom: PaxTheme.Spacing.tight, right: PaxTheme.Spacing.regular))
    let stack = AIUsageLayout.stack([title, value])
    stack.spacing = PaxTheme.Spacing.xs
    for field in [title, value] { field.lineBreakMode = .byClipping }
    value.setContentCompressionResistancePriority(.required, for: .horizontal)
    card.contentView = stack
    card.translatesAutoresizingMaskIntoConstraints = false
    addSubview(card)
    NSLayoutConstraint.activate([
      card.leadingAnchor.constraint(equalTo: leadingAnchor),
      card.trailingAnchor.constraint(equalTo: trailingAnchor),
      card.topAnchor.constraint(equalTo: topAnchor),
      card.bottomAnchor.constraint(equalTo: bottomAnchor),
      card.heightAnchor.constraint(equalTo: stack.heightAnchor, constant: PaxTheme.Spacing.regular),
    ])
    setAccessibilityElement(true)
    setAccessibilityRole(.group)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  func apply(_ item: AIUsageLogFormat.Stat) {
    title.stringValue = item.title
    if let official = item.officialCost, let channel = item.channelCost {
      AIUsageCostDisplay.apply(value, official: official, channel: channel)
    } else {
      value.attributedStringValue = NSAttributedString(
        string: item.value,
        attributes: [.font: value.font ?? PaxTheme.Typography.time, .foregroundColor: item.color])
    }
    toolTip = item.title + " " + item.value
    setAccessibilityLabel(item.title + " " + item.value)
  }
}

/// Row of equal-width stat cards above the log table (筛选结果 / Token / 费用 / 缓存率 / 成功).
@MainActor
final class AIUsageLogSummaryView: NSStackView {
  let cards = (0..<5).map { _ in AIStatCardView() }
  init() {
    super.init(frame: .zero)
    orientation = .horizontal
    distribution = .fillEqually
    alignment = .top
    spacing = PaxTheme.Spacing.tight
    for card in cards { addArrangedSubview(card) }
    setAccessibilityIdentifier("ai-log-summary")
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  func apply(period: String, totals: AIUsageSummary) {
    for (card, item) in zip(cards, AIUsageLogFormat.stats(period: period, totals: totals)) {
      card.apply(item)
    }
  }
}
