import AppKit
import PaxCore
import PaxUI

/// A short hover answers where the input cost went; the bar has no click-to-pin action.
@MainActor enum AILimitRowReadout {
  static func view(_ row: AILimitListRow) -> NSView {
    let title = AIUsageLayout.heading(row.title)
    title.lineBreakMode = .byTruncatingTail
    title.toolTip = row.title
    var items: [NSView] = [title]
    if row.lines != nil {
      items += [headline(row), distribution(row), legend(row), AIUsageLayout.separator()]
      items.append(comparison(row))
    } else {
      items.append(
        line("输入费用 · 官方", AILimitStyle.money(row.input.official, precision: 2)))
      items.append(note("未设上下文上限，不比较安全与超限。"))
    }
    if row.unpriced > 0 {
      items.append(note("另有 \(row.unpriced) 次未定价，不计入费用与均值"))
    }
    let column = AIUsageLayout.stack(items)
    column.spacing = PaxTheme.Spacing.medium
    column.widthAnchor.constraint(equalToConstant: PaxTheme.AISession.selectorWidth).isActive = true
    column.setAccessibilityIdentifier("ai-session-limit-hover")
    return column
  }

  private static func headline(_ row: AILimitListRow) -> NSView {
    let share = row.input.official > 0 ? "\(row.percents[2])%" : "—"
    let value = PaxText.label(share, font: PaxTheme.Typography.metric(size: 32, weight: .light))
    let main = AIUsageLayout.stack([value, note("输入费用落在超限区间")])
    main.widthAnchor.constraint(greaterThanOrEqualToConstant: PaxTheme.Spacing.regular * 10)
      .isActive = true
    let amount = PaxText.label(
      AILimitStyle.money(row.bands[2].money.official, precision: 2),
      font: PaxTheme.Typography.tableValue)
    amount.alignment = .right
    let count = note("\(row.bands[2].requests) 次请求")
    count.alignment = .right
    let side = AIUsageLayout.stack([amount, count])
    return AIUsageLayout.stack([main, NSView(), side], vertical: false)
  }

  private static func distribution(_ row: AILimitListRow) -> NSView {
    let bar = PaxShareBar(
      segments: (0..<3).map {
        .init(AILimitStyle.bandNames[$0], value: row.bands[$0].money.official)
      }, colors: AILimitStyle.bandColors, striped: [2])
    bar.heightAnchor.constraint(equalToConstant: PaxTheme.Spacing.tight).isActive = true
    bar.setAccessibilityLabel("官方输入费用占比")
    bar.setAccessibilityValue(
      (0..<3).map { AILimitStyle.bandNames[$0] + " \(row.percents[$0])%" }.joined(separator: "，"))
    return bar
  }

  private static func legend(_ row: AILimitListRow) -> NSView {
    let legend = PaxSeriesLegend()
    legend.items = (0..<3).map {
      .init(
        AILimitStyle.bandNames[$0] + " \(row.percents[$0])%", color: AILimitStyle.bandColors[$0])
    }
    return legend
  }

  private static func comparison(_ row: AILimitListRow) -> NSView {
    let safe = row.bands[0].average?.official
    let over = row.bands[2].average?.official
    let multiple = safe == nil ? "参考 " : "超限 / 安全 "
    var items: [NSView] = [line("单次输入均费 · 官方", multiple + AILimitStyle.multipleText(row.multiple))]
    let scale = max(safe ?? 0, over ?? 0)
    let edges = row.modelLines.count > 1 ? ["", "", ""] : AILimitStyle.edges(row.lines)
    for (index, amount) in [(0, safe), (2, over)] {
      let label = AILimitStyle.bandNames[index] + (edges[index].isEmpty ? "" : " " + edges[index])
      items.append(line(label, AILimitStyle.money(amount) + (amount == nil ? "" : " / 次")))
      if let amount {
        let bar = PaxShareBar(
          segments: [.init(label, value: amount)], colors: [AILimitStyle.bandColors[index]],
          total: scale)
        bar.heightAnchor.constraint(equalToConstant: PaxTheme.Spacing.tight / 2).isActive = true
        items.append(bar)
      }
    }
    items.append(note("均费为实际值；DeepSeek倍率按同模型、同峰谷时段比较，不直接用两行相除。"))
    if safe == nil {
      items.append(note(row.multiple == .dash ? "缺少同模型、同峰谷时段基准或其他可比较安全样本" : "无安全样本，倍率采用模型参考费用"))
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
