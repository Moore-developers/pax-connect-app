import AppKit
import PaxCore
import PaxUI

extension WorkbenchConnectionGroupsView {
  func targetName(_ group: TrafficConnectionGroup) -> String {
    group.kind == "unknown" ? context.text("connection_groups.unknown") : group.target
  }
  static func exitID(_ value: TrafficExit) -> String {
    value.direction + "\t" + value.routeID + "\t" + value.routeName
  }
  static func ruleID(_ value: TrafficBasis) -> String {
    String(value.source.utf8.count) + ":" + value.source + value.rule
  }
  func exitName(_ value: TrafficExit) -> String {
    TrafficFormatting.actualConnection(
      direction: value.direction, route: value.routeName, context: context)
  }
  func ruleName(_ value: TrafficBasis) -> String {
    guard !value.rule.isEmpty, value.rule != "未记录" else {
      return context.text("connection_groups.unrecorded")
    }
    if value.rule == "final" || value.rule == "未命中规则" {
      return context.text("connection_groups.unmatched")
    }
    return TrafficRuleFormatting.package(rule: value.rule, source: value.source, context: context)
      + " · "
      + TrafficRuleFormatting.condition(rule: value.rule, source: value.source, context: context)
  }
  func hover(_ id: String) -> NSView? {
    guard !ruleEditorOpen, let group = shown.first(where: { $0.id == id }) else { return nil }
    return WorkbenchConnectionGroupReadout(
      group: group, context: context, screen: window?.screen,
      actionTitle: ruleActionTitle(group),
      initialTab: selectedHoverTab,
      onSelectTab: { [weak self] tab in self?.selectedHoverTab = tab },
      onRule: { [weak self] in
        self?.readout.close()
        self?.onRule(group)
      })
  }
  func menu(_ id: String) -> NSMenu? {
    guard let group = shown.first(where: { $0.id == id }) else {
      return nil
    }
    let menu = NSMenu()
    let info = NSMenuItem(
      title: context.text("connection_groups.show_details"),
      action: #selector(menuDetails(_:)), keyEquivalent: "")
    info.target = self
    info.representedObject = id
    menu.addItem(info)
    guard group.kind != "unknown" else { return menu }
    let item = NSMenuItem(
      title: ruleActionTitle(group),
      action: #selector(menuRule(_:)), keyEquivalent: "")
    item.target = self
    item.representedObject = id
    menu.addItem(item)
    return menu
  }
  @objc private func menuDetails(_ sender: NSMenuItem) {
    guard let id = sender.representedObject as? String else { return }
    table.presentReadout(for: id)
  }
  @objc private func menuRule(_ sender: NSMenuItem) {
    guard let id = sender.representedObject as? String,
      let group = shown.first(where: { $0.id == id })
    else { return }
    readout.close()
    onRule(group)
  }
  static func targetID(_ row: WorkbenchTrafficRecord) -> String {
    if let host = row.domain, !host.isEmpty {
      return "domain:" + host.lowercased().trimmingCharacters(in: CharacterSet(charactersIn: "."))
    }
    let ip = row.rawIP ?? row.ip
    return ip.isEmpty ? "unknown" : "ip:" + ip
  }
  func visibleGroups() -> [TrafficConnectionGroup] {
    let term = searchText.trimmingCharacters(in: .whitespacesAndNewlines)
    return loaded.filter { group in
      (term.isEmpty || targetName(group).localizedCaseInsensitiveContains(term))
        && (selectedExit.isEmpty || group.exits.contains { Self.exitID($0) == selectedExit })
        && (selectedRule.isEmpty || group.rules.contains { Self.ruleID($0) == selectedRule })
    }.sorted { a, b in
      let order: ComparisonResult
      switch groupSortColumn {
      case 0: order = targetName(a).localizedStandardCompare(targetName(b))
      case 2:
        let av = a.upload + a.download
        let bv = b.upload + b.download
        order = av == bv ? .orderedSame : av < bv ? .orderedAscending : .orderedDescending
      case 3:
        order = a.exits.map(exitName).joined(separator: "、").localizedStandardCompare(
          b.exits.map(exitName).joined(separator: "、"))
      default:
        order =
          a.connections == b.connections
          ? .orderedSame
          : a.connections < b.connections ? .orderedAscending : .orderedDescending
      }
      if order == .orderedSame { return a.lastAt == b.lastAt ? a.id < b.id : a.lastAt > b.lastAt }
      return groupAscending ? order == .orderedAscending : order == .orderedDescending
    }
  }
  func demoPage() -> TrafficDetail {
    var result = TrafficDetail()
    result.retainedConnectionCount = UInt64(demoRows.count)
    let rows = demoRows
    result.filteredConnectionCount = UInt64(rows.count)
    for (id, items) in Dictionary(grouping: rows, by: Self.targetID) {
      guard let first = items.first else { continue }
      var group = TrafficConnectionGroup()
      group.id = id
      group.kind = id.hasPrefix("domain:") ? "domain" : id.hasPrefix("ip:") ? "ip" : "unknown"
      group.target = id == "unknown" ? "" : String(id.dropFirst(id.hasPrefix("domain:") ? 7 : 3))
      group.connections = UInt64(items.count)
      group.upload = items.reduce(0) { $0 + $1.upload }
      group.download = items.reduce(0) { $0 + $1.download }
      group.bytesState = "complete"
      group.lastAt = items.map(\.started).max() ?? first.started
      group.rules = Self.demoRules(items)
      group.exits = Self.demoExits(items)
      Self.fillDemoEvidence(items, group: &group)
      result.connectionGroups.append(group)
    }
    result.targetGroupTotal = UInt64(result.connectionGroups.count)
    result.connectionRules = Self.demoRules(demoRows)
    result.connectionExits = Self.demoExits(demoRows)
    return result
  }
  private static func fillDemoEvidence(
    _ rows: [WorkbenchTrafficRecord], group: inout TrafficConnectionGroup
  ) {
    let valid = rows.filter { $0.establishmentMS != nil }.sorted {
      $0.started == $1.started ? $0.id < $1.id : $0.started < $1.started
    }
    group.validLatencyCount = UInt64(valid.count)
    group.recentResponses = valid.suffix(60).map {
      TrafficResponsePoint(
        connectionKey: $0.id, startedAt: $0.started, milliseconds: UInt64($0.establishmentMS ?? 0))
    }
    group.sources = Dictionary(grouping: rows) { $0.applicationID + "\0" + $0.process }.values
      .compactMap { values in
        guard let first = values.first else { return nil }
        var value = TrafficConnectionSource()
        value.applicationID = first.applicationID
        value.application = first.application
        value.process = first.process
        value.connections = UInt64(values.count)
        return value
      }.sorted { $0.applicationID + $0.process < $1.applicationID + $1.process }
    group.addresses = Dictionary(grouping: rows) { $0.connectionAddress + "\0" + $0.transport }
      .values.compactMap { values in
        guard let first = values.first else { return nil }
        var value = TrafficConnectionAddress()
        value.ip = first.rawIP ?? first.ip
        value.port = UInt32(first.port ?? 0)
        value.transport = first.transport
        value.connections = UInt64(values.count)
        return value
      }.sorted { $0.ip + $0.transport < $1.ip + $1.transport }
    group.results = Dictionary(grouping: rows) { row in
      row.failure != nil ? "failed" : row.stateUnknown ? "unknown" : row.ended ? "closed" : "active"
    }.map { state, values in
      var value = TrafficConnectionResult()
      value.state = state
      value.connections = UInt64(values.count)
      return value
    }.sorted { $0.state < $1.state }
  }
  private static func demoRules(_ rows: [WorkbenchTrafficRecord]) -> [TrafficBasis] {
    Dictionary(grouping: rows, by: \.ruleIdentity).values.compactMap { items in
      guard let first = items.first else { return nil }
      var value = TrafficBasis()
      value.rule = first.hit.condition
      value.source = first.hit.source
      value.connections = UInt64(items.count)
      return value
    }.sorted { $0.connections > $1.connections }
  }
  private static func demoExits(_ rows: [WorkbenchTrafficRecord]) -> [TrafficExit] {
    Dictionary(grouping: rows, by: \.exitIdentity).values.compactMap { items in
      guard let first = items.first else { return nil }
      var value = TrafficExit()
      value.direction = first.directionCode.isEmpty ? first.direction : first.directionCode
      value.routeID = first.routeID
      value.routeName = first.route
      value.connections = UInt64(items.count)
      return value
    }.sorted { $0.connections > $1.connections }
  }
}

/// Pinned header, KPI strip, and topic tabs above a compact scrollable tab body.
@MainActor private final class WorkbenchConnectionGroupReadout: NSStackView {
  private typealias Layout = WorkbenchConnectionReadoutLayout
  private let onRule: () -> Void
  private let onSelectTab: (Int) -> Void
  private let scroll = NSScrollView()
  private let tabDocuments: [NSView]
  private let latencyValueLabel: NSTextField
  private let latestLatency: String

  init(
    group: TrafficConnectionGroup, context: PresentationContext, screen: NSScreen?,
    actionTitle: String,
    initialTab: Int,
    onSelectTab: @escaping (Int) -> Void,
    onRule: @escaping () -> Void
  ) {
    self.onRule = onRule
    self.onSelectTab = onSelectTab
    let latest = WorkbenchConnectionGroupEvidence.latestLatencyText(group)
    self.latestLatency = latest
    let theme = PaxTheme.TrafficDetail.self
    let visible = (screen ?? NSScreen.main)?.visibleFrame.size ?? PaxTheme.Layout.minimumWindow
    let margin = PaxTheme.Spacing.roomy * 2 + PaxTheme.Spacing.card * 2
    let width = min(theme.hoverWidth, visible.width - margin)
    let scroller =
      NSScroller.preferredScrollerStyle == .legacy
      ? NSScroller.scrollerWidth(for: .regular, scrollerStyle: .legacy) : 0
    let bodyWidth = max(200, width - scroller)
    let metrics = Layout.metrics(group, latestLatency: latest, context: context)
    self.latencyValueLabel = metrics.latencyValue

    let latencyView = WorkbenchConnectionGroupEvidence.latency(group, context: context) {
      [weak latencyValue = metrics.latencyValue] hoverValue in
      latencyValue?.stringValue = hoverValue ?? latest
    }
    let routingTab = Self.tabDocument(
      Layout.stack(
        [
          WorkbenchConnectionGroupEvidence.exits(group, context: context),
          Layout.hairline(),
          WorkbenchConnectionGroupEvidence.rules(group, context: context, width: bodyWidth),
        ], gap: PaxTheme.Spacing.medium), width: bodyWidth)
    let latencyTab = Self.tabDocument(latencyView, width: bodyWidth)
    let diagnosticsTab = Self.tabDocument(
      WorkbenchConnectionGroupEvidence.diagnostics(group, context: context, width: bodyWidth),
      width: bodyWidth)
    self.tabDocuments = [routingTab, latencyTab, diagnosticsTab]

    super.init(frame: .zero)
    orientation = .vertical
    alignment = .leading
    spacing = 0
    setAccessibilityIdentifier("connection-group-hover")
    widthAnchor.constraint(equalToConstant: width).isActive = true

    let action: PaxRouteButton? =
      group.kind == "unknown"
      ? nil
      : {
        let button = PaxRouteButton(
          actionTitle, style: .secondary, target: self, action: #selector(setRule))
        button.isCapsule = true
        button.setAccessibilityIdentifier("connection-set-rule")
        return button
      }()
    let tabs = PaxTabs(
      [
        context.text("connection_groups.tab_routing"),
        context.text("connection_groups.tab_latency"),
        context.text("connection_groups.tab_details"),
      ], style: .tabs)
    tabs.distributesEqually = true
    tabs.heightAnchor.constraint(equalToConstant: PaxTheme.Dashboard.tabHeight).isActive = true
    let clampedTab = max(0, min(tabDocuments.count - 1, initialTab))
    tabs.selectedIndex = clampedTab
    tabs.onSelect = { [weak self] index in self?.selectTab(index) }

    let pinnedTop = Layout.stack(
      [
        Layout.header(group, context: context, action: action),
        metrics.view,
        tabs,
      ], gap: PaxTheme.Spacing.medium)
    addArrangedSubview(pinnedTop)
    pinnedTop.widthAnchor.constraint(equalTo: widthAnchor).isActive = true
    pinnedTop.layoutSubtreeIfNeeded()

    let preferredHeight = ceil(max(routingTab.fittingSize.height, latencyTab.fittingSize.height))
    let screenCap = max(
      theme.hoverBodyMinimumHeight,
      visible.height - margin - ceil(pinnedTop.fittingSize.height))
    let scrollHeight = min(preferredHeight, min(theme.hoverTabBodyMaxHeight, screenCap))

    if !group.recentResponses.isEmpty {
      latencyTab.heightAnchor.constraint(equalToConstant: scrollHeight).isActive = true
    }

    scroll.drawsBackground = false
    scroll.borderType = .noBorder
    scroll.hasVerticalScroller = true
    scroll.hasHorizontalScroller = false
    scroll.autohidesScrollers = true
    addArrangedSubview(scroll)
    scroll.widthAnchor.constraint(equalTo: widthAnchor).isActive = true
    scroll.heightAnchor.constraint(equalToConstant: scrollHeight).isActive = true
    applyTab(clampedTab)
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }

  private static func tabDocument(_ content: NSView, width: CGFloat) -> NSView {
    let document = FlippedDocumentView()
    document.translatesAutoresizingMaskIntoConstraints = false
    content.translatesAutoresizingMaskIntoConstraints = false
    document.addSubview(content)
    NSLayoutConstraint.activate([
      document.widthAnchor.constraint(equalToConstant: width),
      content.leadingAnchor.constraint(equalTo: document.leadingAnchor),
      content.trailingAnchor.constraint(equalTo: document.trailingAnchor),
      content.topAnchor.constraint(
        equalTo: document.topAnchor, constant: PaxTheme.Spacing.medium),
      content.bottomAnchor.constraint(equalTo: document.bottomAnchor),
    ])
    document.layoutSubtreeIfNeeded()
    return document
  }

  private func selectTab(_ index: Int) {
    onSelectTab(index)
    applyTab(index)
  }

  private func applyTab(_ index: Int) {
    guard tabDocuments.indices.contains(index) else { return }
    if index != 1 { latencyValueLabel.stringValue = latestLatency }
    scroll.documentView = tabDocuments[index]
    scroll.contentView.scroll(to: .zero)
    scroll.reflectScrolledClipView(scroll.contentView)
  }

  @objc private func setRule() { onRule() }
}
