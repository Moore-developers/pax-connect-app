import AppKit
import PaxCore
import PaxUI

extension AIUsageViewController {
  func bindingStrip() -> NSView {
    let context = interfaceCopy.context
    let metrics = PaxDataTable.Metrics.workbench(rowHeight: 68, rounded: true)
    let table = PaxDataTable(
      columns: [
        .init(
          context.text("ai_workbench.client_configuration"), width: 164, growth: 1.5,
          symbol: "terminal", sortable: false, contentLayout: .fill),
        .init(
          profilePeriodUsageTitle, width: 145, growth: 1,
          sortable: false, contentLayout: .fill),
        .init(
          context.text("ai_workbench.lifetime"), width: 145, growth: 1,
          sortable: false, contentLayout: .fill),
        .init(
          context.text("ai_workbench.actions"), width: 245, growth: 0.5,
          sortable: false),
      ], metrics: metrics, selection: .none, scrollMode: .page)
    let clients = AIClient.allCases
    let cells = Dictionary(uniqueKeysWithValues: clients.map { ($0.code, clientCells($0)) })
    table.cellProvider = { id, column in cells[id]?[column] }
    table.apply(rows: clients.map { .init(id: $0.code, values: [$0.rawValue]) })
    table.setTableIdentifier("ai-profile-clients")
    contentRefreshers.append { [weak self, weak table] in
      guard let self else { return }
      table?.setColumnTitle(self.interfaceCopy.text("ai_workbench.client_configuration"), at: 0)
      table?.setColumnTitle(self.profilePeriodUsageTitle, at: 1)
      table?.setColumnTitle(self.interfaceCopy.text("ai_workbench.lifetime"), at: 2)
      table?.setColumnTitle(self.interfaceCopy.text("ai_workbench.actions"), at: 3)
    }
    return table
  }

  private func clientCells(_ client: AIClient) -> [NSView] {
    let name = PaxText.tableLabel(client.rawValue, identity: true)
    let state = PaxText.tableLabel("", secondary: true)
    state.lineBreakMode = .byTruncatingTail
    let profile = actions.button("", id: "ai-client-current-" + client.code) { [weak self] in
      guard let self, let id = self.store.bindings[client]?.profileID else { return }
      self.revealProfile(id)
    }
    profile.compactStyle = .text
    profile.leadingAligned = true
    profile.font = PaxTheme.Typography.caption()
    let details = AIUsageLayout.stack([name, profile, state])
    details.spacing = PaxTheme.Spacing.xs
    let identity = AIUsageLayout.stack([AIClientMark.view(client), details], vertical: false)
    identity.spacing = PaxTheme.Spacing.tight
    details.trailingAnchor.constraint(equalTo: identity.trailingAnchor).isActive = true

    let period = AIProfileUsageCell()
    let lifetime = AIProfileUsageCell()
    period.setAccessibilityIdentifier("ai-strip-usage-" + client.code)
    lifetime.setAccessibilityIdentifier("ai-strip-lifetime-" + client.code)
    let usageButton = actions.button("", primary: true, id: "ai-strip-" + client.code) {
      [weak self] in
      self?.store.perform(.openClientLogs(client))
    }
    var buttons = [usageButton]
    let config =
      client.isUsageOnly
      ? nil
      : actions.button("", id: "ai-strip-config-" + client.code) { [weak self] in
        guard let self else { return }
        self.presentWorkbenchModal(AIBindingController(store: self.store, client: client))
      }
    let history =
      client.isUsageOnly
      ? nil
      : actions.button("", id: "ai-strip-history-" + client.code) { [weak self] in
        guard let self else { return }
        let controller = AIProfileHistoryController(store: self.store.configuration, client: client)
        controller.onReveal = { [weak self] id in self?.revealProfile(id) }
        self.presentWorkbenchModal(controller)
      }
    if let history { buttons.append(history) }
    if let config { buttons.append(config) }
    let prices =
      client == .antigravity
      ? actions.button("", id: "ai-antigravity-models") { [weak self] in
        guard let self, let data = self.store.data else { return }
        self.presentWorkbenchModal(AntigravityModelsController(data: data))
      } : nil
    if let prices { buttons.append(prices) }
    for button in buttons { button.capsule = false }
    let controls = AIUsageLayout.stack(buttons + [NSView()], vertical: false)
    controls.spacing = PaxTheme.Spacing.xs
    let update = { [weak self] in
      guard let self else { return }
      let context = self.interfaceCopy.context
      let binding = self.store.bindings[client]
      let bound = self.store.profiles.first { $0.id == binding?.profileID }
      profile.isHidden = bound == nil
      profile.setAccessibilityLabel(
        bound.map { context.text("ai_workbench.locate", ["name": $0.name]) })
      let readOnly = context.text("profile_state.read_only")
      let label =
        client.isUsageOnly ? readOnly : AIProfilesStore.stateLabel(binding, context: context)
      let recent = self.store.lastUsed(client).map {
        AIUsageFormatting.recentUse($0, now: self.store.now)
      }
      let sum = self.store.usageSummary(client: client)
      state.stringValue =
        sum.count == 0 && bound == nil && recent != nil
        ? label + " · " + context.text("ai_projection.1e0354e0b0") + (recent ?? "")
        : label
      state.isHidden = bound != nil
      state.toolTip = state.stringValue
      profile.title = bound.map { $0.name + " · " + label } ?? ""
      profile.toolTip = profile.title
      period.apply(sum, ready: self.store.periodUsageReady, showFees: false, context: context)
      lifetime.apply(
        self.store.usageSummary(client: client, lifetime: true),
        ready: self.store.lifetimeUsageReady, showFees: false, context: context)
      usageButton.title = context.text("ai_usage.view_usage")
      usageButton.setAccessibilityLabel(
        context.text("ai_usage.view_client", ["name": client.rawValue]))
      history?.title = context.text("ai_workbench.history")
      history?.setAccessibilityLabel(
        context.text("ai_workbench.client_history", ["client": client.rawValue]))
      history?.isEnabled = self.store.configuration.historySupported
      history?.toolTip =
        history?.isEnabled == true ? nil : context.text("ai_workbench.history_unavailable")
      config?.title = context.text(
        binding?.state == "missing"
          ? "ai_workbench.configure"
          : bound == nil ? "action.profile.recheck" : "ai_workbench.switch")
      config?.isEnabled = self.store.configuration.available
      prices?.title = context.text("ai_usage.antigravity.models")
      prices?.isEnabled = self.store.data?.antigravityModelsSupported == true
    }
    contentRefreshers.append(update)
    update()
    return [identity, period, lifetime, controls]
  }

  func revealProfile(_ id: String) {
    _ = profileTable?.revealRow(id, select: true)
  }
}
