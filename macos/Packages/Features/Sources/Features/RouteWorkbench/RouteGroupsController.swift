import AppKit
import Observation
import PaxUI

/// 左侧分组、右侧可搜索成员；所有改动留在草稿，保存一次应用。
@MainActor
final class RouteGroupsController: RouteModalController, NSPopoverDelegate {
  override var localizedTitleKey: String? { "modal.route.groups" }
  override var localizedSubtitleKey: String? { "modal.route.groups_note" }

  let store: RouteWorkbenchStore
  var expectedRevision: UInt64
  var saving = false
  var working: [RouteWorkbenchStore.Group]
  var selectedID: String?
  var removed: (RouteWorkbenchStore.Group, Int)?
  private let search = PaxRouteSearchField()
  private lazy var deleteSourceButton = PaxRouteButton(
    self.interfaceCopy.text("rules_ui.delete_subscription"), symbol: "trash", style: .quiet)
  private lazy var refreshSourceButton = PaxRouteButton(
    self.interfaceCopy.text("routes.refresh"), symbol: "arrow.clockwise", style: .quiet)
  private let count = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private lazy var undo = PaxRouteButton(
    self.interfaceCopy.text("rules_ui.undo_deletion"), symbol: "arrow.uturn.backward", style: .quiet
  )
  lazy var new = PaxRouteButton(
    self.interfaceCopy.text("action.routes.new_group"), symbol: "plus", style: .secondary)
  private var namePopover: NSPopover?
  let destructiveTitles = PaxMenuDestructiveTitles()
  private lazy var groups = PaxDataTable(
    columns: [.init(self.interfaceCopy.text("action.routes.group"), width: 0, sortable: false)],
    metrics: .init(
      rowHeight: PaxTheme.Table.compactRow, headerHeight: 0, rowSeparators: false,
      selectionInset: PaxTheme.Table.selectionInset), selection: .single)
  private lazy var members = PaxDataTable(
    columns: [
      .init(
        "", width: PaxTheme.Routes.choiceWidth, growth: 0, inset: PaxTheme.Spacing.xs,
        sortable: false),
      .init(
        self.interfaceCopy.text("navigation.routes"), width: PaxTheme.Routes.nameMinimum, growth: 3,
        symbol: "point.3.connected.trianglepath.dotted", sortable: false),
      .init(
        self.interfaceCopy.text("logs.connection"), width: PaxTheme.Groups.connectionWidth,
        growth: 1.5, symbol: "link", sortable: false
      ),
      .init(
        self.interfaceCopy.text("rules_ui.features"), width: PaxTheme.Groups.featureWidth,
        growth: 0.8, symbol: "tag", sortable: false),
    ],
    metrics: .init(rowHeight: PaxTheme.Table.memberRow, headerHeight: PaxTheme.Table.compactHeader),
    selection: .browse)
  private let body = NSStackView()
  private let editor = NSStackView()
  private let subscriptionActions = NSStackView()
  private var visibleIDs: [String] = []
  var displayedGroups: [RouteWorkbenchStore.Group] { store.subscriptionGroups + working }
  var selected: RouteWorkbenchStore.Group? { displayedGroups.first { $0.id == selectedID } }
  init(store: RouteWorkbenchStore) {
    self.store = store
    expectedRevision = store.catalogRevision
    working = store.groups
    selectedID = store.groups.first?.id ?? store.subscriptionGroups.first?.id
    super.init(title: "管理分组", subtitle: "一条线路可加入多个分组；保存后应用。", symbol: "folder")
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    super.loadView()
    new.controlSize = .small
    new.imagePosition = .imageOnly
    new.target = self
    new.action = #selector(newGroup)
    new.setAccessibilityLabel(self.interfaceCopy.text("action.routes.new_group"))
    new.setAccessibilityIdentifier("group-new")
    new.identifier = .init("group-new")
    undo.target = self
    undo.action = #selector(restoreGroup)
    let sidebarTitle = self.interfaceCopy.label(
      "action.routes.group", font: PaxTheme.Typography.caption(), secondary: true)
    let sidebarHeading = NSStackView(views: [sidebarTitle, NSView(), new])
    sidebarHeading.heightAnchor.constraint(equalToConstant: PaxTheme.Routes.fieldHeight).isActive =
      true
    let sidebar = NSStackView(views: [sidebarHeading, groups, undo])
    sidebar.orientation = .vertical
    sidebar.alignment = .leading
    sidebar.spacing = PaxTheme.Spacing.tight
    for child in sidebar.arrangedSubviews {
      child.widthAnchor.constraint(equalTo: sidebar.widthAnchor).isActive = true
    }
    let sidebarWidth = sidebar.widthAnchor.constraint(equalToConstant: PaxTheme.Groups.sidebar)
    sidebarWidth.priority = .defaultHigh
    sidebarWidth.isActive = true
    sidebar.widthAnchor.constraint(greaterThanOrEqualToConstant: PaxTheme.Groups.sidebarMinimum)
      .isActive = true
    search.placeholderString = self.interfaceCopy.text("interface_detail.eb500bacce")
    search.setAccessibilityLabel(self.interfaceCopy.text("interface_detail.eb500bacce"))
    search.sendsSearchStringImmediately = true
    search.target = self
    search.action = #selector(searchChanged)
    search.setAccessibilityIdentifier("group-search")
    count.alignment = .right
    count.setContentHuggingPriority(.required, for: .horizontal)
    count.setContentCompressionResistancePriority(.required, for: .horizontal)
    deleteSourceButton.target = self
    deleteSourceButton.action = #selector(deleteSelectedSubscription)
    deleteSourceButton.setAccessibilityIdentifier("delete-subscription")
    refreshSourceButton.target = self
    refreshSourceButton.action = #selector(refreshSelectedSubscription)
    refreshSourceButton.showsDisclosure = true
    refreshSourceButton.setAccessibilityIdentifier("group-refresh-subscription")
    let toolbar = NSStackView(views: [search, NSView(), count])
    toolbar.alignment = .centerY
    let searchWidth = search.widthAnchor.constraint(equalToConstant: PaxTheme.Groups.searchWidth)
    searchWidth.priority = .defaultHigh
    searchWidth.isActive = true
    toolbar.spacing = PaxTheme.Spacing.tight
    search.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    subscriptionActions.alignment = .centerY
    subscriptionActions.spacing = PaxTheme.Spacing.tight
    for button in [refreshSourceButton, deleteSourceButton] {
      subscriptionActions.addArrangedSubview(button)
    }
    subscriptionActions.addArrangedSubview(NSView())
    editor.orientation = .vertical
    editor.alignment = .leading
    editor.spacing = PaxTheme.Spacing.tight
    for child in [toolbar, subscriptionActions, members] {
      editor.addArrangedSubview(child)
      child.widthAnchor.constraint(equalTo: editor.widthAnchor).isActive = true
    }
    body.orientation = .horizontal
    body.alignment = .top
    body.spacing = PaxTheme.Groups.gap
    body.addArrangedSubview(sidebar)
    body.addArrangedSubview(editor)
    sidebar.heightAnchor.constraint(equalTo: body.heightAnchor).isActive = true
    editor.heightAnchor.constraint(equalTo: body.heightAnchor).isActive = true
    body.heightAnchor.constraint(greaterThanOrEqualToConstant: PaxTheme.Groups.bodyHeight)
      .isActive = true
    pane.add(body)
    groups.cellProvider = { [weak self] id, _ in self?.groupCell(id) }
    groups.onSelect = { [weak self] id in
      guard let self, let id else { return }
      self.selectedID = id
      self.render()
    }
    groups.canMoveRow = { [weak self] id in self?.working.contains(where: { $0.id == id }) == true }
    groups.onMove = { [weak self] id, index in
      guard let self, self.working.contains(where: { $0.id == id }) else { return }
      self.move(id, to: max(0, index - self.store.subscriptionGroups.count))
    }
    members.onActivate = { [weak self] id in self?.toggle(id) }
    members.onRowClick = { [weak self] id in self?.toggle(id) }
    groups.menuForRow = { [weak self] id in self?.groupMenu(id) }
    members.cellProvider = { [weak self] id, index in self?.memberCell(id, column: index) }
    pane.setActions([
      button(self.interfaceCopy.text("common.cancel"), #selector(close)),
      button(self.interfaceCopy.text("action.common.save"), #selector(save), primary: true),
    ])
    render()
    observeSubscriptions()
  }
  private func observeSubscriptions() {
    withObservationTracking {
      _ = store.subscriptionGroups
      _ = store.nodes
      for group in store.subscriptionGroups {
        _ = store.subscriptionStatus(group.id, context: self.interfaceCopy.context)
      }
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in
        self?.render()
        self?.observeSubscriptions()
      }
    }
  }
  @objc private func refreshSelectedSubscription() {
    guard !saving, let selectedID else { return }
    SubscriptionRefreshMenu.present(
      store: store, groupID: selectedID, from: refreshSourceButton,
      context: self.interfaceCopy.context)
  }
  private func groupCell(_ id: String) -> NSView? {
    guard let group = displayedGroups.first(where: { $0.id == id }) else { return nil }
    let active = id == selectedID
    let title = PaxText.label(
      group.displayTitle,
      font: PaxTheme.Typography.label(
        size: PaxTheme.Table.font, weight: active ? .semibold : .medium))
    title.textColor = active ? PaxTheme.Routes.selectedBlue : PaxTheme.Table.text
    title.lineBreakMode = .byTruncatingTail
    title.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    let stack = NSStackView(views: [title])
    if group.kind == .subscription {
      let icon = PaxIcon.view(
        "link", size: PaxTheme.Control.navigationIcon)
      icon.setAccessibilityElement(false)
      icon.contentTintColor = active ? PaxTheme.Routes.selectedBlue : PaxTheme.Table.muted
      stack.insertArrangedSubview(icon, at: 0)
    }
    stack.spacing = PaxTheme.Spacing.tight
    stack.setAccessibilityLabel(
      interfaceCopy.text(
        group.kind == .subscription ? "routes.subscription_group_ax" : "routes.custom_group_ax",
        ["name": group.name, "count": group.members.count.formatted()]))
    return stack
  }
  func renderSidebar() {
    groups.apply(
      rows: displayedGroups.map {
        .init(
          id: $0.id, values: [$0.displayTitle])
      }, emptyMessage: self.interfaceCopy.text("interface_detail.8fa49b1fec"),
      selectedID: selectedID)
    undo.isHidden = removed == nil
  }
  func render() {
    renderSidebar()
    let group = selected
    subscriptionActions.isHidden = group?.kind != .subscription
    deleteSourceButton.isEnabled = !saving && store.canDeleteSubscriptions
    refreshSourceButton.isEnabled = !saving && selectedID.map(store.canRefreshSubscription) == true
    refreshSourceButton.toolTip = selectedID.map {
      store.subscriptionStatus($0, context: interfaceCopy.context)
    }
    if !saving, group?.kind == .subscription, let selectedID {
      pane.status.stringValue = store.subscriptionStatus(
        selectedID, context: self.interfaceCopy.context)
    }
    search.isEnabled = group != nil
    renderMembers()
  }
  private func renderMembers() {
    let query = search.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
    let group = selected
    visibleIDs = store.nodes.filter { node in
      !node.hidden && group != nil
        && (group?.kind != .subscription || group?.members.contains(node.id) == true)
        && (query.isEmpty
          || [node.name, node.protocolName, node.server].joined(separator: " ")
            .localizedCaseInsensitiveContains(query))
    }.map(\.id)
    count.stringValue =
      group?.kind == .subscription
      ? self.interfaceCopy.text(
        "interface_detail.d15d9153e7", ["arg0": String(describing: visibleIDs.count)])
      : self.interfaceCopy.text(
        "interface_detail.dfe1511732",
        [
          "arg0": String(describing: group?.members.count ?? 0),
          "arg1": String(describing: visibleIDs.count),
        ])
    members.apply(
      rows: visibleIDs.map { .init(id: $0, values: []) },
      emptyMessage: group == nil
        ? self.interfaceCopy.text("interface_detail.c56967e4a7")
        : self.interfaceCopy.text("interface_detail.a058369d32"))
  }
  private func memberCell(_ id: String, column: Int) -> NSView? {
    guard let node = store.node(id) else { return nil }
    if column == 0 {
      if selected?.kind == .subscription {
        let icon = PaxIcon.view("checkmark", size: PaxTheme.Table.font)
        icon.setAccessibilityLabel(self.interfaceCopy.text("action.routes.subscription_members"))
        return icon
      }
      let check = NSButton(checkboxWithTitle: "", target: self, action: #selector(toggleCheck(_:)))
      check.identifier = .init(id)
      check.focusRingType = .none
      check.state =
        working.first(where: { $0.id == selectedID })?.members.contains(id) == true ? .on : .off
      check.setAccessibilityLabel(
        self.interfaceCopy.text("interface_detail.fa2c54768f") + node.name)
      return check
    }
    if column == 3 {
      let info = store.info(id, context: self.interfaceCopy.context)
      let stack = NSStackView()
      stack.spacing = PaxTheme.Spacing.xs
      if info.cfIP != nil {
        stack.addArrangedSubview(PaxFeatureTag("CF", tone: .blue))
      }
      if info.chain != nil {
        stack.addArrangedSubview(
          PaxFeatureTag(self.interfaceCopy.text("action.routes.chain_proxy"), tone: .purple))
      }
      return stack
    }
    let text =
      column == 1
      ? node.name
      : [node.protocolName.uppercased(), node.transport.uppercased(), node.security.uppercased()]
        .joined(separator: " · ")
    let label = PaxText.label(
      text,
      font: column == 1
        ? PaxTheme.Typography.label(size: PaxTheme.Table.font) : PaxTheme.Typography.caption())
    label.lineBreakMode = .byTruncatingTail
    label.textColor = column == 1 ? PaxTheme.Table.text : PaxTheme.Table.muted
    return label
  }
  private func toggle(_ id: String) {
    guard !saving else { return }
    guard let index = working.firstIndex(where: { $0.id == selectedID }) else { return }
    if working[index].members.contains(id) {
      working[index].members.remove(id)
    } else {
      working[index].members.insert(id)
    }
    renderMembers()
    renderSidebar()
  }
  @objc private func toggleCheck(_ sender: NSButton) {
    if let id = sender.identifier?.rawValue { toggle(id) }
  }
  @objc private func searchChanged() { renderMembers() }
  @objc private func newGroup() { showNameEditor(groupID: nil) }
  func showNameEditor(groupID: String?) {
    guard !saving, new.window != nil else { return }
    namePopover?.performClose(nil)
    let current = groupID.flatMap { id in working.first { $0.id == id } }
    guard groupID == nil || current != nil else { return }
    let popover = NSPopover()
    let editor = RouteGroupNameController(
      mode: current.map { .rename($0.name) } ?? .create, context: interfaceCopy.context
    ) {
      [weak self] value in
      guard let self else { return }
      let name = try RouteWorkbenchStore.validatedGroupName(
        value, existing: Set(self.working.filter { $0.id != groupID }.map(\.name)))
      if let groupID {
        guard let index = self.working.firstIndex(where: { $0.id == groupID }) else {
          throw RouteWorkbenchStore.EditError(
            self.interfaceCopy.text("interface_detail.46c3b7cfa9"))
        }
        self.working[index].name = name
      } else {
        let id = UUID().uuidString
        self.working.append(.init(id: id, name: name, members: []))
        self.selectedID = id
      }
      self.render()
    } dismiss: { [weak popover] in
      popover?.performClose(nil)
    }
    popover.contentViewController = editor
    popover.behavior = .transient
    popover.applyMotionPreference()
    popover.delegate = self
    namePopover = popover
    if let groupID, let rect = groups.revealRow(groupID) {
      popover.show(relativeTo: rect, of: groups, preferredEdge: .maxX)
    } else {
      popover.show(relativeTo: new.bounds, of: new, preferredEdge: .maxX)
    }
  }
  func popoverDidClose(_ notification: Notification) {
    guard let closed = notification.object as? NSPopover, closed === namePopover else { return }
    namePopover = nil
    view.window?.makeFirstResponder(new)
  }
  override func cancelOperation(_ sender: Any?) {
    if namePopover?.isShown == true {
      namePopover?.performClose(sender)
    } else {
      super.cancelOperation(sender)
    }
  }
  override func close() {
    guard !saving else { return }
    namePopover?.performClose(nil)
    super.close()
  }
  @objc private func save() {
    guard !saving else { return }
    if !store.isLive {
      do {
        try store.replaceGroups(working)
        close()
      } catch { showError(error) }
      return
    }
    saving = true
    new.isEnabled = false
    pane.status.stringValue = self.interfaceCopy.text("interface_detail.82bc723515")
    pane.status.isHidden = false
    Task { [self] in
      do {
        try await store.saveGroups(working, expectedRevision: expectedRevision)
        saving = false
        close()
      } catch {
        saving = false
        new.isEnabled = true
        showError(error)
      }
    }
  }
}
