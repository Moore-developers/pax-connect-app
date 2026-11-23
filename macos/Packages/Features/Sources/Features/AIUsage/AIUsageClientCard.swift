import AppKit
import PaxCore
import PaxUI

extension AIUsageViewController {
  /// Header, optional configuration notice, then usage, bar and billing metadata per CLI.
  func clientContent() -> NSStackView {
    let tabs = measureTabs(cost: store.profileMeasureCost, group: "ai_usage.client_usage") {
      [weak self] cost in
      guard let self, self.store.profileMeasureCost != cost else { return }
      self.refreshMeasuresOnly = true
      self.store.perform(.profileMeasureCost(cost))
    }
    contentRefreshers.append { [weak self, weak tabs] in
      tabs?.selectedIndex = self?.store.profileMeasureCost == true ? 1 : 0
    }
    let header = self.interfaceCopy.usageHeader(
      symbol: "terminal", titleKey: "ai_usage.client_usage", trailing: [tabs])
    let rows = store.clientRows(cost: store.profileMeasureCost, context: self.interfaceCopy.context)
    let total = rows.reduce(0) { $0 + $1.amount }
    var items: [NSView] = [header]
    let noticeHost = AIUsageLayout.stack([])
    var noticeKey = ""
    let updateNotice: () -> Void = { [weak self, weak noticeHost] in
      guard let self, let noticeHost else { return }
      let key = self.store.bindingIssues(context: self.interfaceCopy.context).map {
        $0.client.rawValue + $0.label
      }.joined(
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
    let rowHost = AIUsageLayout.stack([])
    items.append(rowHost)
    let views = Dictionary(
      uniqueKeysWithValues: rows.map { ($0.client, clientRow($0, total: total)) })
    var visible: [AIClient] = []
    let updateOrder: () -> Void = { [weak self, weak rowHost] in
      guard let self, let rowHost else { return }
      let next = self.store.clientRows(
        cost: self.store.profileMeasureCost, context: self.interfaceCopy.context
      ).enumerated().sorted { lhs, rhs in
        if lhs.element.amount != rhs.element.amount {
          return lhs.element.amount > rhs.element.amount
        }
        if lhs.element.hasUsage != rhs.element.hasUsage { return lhs.element.hasUsage }
        return lhs.offset < rhs.offset
      }.prefix(3).map { $0.element.client }
      guard visible != next else { return }
      visible = next
      for child in rowHost.arrangedSubviews {
        rowHost.removeArrangedSubview(child)
        child.removeFromSuperview()
      }
      for client in next {
        guard let row = views[client] else { continue }
        rowHost.addArrangedSubview(row)
        row.widthAnchor.constraint(equalTo: rowHost.widthAnchor).isActive = true
      }
    }
    rowHost.spacing = PaxTheme.Spacing.regular
    contentRefreshers.append(updateOrder)
    updateOrder()
    let content = AIUsageLayout.stack(items)
    content.spacing = PaxTheme.Spacing.regular
    return content
  }

  private func issueNotice() -> NSView? {
    let issues = store.bindingIssues(context: self.interfaceCopy.context)
    guard let first = issues.first else { return nil }
    let title: String
    let detail: String
    if issues.count == 1 {
      title = first.client.rawValue + " " + first.label
      detail =
        first.client == .harness
        ? self.interfaceCopy.text("ai_usage.dsh_independent")
        : self.interfaceCopy.text("ai_usage.future_sessions")
    } else {
      title = self.interfaceCopy.text(
        "interface_detail.f4f220e9b8", ["arg0": String(describing: issues.count)])
      detail = issues.map(\.client.rawValue).joined(separator: "、")
    }
    let notice = PaxNotice(title: title, detail: detail, tone: .warning)
    notice.setAction(
      self.interfaceCopy.text("ai_usage.configure"), identifier: "ai-configure-" + first.client.code
    )
    notice.onAction = { [weak self, weak notice] in
      guard let self, let notice else { return }
      self.presentBinding(first.client, from: notice)
    }
    return notice
  }

  private func billingText(_ row: AIClientUsageRow) -> String {
    if row.client == .antigravity {
      return self.interfaceCopy.text("ai_usage.antigravity.estimate")
    }
    if row.serviceSource == .officialAccount {
      return self.interfaceCopy.text("ai_usage.account_estimate")
    }
    return row.configurationText(context: self.interfaceCopy.context) + " · " + row.billedCostText
  }

  private func clientRow(_ row: AIClientUsageRow, total: Double) -> NSView {
    let name = AIUsageLayout.label(row.client.rawValue)
    name.lineBreakMode = .byTruncatingTail
    let amount = AIUsageLayout.label(row.value, numeric: true)
    let share = AIUsageLayout.label(row.shareText, secondary: true, numeric: true)
    share.isHidden = row.shareText.isEmpty
    let profile = AIUsageLayout.label(
      billingText(row), secondary: true)
    profile.lineBreakMode = .byTruncatingTail
    profile.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    let state = PaxFeatureTag(row.state ?? "", tone: row.needsAction ? .warning : .neutral)
    state.isHidden = row.state == nil
    let stateHost = AIUsageLayout.stack([state], vertical: false)
    stateHost.isHidden = row.state == nil
    let top = AIUsageLayout.stack(
      [AIClientMark.view(row.client), name, stateHost, NSView(), amount, share], vertical: false)
    let requests = AIUsageLayout.label(row.requestsText, secondary: true, numeric: true)
    requests.setContentCompressionResistancePriority(.required, for: .horizontal)
    let meta = AIUsageLayout.stack([profile, NSView(), requests], vertical: false)
    let bar = PaxShareBar(
      segments: [.init(row.client.rawValue, value: row.amount)], colors: [PaxTheme.Routes.blue],
      total: total)
    let barHost = AIUsageLayout.stack([bar])
    let body = AIUsageLayout.stack([top, barHost, meta])
    body.spacing = PaxTheme.Spacing.tight
    let button = actions.overlay(
      label: row.client.isUsageOnly
        ? self.interfaceCopy.text("ai_usage.view_client", ["name": row.client.rawValue])
        : self.interfaceCopy.text("ai_usage.manage_prefix") + row.client.rawValue
          + self.interfaceCopy.text("ai_usage.profile_suffix"),
      id: "ai-client-" + row.client.code
    ) { [weak self] in
      guard let self else { return }
      if row.client.isUsageOnly {
        self.store.perform(.openClientLogs(row.client))
      } else {
        self.store.perform(.page(.profiles))
      }
    }
    overlay(button, on: body)
    var displayed = row
    var denominator = total
    contentRefreshers.append { [weak self] in
      guard let self,
        let next = self.store.clientRows(
          cost: self.store.profileMeasureCost, context: self.interfaceCopy.context
        ).first(where: {
          $0.client == row.client
        })
      else { return }
      let total = self.store.clientRows(
        cost: self.store.profileMeasureCost, context: self.interfaceCopy.context
      ).reduce(0) {
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
    body.toolTip = self.interfaceCopy.text(
      "interface_detail.835ab983a9", ["arg0": String(describing: row.client.rawValue)])
    return body
  }
}
