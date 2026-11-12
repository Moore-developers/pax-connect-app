import AppKit
import PaxCore
import PaxUI

extension AIUsageViewController {
  /// One card, one segment per CLI; reads the same projection as the overview client card.
  func bindingStrip() -> NSView {
    let segments = store.clientRows(cost: false).filter { $0.client != .harness }.map(
      bindingSegment)
    let card = AIUsageLayout.segmentedCard(segments)
    if let content = card.contentView {
      for segment in segments {
        segment.bottomAnchor.constraint(lessThanOrEqualTo: content.bottomAnchor).isActive = true
      }
    }
    return card
  }

  private func bindingSegment(_ row: AIClientUsageRow) -> NSView {
    let name = AIUsageLayout.label(row.client.rawValue, secondary: true)
    let heading = AIUsageLayout.stack([AIClientMark.view(row.client), name], vertical: false)
    heading.spacing = PaxTheme.Spacing.tight
    let profileText = row.configurationText
    let profile = PaxText.label(profileText, font: PaxTheme.Typography.section)
    profile.lineBreakMode = .byTruncatingTail
    profile.toolTip = profileText
    let lines = store.cliCardLines(row.client)
    let usage = AIUsageLayout.label("", secondary: true, numeric: true)
    let recent = AIUsageLayout.label("", secondary: true, numeric: true)
    let costs = AIUsageLayout.label("", numeric: true)
    usage.attributedStringValue = aiMetricText(lines.usageRuns)
    recent.attributedStringValue = aiMetricText(lines.recentRuns)
    costs.attributedStringValue = aiMetricText(lines.costRuns)
    costs.isHidden = lines.costRuns.isEmpty
    for field in [profile, usage, costs, recent] {
      field.lineBreakMode = .byTruncatingTail
      field.setContentCompressionResistancePriority(.required, for: .vertical)
    }
    usage.setAccessibilityIdentifier("ai-strip-usage-" + row.client.code)
    recent.setAccessibilityIdentifier("ai-strip-recent-" + row.client.code)
    let badge = PaxBadge(
      title: row.stateText, tone: AIUsageStore.tone(forState: row.stateText))
    let unconfigured = row.unconfigured
    let button = actions.button(
      unconfigured ? "去配置" : "切换…", enabled: store.configuration.available,
      id: "ai-strip-" + row.client.code
    ) { [weak self] in
      guard let self else { return }
      self.presentWorkbenchModal(AIBindingController(store: self.store, client: row.client))
    }
    let axName = (unconfigured ? "去配置 " : "切换 ") + row.client.rawValue
    button.setAccessibilityLabel(axName)
    button.toolTip = axName
    button.setContentCompressionResistancePriority(.required, for: .horizontal)
    button.heightAnchor.constraint(equalToConstant: button.capsuleHeight).isActive = true
    badge.lineBreakMode = .byTruncatingTail
    badge.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    let top = AIUsageLayout.stack([heading, NSView(), badge, button], vertical: false)
    let body = AIUsageLayout.stack([top, profile, usage, costs, recent])
    body.spacing = PaxTheme.Spacing.tight
    body.setCustomSpacing(PaxTheme.Spacing.tight / 2, after: top)
    usage.toolTip = "已绑定时为当前 Profile 的跨 CLI 用量；无 Profile 时为此 CLI 的全部来源用量。官方账号费用是 API 等值估算。"
    costs.toolTip = AIUsageCostDisplay.explanation
    contentRefreshers.append { [weak self] in
      guard let self,
        let next = self.store.clientRows(cost: false).first(where: { $0.client == row.client })
      else { return }
      let text = next.configurationText
      profile.stringValue = text
      profile.toolTip = text
      let lines = self.store.cliCardLines(row.client)
      usage.attributedStringValue = aiMetricText(lines.usageRuns)
      recent.attributedStringValue = aiMetricText(lines.recentRuns)
      costs.attributedStringValue = aiMetricText(lines.costRuns)
      costs.isHidden = lines.costRuns.isEmpty
      badge.apply(title: next.stateText, tone: AIUsageStore.tone(forState: next.stateText))
      button.title = next.unconfigured ? "去配置" : "切换…"
      button.isEnabled = self.store.configuration.available
      button.setAccessibilityLabel((next.unconfigured ? "去配置 " : "切换 ") + row.client.rawValue)
      button.toolTip = button.accessibilityLabel()
    }
    return body
  }
}

/// Value colour per metric role, shared by the profile page, the log cards and the TOP10 table.
func aiMetricColor(_ role: AICLICardLines.Run.Role) -> NSColor {
  switch role {
  case .label: PaxTheme.Color.textSecondary
  case .cost: AIUsageCostDisplay.channelColor
  case .officialCost: AIUsageCostDisplay.officialColor
  case .token: AIUsageLogFormat.tokenColor
  case .request: PaxTheme.Color.healthy
  case .cache: AIUsageLogFormat.cacheColor
  }
}

/// Runs as one attributed line: labels secondary, values in their metric colour.
func aiMetricText(_ runs: [AICLICardLines.Run]) -> NSAttributedString {
  let font = PaxTheme.Typography.time
  let text = NSMutableAttributedString()
  for run in runs {
    text.append(
      .init(string: run.text, attributes: [.font: font, .foregroundColor: aiMetricColor(run.role)]))
  }
  return text
}
