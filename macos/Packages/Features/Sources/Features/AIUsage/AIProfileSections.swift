import AppKit
import PaxCore
import PaxUI

extension AIProfileEditor {
  func profileSection(_ title: String, symbol: String, views: [NSView], detail: String = "")
    -> NSView
  {
    let content = AIUsageLayout.stack(
      [PaxRouteSectionHeading(title, symbol: symbol, detail: detail)] + views)
    content.spacing = PaxTheme.Spacing.medium
    let card = PaxCard(style: .routesSection)
    card.contentView = content
    card.heightAnchor.constraint(
      equalTo: content.heightAnchor, constant: PaxTheme.Routes.sectionTop * 2
    ).isActive = true
    return card
  }
  func modelSummary(_ p: AIProfile) -> String {
    let sync =
      p.modelsChecked.map {
        self.interfaceCopy.text("profile_section.last_sync_prefix")
          + TrafficFormatting.dateMinute($0)
      } ?? ""
    switch p.modelsState {
    case "ready":
      return interfaceCopy.text("profile_section.catalog_count", count: p.catalog.count) + sync
    case "partial":
      return interfaceCopy.text("profile_section.catalog_partial", count: p.catalog.count) + sync
    case "failed":
      let reason = modelFailure(p.modelsError)
      return p.catalog.isEmpty
        ? self.interfaceCopy.text("profile_section.read_failed_prefix") + reason
        : interfaceCopy.text(
          "profile_section.catalog_preserved",
          ["count": p.catalog.count.formatted(), "reason": reason, "sync": sync])
    default: return self.interfaceCopy.text("profile_section.catalog_unread")
    }
  }
  func modelFailure(_ code: String) -> String {
    switch code {
    case "models_timeout": return self.interfaceCopy.text("profile_section.connection_timeout")
    case "models_auth_failed":
      return self.interfaceCopy.text("profile_section.authentication_failed")
    case "models_not_found": return self.interfaceCopy.text("profile_section.endpoint_missing")
    case "models_invalid_response":
      return self.interfaceCopy.text("profile_section.invalid_response")
    default: return self.interfaceCopy.text("profile_section.provider_unavailable")
    }
  }
  func modelRefreshRow(_ p: AIProfile) -> NSView {
    AIUsageLayout.stack(
      [
        AIUsageLayout.label(modelSummary(p)), NSView(),
        actions.button(
          store.configuration.loading
            ? self.interfaceCopy.text("profile_section.loading")
            : self.interfaceCopy.text("profile_section.refresh_models"),
          enabled: !store.preview && !store.configuration.loading
        ) { [weak self] in
          guard let self else { return }
          Task { [weak self] in
            guard let self else { return }
            _ = await store.configuration.refreshModels(p.id)
          }
        },
      ], vertical: false)
  }
  /// The cards below the connection form; a new Profile only gets a hint.
  func profileDetailViews() -> [NSView] {
    guard let p = current else {
      return [
        AIUsageLayout.label(
          self.interfaceCopy.text("profile_section.catalog_after_save"), secondary: true)
      ]
    }
    return [applySection(p), usageSection(p), modelsSection(p)]
  }
  private func applySection(_ p: AIProfile) -> NSView {
    let rows = store.configurableClients.filter { p.kind != "native" || p.nativeCLI == $0 }.map {
      cli -> NSView in
      let b = store.bindings[cli]
      let owns = b?.profileID == p.id
      let label =
        owns
        ? AIProfilesStore.stateLabel(b, context: interfaceCopy.context)
        : self.interfaceCopy.text("profile_section.not_applied")
      let mark = AIUsageLayout.label(label, secondary: true)
      if owns && b?.state == "applied" { mark.textColor = AIUsageCostDisplay.channelColor }
      return AIUsageLayout.stack(
        [
          AIUsageLayout.label(cli.rawValue), NSView(), mark,
          actions.button(
            self.interfaceCopy.text("profile_section.switch"),
            enabled: store.configuration.available
          ) { [weak self] in
            guard let self else { return }
            self.presentWorkbenchModal(
              AIBindingController(store: store, client: cli, targetProfile: p.id))
          },
        ], vertical: false)
    }
    return profileSection(
      self.interfaceCopy.text("profile_section.application_state"), symbol: "terminal", views: rows)
  }
  private func usageSection(_ p: AIProfile) -> NSView {
    let summary = store.usageSummary(profile: p.id, lifetime: true)
    let body: NSView =
      summary.count > 0
      ? AIUsageLayout.equalRow([
        metric("Token", AIUsageFormatting.tokens(summary.tokens)),
        metric(
          self.interfaceCopy.text("ai_usage.official_cost"), summary.officialCostLabel,
          color: AIUsageCostDisplay.officialColor),
        metric(
          self.interfaceCopy.text("profile_section.channel_cost"), summary.costLabel,
          color: AIUsageCostDisplay.channelColor),
        metric(self.interfaceCopy.text("ai_usage.requests"), String(summary.count)),
      ])
      : AIUsageLayout.label(
        self.interfaceCopy.text("profile_section.unattributed_requests"), secondary: true)
    return profileSection(
      self.interfaceCopy.text("profile_section.lifetime_usage"), symbol: "chart.bar", views: [body],
      detail: self.interfaceCopy.text("profile_section.lifetime_note"))
  }
  private func metric(_ title: String, _ value: String, color: NSColor? = nil) -> NSView {
    let number = AIUsageLayout.label(value, metric: true)
    if let color { number.textColor = color }
    return AIUsageLayout.stack([AIUsageLayout.label(title, secondary: true), number])
  }
  private func modelsSection(_ p: AIProfile) -> NSView {
    if p.kind == "native" {
      return profileSection(
        self.interfaceCopy.text("ai_usage.model"), symbol: "sparkles",
        views: [
          AIUsageLayout.label(
            self.interfaceCopy.text("profile_section.native_catalog"), secondary: true)
        ])
    }
    var views: [NSView] = [modelRefreshRow(p)]
    let split = usedModels(p)
    if let top = store.topModelLine(p, context: interfaceCopy.context) {
      let period = store.period.label(context: interfaceCopy.context)
      views.append(AIUsageLayout.label(period + " · " + top, secondary: true))
    }
    if !split.used.isEmpty { views.append(modelTable(p, split.used)) }
    if !split.unused.isEmpty {
      let table = modelTable(p, split.unused)
      table.isHidden = !showUnusedModels
      let title = { [self] in unusedTitle(split.unused.count, hasUsed: !split.used.isEmpty) }
      let box = ToggleBox()
      let toggle = actions.button(title()) { [weak self, weak table] in
        guard let self, let table else { return }
        showUnusedModels.toggle()
        pane.performContentUpdates { table.isHidden = !showUnusedModels }
        box.button?.title = title()
      }
      box.button = toggle
      toggle.compactStyle = .text
      views += [AIUsageLayout.wrap(toggle), table]
    }
    return profileSection(
      self.interfaceCopy.text("ai_usage.model"), symbol: "sparkles", views: views)
  }
  private func unusedTitle(_ n: Int, hasUsed: Bool) -> String {
    (showUnusedModels ? self.interfaceCopy.text("profile_section.collapse_prefix") : "")
      + (hasUsed
        ? interfaceCopy.text("profile_section.unused_models", count: n)
        : interfaceCopy.text("profile_section.no_model_usage", count: n))
      + (showUnusedModels ? "" : " ›")
  }
  /// Catalog models only; used ones first, by cost.
  private func usedModels(_ p: AIProfile) -> (used: [AIProviderModel], unused: [AIProviderModel]) {
    let rated = p.catalog.map {
      ($0, store.usageSummary(model: $0.id, profile: p.id, lifetime: true))
    }
    let used = rated.filter { $0.1.count > 0 }.sorted {
      $0.1.cost != $1.1.cost ? $0.1.cost > $1.1.cost : $0.0.id < $1.0.id
    }
    return (used.map(\.0), rated.filter { $0.1.count == 0 }.map(\.0))
  }
  private func modelTable(_ p: AIProfile, _ models: [AIProviderModel]) -> PaxDataTable {
    let owners = Set(p.catalog.map(\.owner))
    let showOwner = owners.count > 1 && owners.contains { !$0.isEmpty }
    let showContext = p.catalog.contains { $0.contextWindow > 0 }
    var columns: [PaxDataTable.Column] = [
      .init(
        self.interfaceCopy.text("profile_section.model_id"), width: 190, growth: 1.6,
        symbol: "sparkles", sortable: false)
    ]
    if showOwner {
      columns.append(
        .init(
          self.interfaceCopy.text("profile_section.provider"), width: 110, growth: 0.3,
          sortable: false))
    }
    if showContext {
      columns.append(
        .init(
          self.interfaceCopy.text("profile_section.context"), width: 100, growth: 0, sortable: false
        ))
    }
    columns.append(
      .init(
        self.interfaceCopy.text("ai_usage.column.tokens"), width: 96, growth: 0, sortable: false))
    let costColumn = columns.count
    columns.append(
      .init(
        self.interfaceCopy.text("profile_section.official_channel_cost"), width: 190, growth: 0,
        inset: PaxTheme.Spacing.regular * 1.5,
        sortable: false, toolTip: AIUsageCostDisplay.explanation(context: interfaceCopy.context)))
    let table = PaxDataTable(
      columns: columns,
      rows: models.map { m in
        let usage = store.usageSummary(model: m.id, profile: p.id, lifetime: true)
        var values = [m.id]
        if showOwner { values.append(m.owner.isEmpty ? "—" : m.owner) }
        if showContext {
          values.append(m.contextWindow > 0 ? AIUsageFormatting.tokens(Int(m.contextWindow)) : "—")
        }
        values.append(usage.count > 0 ? AIUsageFormatting.tokens(usage.tokens) : "—")
        values.append(
          usage.count > 0
            ? AIUsageCostDisplay.text(official: usage.officialCostLabel, channel: usage.costLabel)
            : "—")
        return .init(id: m.id, values: values)
      }, metrics: .workbench(rowHeight: PaxTheme.Dashboard.rowHeight, rounded: true),
      selection: .none)
    table.cellProvider = { [weak self] id, column in
      guard column == costColumn, let self else { return nil }
      let usage = self.store.usageSummary(model: id, profile: p.id, lifetime: true)
      return AIUsageCostDisplay.label(
        official: usage.count > 0 ? usage.officialCostLabel : "—",
        channel: usage.count > 0 ? usage.costLabel : "—", stacked: true,
        context: interfaceCopy.context)
    }
    table.setTableIdentifier("ai-profile-model-catalog")
    return table
  }
}

@MainActor private final class ToggleBox { weak var button: PaxCompactButton? }
