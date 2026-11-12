import AppKit
import PaxCore
import PaxUI

extension AIUsageViewController {
  /// Header, optional configuration notice, then usage, bar and billing metadata per CLI.
  func clientContent() -> NSStackView {
    let tabs = measureTabs(cost: store.profileMeasureCost) { [weak self] cost in
      guard let self, self.store.profileMeasureCost != cost else { return }
      self.refreshMeasuresOnly = true
      self.store.perform(.profileMeasureCost(cost))
    }
    contentRefreshers.append { [weak self, weak tabs] in
      tabs?.selectedIndex = self?.store.profileMeasureCost == true ? 1 : 0
    }
    let header = AIUsageLayout.cardHeader(symbol: "terminal", title: "客户端用量", trailing: [tabs])
    let rows = store.clientRows(cost: store.profileMeasureCost)
    let total = rows.reduce(0) { $0 + $1.amount }
    var items: [NSView] = [header]
    let noticeHost = AIUsageLayout.stack([])
    var noticeKey = ""
    let updateNotice: () -> Void = { [weak self, weak noticeHost] in
      guard let self, let noticeHost else { return }
      let key = self.store.bindingIssues.map { $0.client.rawValue + $0.label }.joined(
        separator: "|")
      guard key != noticeKey else { return }
      noticeKey = key
      for child in noticeHost.arrangedSubviews {
        noticeHost.removeArrangedSubview(child)
        child.removeFromSuperview()
      }
      if let notice = self.issueNotice() {
        noticeHost.addArrangedSubview(notice)
        notice.widthAnchor.constraint(equalTo: noticeHost.widthAnchor).isActive = true
      }
      noticeHost.isHidden = key.isEmpty
    }
    contentRefreshers.append(updateNotice)
    noticeHost.isHidden = true
    updateNotice()
    items.append(noticeHost)
    items += rows.map { clientRow($0, total: total) }
    let content = AIUsageLayout.stack(items)
    content.spacing = PaxTheme.Spacing.regular
    return content
  }

  private func issueNotice() -> NSView? {
    let issues = store.bindingIssues
    guard let first = issues.first else { return nil }
    let title: String
    let detail: String
    if issues.count == 1 {
      title = first.client.rawValue + " " + first.label
      detail =
        first.client == .harness ? "DSH 用量按本地配置统计，独立于 Pax Profile 归属。" : "配置只影响后续会话；历史用量不重新归属。"
    } else {
      title = "\(issues.count) 个 CLI 需要处理"
      detail = issues.map(\.client.rawValue).joined(separator: "、")
    }
    let notice = PaxNotice(title: title, detail: detail, tone: .warning)
    notice.setAction("去配置", identifier: "ai-configure-" + first.client.code)
    notice.onAction = { [weak self, weak notice] in
      guard let self, let notice else { return }
      self.presentBinding(first.client, from: notice)
    }
    return notice
  }

  private func billingText(_ row: AIClientUsageRow) -> String {
    if row.serviceSource == .officialAccount { return "官方账号 · API 等值估算，非订阅扣费" }
    return row.configurationText + " · " + row.billedCostText
  }

  private func clientRow(_ row: AIClientUsageRow, total: Double) -> NSView {
    let name = AIUsageLayout.label(row.client.rawValue)
    name.lineBreakMode = .byTruncatingTail
    let amount = AIUsageLayout.label(row.value, numeric: true)
    let share = AIUsageLayout.label(row.shareText, secondary: true, numeric: true)
    share.isHidden = row.shareText.isEmpty
    let heading: [NSView] = [AIClientMark.view(row.client), name, NSView(), amount, share]
    let top = AIUsageLayout.stack(heading, vertical: false)
    let profile = AIUsageLayout.label(
      billingText(row), secondary: true)
    profile.lineBreakMode = .byTruncatingTail
    let state = PaxFeatureTag(row.state ?? "", tone: row.needsAction ? .warning : .neutral)
    state.isHidden = row.state == nil
    let stateHost = AIUsageLayout.stack([state], vertical: false)
    var meta: [NSView] = [profile, stateHost]
    let requests = AIUsageLayout.label(row.requestsText, secondary: true, numeric: true)
    requests.setContentCompressionResistancePriority(.required, for: .horizontal)
    meta += [NSView(), requests]
    let bar = PaxShareBar(
      segments: [.init(row.client.rawValue, value: row.amount)], colors: [PaxTheme.Routes.blue],
      total: total)
    let barHost = AIUsageLayout.stack([bar])
    let body = AIUsageLayout.stack([top, barHost, AIUsageLayout.stack(meta, vertical: false)])
    body.spacing = PaxTheme.Spacing.tight
    let button = actions.overlay(
      label: row.client == .harness ? "查看 DSH 用量" : "管理 " + row.client.rawValue + " 的 Profile",
      id: "ai-client-" + row.client.code
    ) { [weak self] in
      guard let self else { return }
      if row.client == .harness {
        self.store.perform(.page(.logs))
        self.store.perform(.logClient(.harness))
      } else {
        self.store.perform(.page(.profiles))
      }
    }
    overlay(button, on: body)
    var displayed = row
    var denominator = total
    contentRefreshers.append { [weak self] in
      guard let self,
        let next = self.store.clientRows(cost: self.store.profileMeasureCost).first(where: {
          $0.client == row.client
        })
      else { return }
      let total = self.store.clientRows(cost: self.store.profileMeasureCost).reduce(0) {
        $0 + $1.amount
      }
      guard displayed != next || denominator != total else { return }
      displayed = next
      denominator = total
      amount.stringValue = next.value
      share.stringValue = next.shareText
      share.isHidden = next.shareText.isEmpty
      profile.stringValue = self.billingText(next)
      requests.stringValue = next.requestsText
      for child in stateHost.arrangedSubviews {
        stateHost.removeArrangedSubview(child)
        child.removeFromSuperview()
      }
      stateHost.isHidden = next.state == nil
      if let text = next.state {
        stateHost.addArrangedSubview(
          PaxFeatureTag(text, tone: next.needsAction ? .warning : .neutral))
      }
      for child in barHost.arrangedSubviews {
        barHost.removeArrangedSubview(child)
        child.removeFromSuperview()
      }
      barHost.addArrangedSubview(
        PaxShareBar(
          segments: [.init(next.client.rawValue, value: next.amount)],
          colors: [PaxTheme.Routes.blue], total: total))
    }
    body.toolTip = "所选时段内 \(row.client.rawValue) 的用量，点击查看"
    return body
  }
}
