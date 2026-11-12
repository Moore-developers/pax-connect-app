import AppKit
import PaxCore
import PaxUI

extension AIUsageLogView {
  func cell(_ request: AIRequest, column: Int) -> NSView {
    func secondary(_ text: String) -> NSTextField {
      let label = PaxText.tableLabel(text, secondary: true)
      label.toolTip = text
      return label
    }
    func pair(_ first: NSView, _ second: NSView) -> NSView {
      AIUsageLogPairView(first, second)
    }
    switch column {
    case 0:
      let tile = AIIdentityTile(content: AIClientMark.view(request.client))
      tile.toolTip = request.client.rawValue
      let cli = PaxText.tableLabel(request.client.shortName)
      cli.setContentCompressionResistancePriority(.required, for: .horizontal)
      let row = AIUsageLayout.stack([tile, cli], vertical: false)
      row.spacing = PaxTheme.Spacing.tight
      return row
    case 1:
      // Two fixed lines: model above, time below.
      let model = PaxText.tableLabel(store.model(request.modelID), identity: true)
      model.lineBreakMode = .byTruncatingTail
      model.toolTip = model.stringValue
      var heading: [NSView] = [model]
      if !request.reasoningEffort.isEmpty {
        heading.append(PaxFeatureTag(request.reasoningEffort.capitalized, tone: .blue))
      }
      let modelRow = AIUsageLayout.stack(heading, vertical: false)
      modelRow.spacing = PaxTheme.Spacing.tight
      model.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
      model.setContentHuggingPriority(.defaultLow, for: .horizontal)
      let lines = AIUsageLayout.stack([
        modelRow, secondary(TrafficFormatting.dateMinute(request.at)),
      ])
      lines.spacing = PaxTheme.Spacing.xs
      return lines
    case 2:
      let text = request.client == .harness ? "只读" : request.profileName
      let label = PaxText.tableLabel(text)
      label.lineBreakMode = .byTruncatingTail
      label.alignment = .center
      label.toolTip = text
      return label
    case 3:
      // Plain text, no tag background.
      let multiplier = secondary(AIUsageLogFormat.multiplier(request))
      multiplier.alignment = .center
      return multiplier
    case 4:
      let speed = PaxText.tableLabel(
        request.succeeded
          ? request.tps.map { String(format: "%.1f tok/s", $0) } ?? "—" : "—",
        numeric: true)
      speed.textColor = PaxTheme.Color.healthy
      let elapsed = request.elapsed.map { String(format: "%.1fs", $0) } ?? "—"
      let timingLabel = secondary(elapsed + " 总耗时")
      timingLabel.toolTip = "发送到结束；缺少可靠发送时间时显示—。TPS = 输出 Token ÷ 总耗时"
      return AIUsageLogPairView(speed, timingLabel, centered: true)
    case 5:
      // Token above cost, both right-aligned in their own colors.
      let tokens = PaxText.tableLabel(
        AIUsageFormatting.tokens(request.tokens), numeric: true)
      tokens.alignment = .right
      let fee = PaxText.tableLabel("", numeric: true, alignment: .right)
      AIUsageCostDisplay.apply(
        fee, official: request.officialCostLabel, channel: request.costLabel)
      let lines = AIUsageLayout.stack([tokens, fee])
      lines.spacing = PaxTheme.Spacing.xs
      return lines
    case 6:
      let rate = PaxText.tableLabel(AIUsageLogFormat.cacheRate(request), numeric: true)
      rate.alignment = .center
      rate.textColor = AIUsageLogFormat.cacheColor
      return rate
    default:
      // ✓ on success; a short error on failure with the full reason in the tooltip.
      let text = request.succeeded ? "✓" : AIUsageFormatting.failureBrief(request.errorMessage)
      let label = PaxText.tableLabel(text)
      label.textColor = request.succeeded ? PaxTheme.Color.healthy : PaxTheme.Color.error
      label.lineBreakMode = .byTruncatingTail
      label.alignment = .center
      label.toolTip = request.succeeded ? "成功" : request.errorMessage ?? "请求失败"
      label.setAccessibilityLabel(request.succeeded ? "成功" : "失败 " + text)
      return label
    }
  }
}

/// Pure text and color rules of the log cells, shared with tests.
enum AIUsageLogFormat {
  static let tokenColor = PaxTheme.Color.text
  static let cacheColor = PaxTheme.Chart.cacheLabel
  static func multiplier(_ request: AIRequest) -> String {
    AIUsageFormatting.multiplier(request.multiplier)
  }
  /// Token count in the token color; the cache rate has its own column.
  static func tokenLine(_ request: AIRequest) -> NSAttributedString {
    NSAttributedString(
      string: AIUsageFormatting.tokens(request.tokens),
      attributes: [.font: PaxTheme.Typography.time, .foregroundColor: tokenColor])
  }
  /// "99.6%", one decimal; "—" without input.
  static func cacheRate(_ request: AIRequest) -> String {
    AIUsageFormatting.cacheShare(read: request.read, input: request.input) ?? "—"
  }
  struct Stat: Equatable {
    let title: String
    let value: String
    let color: NSColor
    var officialCost: String? = nil
    var channelCost: String? = nil
  }
  /// The five summary cards, computed from the totals of every row matching the period and filters;
  /// the first card carries the period name.
  static func stats(period: String, totals: AIUsageSummary) -> [Stat] {
    [
      .init(
        title: period + " · 筛选结果", value: AIUsageFormatting.count(totals.count) + " 条",
        color: tokenColor),
      .init(title: "Token", value: AIUsageFormatting.tokens(totals.tokens), color: tokenColor),
      .init(
        title: "官方费用 / 渠道费用",
        value: AIUsageCostDisplay.text(
          official: totals.officialCostLabel, channel: totals.costLabel),
        color: AIUsageCostDisplay.officialColor, officialCost: totals.officialCostLabel,
        channelCost: totals.costLabel),
      .init(title: "缓存率", value: AIUsageFormatting.percent(totals.cache), color: cacheColor),
      .init(
        title: "成功", value: AIUsageFormatting.percent(totals.successRate),
        color: PaxTheme.Color.healthy),
    ]
  }
  /// "今日 · 筛选结果 1,930 条 · Token 375.4M · …" plain text for tooltips and accessibility.
  static func summary(period: String, totals: AIUsageSummary) -> String {
    let items = stats(period: period, totals: totals)
    return items[0].title + " " + items[0].value
      + items.dropFirst().map { " · " + $0.title + " " + $0.value }.joined()
  }
}

extension AIClient {
  /// Short CLI name for dense rows; the full name stays in tooltips.
  fileprivate var shortName: String {
    switch self {
    case .claude: "Claude"
    case .codex: "Codex"
    case .harness: "DSH"
    }
  }
}
