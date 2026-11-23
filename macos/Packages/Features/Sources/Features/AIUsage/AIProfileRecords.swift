import AppKit
import PaxCore
import PaxUI

private let tileSize: CGFloat = 30

/// Preserve the existing CLI buttons; column sizing belongs to PaxDataTable.
private let applyButtonWidth: CGFloat = 122
private let applyWidth: CGFloat = applyButtonWidth * 2 + PaxTheme.Spacing.tight
private let moreWidth: CGFloat = 40
private let profileRowHeight: CGFloat = 96

/// Square identity tile: shows a CLI mark.
@MainActor
final class AIIdentityTile: NSView {
  init(content: NSView) {
    super.init(frame: .zero)
    wantsLayer = true
    content.translatesAutoresizingMaskIntoConstraints = false
    addSubview(content)
    NSLayoutConstraint.activate([
      widthAnchor.constraint(equalToConstant: tileSize),
      heightAnchor.constraint(equalToConstant: tileSize),
      content.centerXAnchor.constraint(equalTo: centerXAnchor),
      content.centerYAnchor.constraint(equalTo: centerYAnchor),
    ])
    setAccessibilityElement(false)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func layout() {
    super.layout()
    effectiveAppearance.performAsCurrentDrawingAppearance {
      layer?.backgroundColor = PaxTheme.Color.control.cgColor
      layer?.borderColor = PaxTheme.DashboardColor.iconBorder.cgColor
    }
    layer?.cornerRadius = PaxTheme.Dashboard.iconRadius
    layer?.borderWidth = PaxTheme.Layout.cardBorder
  }
}

/// Retains cell views and their update closure across data refreshes.
@MainActor
private final class AIProfileTableRecord {
  let cells: [NSView]
  let apply: (AIProfileRecord, AIProfile) -> Void
  init(cells: [NSView], apply: @escaping (AIProfileRecord, AIProfile) -> Void) {
    self.cells = cells
    self.apply = apply
  }
}

extension AIUsageViewController {
  func recordList() -> NSView {
    let columns: [PaxDataTable.Column] = [
      .init(
        "Profile", width: 136, growth: 1, symbol: "person.crop.circle",
        inset: PaxTheme.Spacing.tight, sortable: false),
      .init(
        self.interfaceCopy.text("interface_detail.aa330a7323"),
        width: applyWidth + PaxTheme.Spacing.xs * 2, growth: 1,
        alignment: .center, symbol: "terminal", inset: PaxTheme.Spacing.xs, sortable: false),
      .init(
        "", width: 147, growth: 1, alignment: .center, inset: PaxTheme.Spacing.xs, sortable: false,
        contentLayout: .fill),
      .init(
        "", width: 147, growth: 1, alignment: .center, inset: PaxTheme.Spacing.xs, sortable: false,
        contentLayout: .fill),
      .init(
        interfaceCopy.text("ai_workbench.actions"), width: 108, growth: 1,
        alignment: .center, inset: PaxTheme.Spacing.xs, sortable: false),
    ]
    let metrics = PaxDataTable.Metrics(rowHeight: profileRowHeight)
    let table = PaxDataTable(
      columns: columns, metrics: metrics, selection: .browse, scrollMode: .page)
    var rows: [String: AIProfileTableRecord] = [:]
    table.cellProvider = { id, column in rows[id]?.cells[column] }
    let open: (String) -> Void = { [weak self] id in
      guard let self, let profile = self.store.profiles.first(where: { $0.id == id }) else {
        return
      }
      self.presentWorkbenchModal(AIProfileEditor(store: self.store, profile: profile))
    }
    table.onRowClick = open
    table.onActivate = open
    table.menuForRow = { [weak self] id in
      guard let self, let profile = self.store.profiles.first(where: { $0.id == id }) else {
        return nil
      }
      return self.profileMenu(profile)
    }
    let update: () -> Void = { [weak self, weak table] in
      guard let self, let table else { return }
      let context = self.interfaceCopy.context
      table.setColumnTitle(context.text("interface_detail.aa330a7323"), at: 1)
      table.setColumnTitle(self.profilePeriodUsageTitle, at: 2)
      table.setColumnTitle(context.text("ai_workbench.profile_lifetime"), at: 3)
      table.setColumnTitle(context.text("ai_workbench.actions"), at: 4)
      let records = self.store.profileRecords(context: self.interfaceCopy.context)
      let keep = Set(records.map(\.id))
      rows = rows.filter { keep.contains($0.key) }
      for record in records {
        guard let profile = self.store.profiles.first(where: { $0.id == record.id }) else {
          continue
        }
        if let row = rows[record.id] {
          row.apply(record, profile)
        } else {
          rows[record.id] = self.recordRow(record, profile)
        }
      }
      table.apply(
        rows: records.map { .init(id: $0.id, values: [$0.name]) },
        emptyMessage: self.interfaceCopy.text("interface_detail.6731ba9f57"), reloading: [])
    }
    contentRefreshers.append(update)
    update()
    table.setTableIdentifier("ai-profile-list")
    profileTable = table
    return table
  }

  private func recordRow(_ r: AIProfileRecord, _ p: AIProfile) -> AIProfileTableRecord {
    let status = PaxBadge(title: r.statusText, tone: r.statusTone)
    let source = PaxText.tableLabel(r.source, secondary: true)
    source.lineBreakMode = .byTruncatingMiddle
    source.toolTip =
      r.isNative ? self.interfaceCopy.text("interface_detail.8e171fc12d") : p.endpoint
    var first: [NSView] = [status]
    let missing = interfaceCopy.bind(
      PaxText.tableLabel("", secondary: true), key: "interface_detail.bbcaaa9e88"
    ) { $0.stringValue = $1 }
    missing.textColor = PaxTheme.Color.warning
    missing.isHidden = !r.missingKey
    first.append(missing)
    first.append(NSView())
    let name = PaxText.tableLabel(r.name, identity: true)
    name.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    name.lineBreakMode = .byTruncatingTail
    let multiplier = AIUsageLayout.stack(
      [PaxFeatureTag(r.multiplier, tone: .blue)], vertical: false)
    let second = AIUsageLayout.stack(
      [name, multiplier, NSView()],
      vertical: false)
    second.spacing = PaxTheme.Spacing.tight
    let apply = AIUsageLayout.stack(applyButtons(p), vertical: false)
    apply.spacing = PaxTheme.Spacing.tight
    apply.widthAnchor.constraint(equalToConstant: applyWidth).isActive = true
    apply.setContentHuggingPriority(.required, for: .horizontal)
    apply.setContentCompressionResistancePriority(.required, for: .horizontal)
    let identity = AIUsageLayout.stack(
      [AIUsageLayout.stack(first, vertical: false), second, source])
    identity.spacing = PaxTheme.Spacing.xs
    identity.setAccessibilityIdentifier("ai-profile-row-" + r.id)
    let more = actions.button("···", id: "ai-profile-more-" + r.id) { [weak self] in
      guard let self, let current = self.store.profiles.first(where: { $0.id == p.id }) else {
        return
      }
      self.showProfileMenu(current)
    }
    interfaceCopy.bind(more) { button, context in
      button.toolTip = context.text("interface_detail.9f07d2ce41")
      button.setAccessibilityLabel(context.text("action.usage.profile_more"))
    }
    more.widthAnchor.constraint(equalToConstant: moreWidth).isActive = true
    more.setContentHuggingPriority(.required, for: .horizontal)
    more.setContentCompressionResistancePriority(.required, for: .horizontal)
    let details = actions.button(
      interfaceCopy.text("ai_workbench.details"), id: "ai-profile-details-" + r.id
    ) { [weak self] in
      guard let self, let current = self.store.profiles.first(where: { $0.id == p.id }) else {
        return
      }
      self.presentWorkbenchModal(AIProfileEditor(store: self.store, profile: current))
    }
    details.capsule = false
    interfaceCopy.button(details, key: "ai_workbench.details")
    more.capsule = false
    let operations = AIUsageLayout.stack([details, more], vertical: false)
    operations.spacing = PaxTheme.Spacing.xs
    let period = AIProfileUsageCell(alignment: .center)
    let lifetime = AIProfileUsageCell(alignment: .center)
    period.setAccessibilityIdentifier("ai-profile-period-" + r.id)
    lifetime.setAccessibilityIdentifier("ai-profile-lifetime-" + r.id)
    var displayedMultiplier = r.multiplier
    let row = AIProfileTableRecord(
      cells: [
        identity, AIUsageLayout.wrapCentered(apply), period, lifetime,
        AIUsageLayout.wrapCentered(operations),
      ]
    ) {
      [weak self] next, profile in
      guard let self else { return }
      let context = self.interfaceCopy.context
      status.apply(title: next.statusText, tone: next.statusTone)
      source.stringValue = next.source
      source.toolTip =
        next.isNative ? context.text("interface_detail.8e171fc12d") : profile.endpoint
      name.stringValue = next.name
      name.toolTip = next.name
      missing.isHidden = !next.missingKey
      if displayedMultiplier != next.multiplier {
        displayedMultiplier = next.multiplier
        for child in multiplier.arrangedSubviews {
          multiplier.removeArrangedSubview(child)
          child.removeFromSuperview()
        }
        multiplier.addArrangedSubview(PaxFeatureTag(next.multiplier, tone: .blue))
      }
      period.apply(
        next.periodUsage, ready: self.store.periodUsageReady, showFees: true, context: context)
      lifetime.apply(
        next.lifetimeUsage, ready: self.store.lifetimeUsageReady, showFees: true, context: context)
    }
    row.apply(r, p)
    return row
  }

  private func applyButtons(_ p: AIProfile) -> [NSView] {
    store.configurableClients.map { client in
      let binding = store.bindings[client]
      let owns = binding?.profileID == p.id
      let applied = owns && binding?.state == "applied"
      let supported =
        store.configuration.available && (p.kind != "native" || p.nativeCLI == client)
      let shortName: String
      switch client {
      case .claude: shortName = "Claude"
      case .codex: shortName = "Codex"
      case .harness: shortName = "DSH"
      case .antigravity: shortName = "Antigravity"
      }
      let button = actions.button(shortName, enabled: supported) { [weak self] in
        guard let self else { return }
        self.presentWorkbenchModal(
          AIBindingController(store: self.store, client: client, targetProfile: p.id))
      }
      button.trailingGlyph = applied ? "✓" : owns ? "!" : "+"
      let label =
        owns
        ? AIProfilesStore.stateLabel(binding, context: self.interfaceCopy.context)
        : (["ready", "applied", "pending", "changed"].contains(binding?.state ?? "")
          ? self.interfaceCopy.text("interface_detail.ac9747ed16")
          : AIProfilesStore.stateLabel(binding, context: self.interfaceCopy.context))
      let text = supported ? label : self.interfaceCopy.text("interface_detail.7330fedf83")
      button.setAccessibilityLabel(client.rawValue + "，" + text)
      button.toolTip = client.rawValue + " · " + text
      button.image = AIClientMark.image(client)
      button.leadingAligned = true
      button.capsuleHeight = 38
      button.widthAnchor.constraint(equalToConstant: applyButtonWidth).isActive = true
      button.compactStyle = applied ? .selected : .bordered
      contentRefreshers.append { [weak self, weak button] in
        guard let self, let button,
          let current = self.store.profiles.first(where: { $0.id == p.id })
        else { return }
        let binding = self.store.bindings[client]
        let owns = binding?.profileID == p.id
        let applied = owns && binding?.state == "applied"
        let supported =
          self.store.configuration.available
          && (current.kind != "native" || current.nativeCLI == client)
        button.isEnabled = supported
        button.trailingGlyph = applied ? "✓" : owns ? "!" : "+"
        let label =
          owns
          ? AIProfilesStore.stateLabel(binding, context: self.interfaceCopy.context)
          : (["ready", "applied", "pending", "changed"].contains(binding?.state ?? "")
            ? self.interfaceCopy.text("interface_detail.ac9747ed16")
            : AIProfilesStore.stateLabel(binding, context: self.interfaceCopy.context))
        let text = supported ? label : self.interfaceCopy.text("interface_detail.7330fedf83")
        button.setAccessibilityLabel(client.rawValue + "，" + text)
        button.toolTip = client.rawValue + " · " + text
        button.compactStyle = applied ? .selected : .bordered
      }
      return button
    }
  }
}
