import AppKit
import PaxCore
import PaxUI

private let tileSize: CGFloat = 30

/// Preserve the existing CLI buttons; column sizing belongs to PaxDataTable.
private let applyButtonWidth: CGFloat = 122
private let applyWidth: CGFloat = applyButtonWidth * 2 + PaxTheme.Spacing.tight
private let moreWidth: CGFloat = 40
private let profileRowHeight: CGFloat = 88

/// Two-line model summary; a click opens the Profile detail like the rest of the row.
@MainActor
final class AIProfileModelsLink: NSView {
  var onOpen: (() -> Void)?
  private let firstLabel = PaxText.tableLabel("", secondary: true)
  private let secondLabel = PaxText.tableLabel("", secondary: true)
  init(first: String, second: String?) {
    super.init(frame: .zero)
    let lines = [firstLabel, secondLabel]
    firstLabel.stringValue = first
    secondLabel.stringValue = second ?? ""
    secondLabel.isHidden = second == nil
    for line in lines {
      line.lineBreakMode = .byTruncatingMiddle
      line.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    }
    let stack = AIUsageLayout.stack(lines)
    stack.spacing = PaxTheme.Spacing.xs
    stack.translatesAutoresizingMaskIntoConstraints = false
    addSubview(stack)
    NSLayoutConstraint.activate([
      stack.leadingAnchor.constraint(equalTo: leadingAnchor),
      stack.trailingAnchor.constraint(equalTo: trailingAnchor),
      stack.topAnchor.constraint(equalTo: topAnchor),
      stack.bottomAnchor.constraint(equalTo: bottomAnchor),
    ])
    setAccessibilityElement(true)
    setAccessibilityRole(.button)
    setAccessibilityLabel([first, second].compactMap { $0 }.joined(separator: "，"))
    toolTip = ([first] + (second.map { [$0] } ?? [])).joined(separator: "\n")
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override var intrinsicContentSize: NSSize {
    NSSize(
      width: NSView.noIntrinsicMetric,
      height: firstLabel.intrinsicContentSize.height
        + (secondLabel.isHidden ? 0 : PaxTheme.Spacing.xs + secondLabel.intrinsicContentSize.height)
    )
  }
  func apply(first: String, second: String?) {
    firstLabel.stringValue = first
    secondLabel.stringValue = second ?? ""
    secondLabel.isHidden = second == nil
    invalidateIntrinsicContentSize()
    setAccessibilityLabel([first, second].compactMap { $0 }.joined(separator: "，"))
    toolTip = [first, second].compactMap { $0 }.joined(separator: "\n")
  }
  override func mouseDown(with event: NSEvent) { onOpen?() }
  override func resetCursorRects() { addCursorRect(bounds, cursor: .pointingHand) }
}

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
      .init("Profile", width: 160, growth: 3, symbol: "person.crop.circle", sortable: false),
      .init(
        "应用到", width: applyWidth + PaxTheme.Spacing.xs * 2, growth: 0,
        symbol: "terminal", inset: PaxTheme.Spacing.xs, sortable: false),
      .init("模型", width: 120, growth: 2, symbol: "sparkles", sortable: false),
      .init(
        store.period.rawValue + "费用", width: 124, growth: 1, alignment: .left,
        symbol: "creditcard", sortable: false),
      .init("Token", width: 80, numeric: true, growth: 1, alignment: .center, sortable: false),
      .init("请求", width: 68, numeric: true, growth: 1, alignment: .center, sortable: false),
      .init("缓存率", width: 80, numeric: true, growth: 1, alignment: .center, sortable: false),
      .init(
        "", width: moreWidth + PaxTheme.Spacing.xs * 2, growth: 0,
        alignment: .center, inset: PaxTheme.Spacing.xs, sortable: false),
    ]
    let table = PaxDataTable(
      columns: columns, metrics: .init(rowHeight: profileRowHeight), selection: .browse)
    // The two full CLI buttons establish the actual minimum, as in the prior record list.
    table.widthAnchor.constraint(
      greaterThanOrEqualToConstant: columns.reduce(0) { $0 + $1.width }
    ).isActive = true
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
      let records = self.store.profileRecords
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
        emptyMessage: "暂无 Profile", reloading: [])
    }
    contentRefreshers.append(update)
    update()
    table.setTableIdentifier("ai-profile-list")
    return table
  }

  private func recordRow(_ r: AIProfileRecord, _ p: AIProfile) -> AIProfileTableRecord {
    let status = PaxBadge(title: r.statusText, tone: r.statusTone)
    let source = PaxText.tableLabel(r.source, secondary: true)
    source.lineBreakMode = .byTruncatingMiddle
    source.toolTip = r.isNative ? "保留账号及原生设置，仅供对应 CLI 使用" : p.endpoint
    var first: [NSView] = [status]
    let missing = PaxText.tableLabel("未配置凭据", secondary: true)
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
    let models = AIProfileModelsLink(first: r.modelsLine, second: r.topModelLine)
    models.setAccessibilityIdentifier("ai-profile-models-" + r.id)
    models.onOpen = { [weak self] in
      guard let self else { return }
      guard let current = self.store.profiles.first(where: { $0.id == p.id }) else { return }
      self.presentWorkbenchModal(
        AIProfileEditor(store: self.store, profile: current))
    }
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
    more.toolTip = "更多操作"
    more.setAccessibilityLabel("Profile 更多操作")
    more.widthAnchor.constraint(equalToConstant: moreWidth).isActive = true
    more.setContentHuggingPriority(.required, for: .horizontal)
    more.setContentCompressionResistancePriority(.required, for: .horizontal)
    let metrics = metricColumns(r)
    var displayed = r
    return AIProfileTableRecord(cells: [identity, apply, models] + metrics + [more]) {
      next, profile in
      guard displayed != next else { return }
      let oldMultiplier = displayed.multiplier
      displayed = next
      status.apply(title: next.statusText, tone: next.statusTone)
      source.stringValue = next.source
      source.toolTip = next.isNative ? "保留账号及原生设置，仅供对应 CLI 使用" : profile.endpoint
      name.stringValue = next.name
      name.toolTip = next.name
      missing.isHidden = !next.missingKey
      models.apply(first: next.modelsLine, second: next.topModelLine)
      if oldMultiplier != next.multiplier {
        for child in multiplier.arrangedSubviews {
          multiplier.removeArrangedSubview(child)
          child.removeFromSuperview()
        }
        multiplier.addArrangedSubview(PaxFeatureTag(next.multiplier, tone: .blue))
      }
      if let cost = metrics.first {
        AIUsageCostDisplay.apply(
          cost, official: next.costText, channel: next.channelCostText, stacked: true,
          lineSpacing: PaxTheme.Spacing.xs)
      }
      for (label, value) in zip(
        metrics.dropFirst(), [next.tokensText, next.requestsText, next.cacheText])
      {
        label.stringValue = value
        label.toolTip = value
      }
    }
  }

  /// Values use the same column alignment as their native headers.
  private func metricColumns(_ r: AIProfileRecord) -> [NSTextField] {
    let cells: [(String, String, AICLICardLines.Run.Role)] = [
      ("cost", r.costText, .cost), ("token", r.tokensText, .token),
      ("request", r.requestsText, .request), ("cache", r.cacheText, .cache),
    ]
    return cells.map { cell in
      let value = PaxText.tableLabel(
        cell.1, numeric: true, alignment: cell.0 == "cost" ? .left : .center)
      if cell.0 == "cost" {
        AIUsageCostDisplay.apply(
          value, official: r.costText, channel: r.channelCostText, stacked: true,
          lineSpacing: PaxTheme.Spacing.xs)
      } else if cell.0 != "token" {
        value.textColor = aiMetricColor(cell.2)
      }
      value.setAccessibilityIdentifier("ai-profile-metric-" + r.id + "-" + cell.0)
      return value
    }
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
      }
      let button = actions.button(shortName, enabled: supported) { [weak self] in
        guard let self else { return }
        self.presentWorkbenchModal(
          AIBindingController(store: self.store, client: client, targetProfile: p.id))
      }
      button.trailingGlyph = applied ? "✓" : owns ? "!" : "+"
      let label =
        owns
        ? AIProfilesStore.stateLabel(binding)
        : (["ready", "applied", "pending", "changed"].contains(binding?.state ?? "")
          ? "可切换" : AIProfilesStore.stateLabel(binding))
      let text = supported ? label : "仅供对应 CLI 使用"
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
          ? AIProfilesStore.stateLabel(binding)
          : (["ready", "applied", "pending", "changed"].contains(binding?.state ?? "")
            ? "可切换" : AIProfilesStore.stateLabel(binding))
        let text = supported ? label : "仅供对应 CLI 使用"
        button.setAccessibilityLabel(client.rawValue + "，" + text)
        button.toolTip = client.rawValue + " · " + text
        button.compactStyle = applied ? .selected : .bordered
      }
      return button
    }
  }
}
