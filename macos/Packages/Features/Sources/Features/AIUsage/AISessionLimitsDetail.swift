import AppKit
import PaxCore
import PaxUI

/// A short hover answers where the input cost went; the bar has no click-to-pin action.
@MainActor enum AILimitRowReadout {
  static func view(_ row: AILimitListRow, context: PresentationContext = .chinese) -> NSView {
    let title = AIUsageLayout.heading(row.title)
    title.lineBreakMode = .byTruncatingTail
    title.toolTip = row.title
    var items: [NSView] = [title]
    if row.lines != nil {
      items += [
        headline(row, context: context), distribution(row, context: context),
        legend(row, context: context), AIUsageLayout.separator(),
      ]
      items.append(comparison(row, context: context))
    } else {
      items.append(
        line(
          context.text("session_limits.fe4606df06"),
          AILimitStyle.money(row.input.official, precision: 2)))
      items.append(note(context.text("session_limits.34c2069f05")))
    }
    if row.unpriced > 0 {
      items.append(
        note(context.text("session_limits.04d7039876", ["arg0": String(describing: row.unpriced)])))
    }
    let column = AIUsageLayout.stack(items)
    column.spacing = PaxTheme.Spacing.medium
    column.widthAnchor.constraint(equalToConstant: PaxTheme.AISession.selectorWidth).isActive = true
    column.setAccessibilityIdentifier("ai-session-limit-hover")
    return column
  }

  private static func headline(_ row: AILimitListRow, context: PresentationContext = .chinese)
    -> NSView
  {
    let share = row.input.official > 0 ? "\(row.percents[2])%" : "—"
    let value = PaxText.label(share, font: PaxTheme.Typography.metric(size: 32, weight: .light))
    let main = AIUsageLayout.stack([value, note(context.text("session_limits.735a68d800"))])
    main.widthAnchor.constraint(greaterThanOrEqualToConstant: PaxTheme.Spacing.regular * 10)
      .isActive = true
    let amount = PaxText.label(
      AILimitStyle.money(row.bands[2].money.official, precision: 2),
      font: PaxTheme.Typography.tableValue)
    amount.alignment = .right
    let count = PaxText.label(
      context.text(
        "session_limits.8a9318fa01", ["arg0": String(describing: row.bands[2].requests)]),
      font: PaxTheme.Typography.caption(), secondary: true)
    count.alignment = .right
    count.setContentCompressionResistancePriority(.required, for: .horizontal)
    let side = AIUsageLayout.stack([amount, count])
    return AIUsageLayout.stack([main, NSView(), side], vertical: false)
  }

  private static func distribution(_ row: AILimitListRow, context: PresentationContext = .chinese)
    -> NSView
  {
    let bar = PaxShareBar(
      segments: (0..<3).map {
        .init(AILimitStyle.bandNames(context: context)[$0], value: row.bands[$0].money.official)
      }, colors: AILimitStyle.bandColors, striped: [2])
    bar.heightAnchor.constraint(equalToConstant: PaxTheme.Spacing.tight).isActive = true
    bar.setAccessibilityLabel(context.text("session_limits.41516f84b4"))
    bar.setAccessibilityValue(
      (0..<3).map { AILimitStyle.bandNames(context: context)[$0] + " \(row.percents[$0])%" }.joined(
        separator: "，"))
    return bar
  }

  private static func legend(_ row: AILimitListRow, context: PresentationContext = .chinese)
    -> NSView
  {
    let legend = PaxSeriesLegend()
    legend.items = (0..<3).map {
      .init(
        AILimitStyle.bandNames(context: context)[$0] + " \(row.percents[$0])%",
        color: AILimitStyle.bandColors[$0])
    }
    return legend
  }

  private static func comparison(_ row: AILimitListRow, context: PresentationContext = .chinese)
    -> NSView
  {
    let safe = row.bands[0].average?.official
    let over = row.bands[2].average?.official
    let multiple =
      safe == nil
      ? context.text("session_limits.70723ec02e") : context.text("session_limits.4808f83c53")
    var items: [NSView] = [
      line(
        context.text("session_limits.60c50a593d"),
        multiple + AILimitStyle.multipleText(row.multiple))
    ]
    let scale = max(safe ?? 0, over ?? 0)
    let edges =
      row.modelLines.count > 1 ? ["", "", ""] : AILimitStyle.edges(row.lines, context: context)
    for (index, amount) in [(0, safe), (2, over)] {
      let label =
        AILimitStyle.bandNames(context: context)[index]
        + (edges[index].isEmpty ? "" : " " + edges[index])
      items.append(
        line(
          label,
          AILimitStyle.money(amount)
            + (amount == nil ? "" : context.text("session_limits.3f947bf4c7"))))
      if let amount {
        let bar = PaxShareBar(
          segments: [.init(label, value: amount)], colors: [AILimitStyle.bandColors[index]],
          total: scale)
        bar.heightAnchor.constraint(equalToConstant: PaxTheme.Spacing.tight / 2).isActive = true
        items.append(bar)
      }
    }
    items.append(note(context.text("session_limits.59eacd95bf")))
    if safe == nil {
      items.append(
        note(
          row.multiple == .dash
            ? context.text("session_limits.0da663c5db") : context.text("session_limits.c189879528"))
      )
    }
    let column = AIUsageLayout.stack(items)
    column.spacing = PaxTheme.Spacing.tight
    return column
  }

  private static func line(_ label: String, _ value: String) -> NSView {
    let amount = PaxText.label(value, font: PaxTheme.Typography.time)
    amount.alignment = .right
    return AIUsageLayout.stack([note(label), NSView(), amount], vertical: false)
  }

  private static func note(_ text: String) -> NSTextField {
    PaxText.label(text, font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  }
}
