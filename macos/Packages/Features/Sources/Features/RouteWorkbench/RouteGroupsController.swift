import AppKit
import Observation
import PaxUI

/// 左侧分组、右侧可搜索成员；所有改动留在草稿，保存一次应用。
@MainActor
final class RouteGroupsController: RouteModalController, NSPopoverDelegate {
  private let store: RouteWorkbenchStore
  private var expectedRevision: UInt64
  private var saving = false
  private var working: [RouteWorkbenchStore.Group]
  private var selectedID: String?
  private var removed: (RouteWorkbenchStore.Group, Int)?
  private let search = PaxRouteSearchField()
  private let deleteSourceButton = PaxRouteButton("删除订阅…", symbol: "trash", style: .quiet)
  private let refreshSourceButton = PaxRouteButton("刷新订阅", symbol: "arrow.clockwise", style: .quiet)
  private let count = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private let undo = PaxRouteButton("撤销删除", symbol: "arrow.uturn.backward", style: .quiet)
  private let new = PaxRouteButton("新建分组", symbol: "plus", style: .secondary)
  private var namePopover: NSPopover?
  private let destructiveTitles = PaxMenuDestructiveTitles()
  private let groups = PaxDataTable(
    columns: [.init("分组", width: 0, sortable: false)],
    metrics: .init(
      rowHeight: PaxTheme.Table.compactRow, headerHeight: 0, rowSeparators: false,
      selectionInset: PaxTheme.Table.selectionInset), selection: .single)
  private let members = PaxDataTable(
    columns: [
      .init(
        "", width: PaxTheme.Routes.choiceWidth, growth: 0, inset: PaxTheme.Spacing.xs,
        sortable: false),
      .init(
        "线路", width: PaxTheme.Routes.nameMinimum, growth: 3,
        symbol: "point.3.connected.trianglepath.dotted", sortable: false),
      .init(
        "连接方式", width: PaxTheme.Groups.connectionWidth, growth: 1.5, symbol: "link", sortable: false
      ),
      .init("特性", width: PaxTheme.Groups.featureWidth, growth: 0.8, symbol: "tag", sortable: false),
    ],
    metrics: .init(rowHeight: PaxTheme.Table.memberRow, headerHeight: PaxTheme.Table.compactHeader),
    selection: .browse)
  private let body = NSStackView()
  private let editor = NSStackView()
  private var visibleIDs: [String] = []
  private var displayedGroups: [RouteWorkbenchStore.Group] { store.subscriptionGroups + working }
  private var selected: RouteWorkbenchStore.Group? { displayedGroups.first { $0.id == selectedID } }
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
    new.setAccessibilityLabel("新建分组")
    new.setAccessibilityIdentifier("group-new")
    new.identifier = .init("group-new")
    undo.target = self
    undo.action = #selector(restoreGroup)
    let sidebarTitle = PaxText.label("分组", font: PaxTheme.Typography.caption(), secondary: true)
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
    search.placeholderString = "搜索线路、协议或地址"
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
    let toolbar = NSStackView(views: [
      search, NSView(), refreshSourceButton, deleteSourceButton, count,
    ])
    toolbar.alignment = .centerY
    let searchWidth = search.widthAnchor.constraint(equalToConstant: PaxTheme.Groups.searchWidth)
    searchWidth.priority = .defaultHigh
    searchWidth.isActive = true
    toolbar.spacing = PaxTheme.Spacing.tight
    search.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    editor.orientation = .vertical
    editor.alignment = .leading
    editor.spacing = PaxTheme.Spacing.tight
    for child in [toolbar, members] {
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
    pane.setActions([button("取消", #selector(close)), button("保存", #selector(save), primary: true)])
    render()
    observeSubscriptions()
  }
  private func observeSubscriptions() {
    withObservationTracking {
      _ = store.subscriptionGroups
      _ = store.nodes
      for group in store.subscriptionGroups { _ = store.subscriptionStatus(group.id) }
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in
        self?.render()
        self?.observeSubscriptions()
      }
    }
  }
  @objc private func refreshSelectedSubscription() {
    guard !saving, let selectedID else { return }
    SubscriptionRefreshMenu.present(store: store, groupID: selectedID, from: refreshSourceButton)
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
      (group.kind == .subscription ? "订阅分组，" : "自定义分组，") + group.name + "，"
        + String(group.members.count) + " 条线路")
    return stack
  }
  private func renderSidebar() {
    groups.apply(
      rows: displayedGroups.map {
        .init(
          id: $0.id, values: [$0.displayTitle])
      }, emptyMessage: "暂无分组", selectedID: selectedID)
    undo.isHidden = removed == nil
  }
  private func render() {
    renderSidebar()
    let group = selected
    deleteSourceButton.isHidden = group?.kind != .subscription
    deleteSourceButton.isEnabled = !saving && store.canDeleteSubscriptions
    refreshSourceButton.isHidden = group?.kind != .subscription
    refreshSourceButton.isEnabled = !saving && selectedID.map(store.canRefreshSubscription) == true
    refreshSourceButton.toolTip = selectedID.map(store.subscriptionStatus)
    if !saving, group?.kind == .subscription, let selectedID {
      pane.status.stringValue = store.subscriptionStatus(selectedID)
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
      ? "订阅自动维护 · 显示 \(visibleIDs.count) 条"
      : "已加入 \(group?.members.count ?? 0) 条 · 显示 \(visibleIDs.count) 条"
    members.apply(
      rows: visibleIDs.map { .init(id: $0, values: []) },
      emptyMessage: group == nil ? "新建一个分组，再选择线路" : "没有匹配的线路")
  }
  private func memberCell(_ id: String, column: Int) -> NSView? {
    guard let node = store.node(id) else { return nil }
    if column == 0 {
      if selected?.kind == .subscription {
        let icon = PaxIcon.view("checkmark", size: PaxTheme.Table.font)
        icon.setAccessibilityLabel("订阅成员")
        return icon
      }
      let check = NSButton(checkboxWithTitle: "", target: self, action: #selector(toggleCheck(_:)))
      check.identifier = .init(id)
      check.focusRingType = .none
      check.state =
        working.first(where: { $0.id == selectedID })?.members.contains(id) == true ? .on : .off
      check.setAccessibilityLabel("加入 " + node.name)
      return check
    }
    if column == 3 {
      let info = store.info(id)
      let stack = NSStackView()
      stack.spacing = PaxTheme.Spacing.xs
      if info.cfIP != nil {
        stack.addArrangedSubview(PaxFeatureTag("CF", tone: .blue))
      }
      if info.chain != nil { stack.addArrangedSubview(PaxFeatureTag("链式代理", tone: .purple)) }
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
  private func showNameEditor(groupID: String?) {
    guard !saving, new.window != nil else { return }
    namePopover?.performClose(nil)
    let current = groupID.flatMap { id in working.first { $0.id == id } }
    guard groupID == nil || current != nil else { return }
    let popover = NSPopover()
    let editor = RouteGroupNameController(mode: current.map { .rename($0.name) } ?? .create) {
      [weak self] value in
      guard let self else { return }
      let name = try RouteWorkbenchStore.validatedGroupName(
        value, existing: Set(self.working.filter { $0.id != groupID }.map(\.name)))
      if let groupID {
        guard let index = self.working.firstIndex(where: { $0.id == groupID }) else {
          throw RouteWorkbenchStore.EditError("分组已不存在")
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
    pane.status.stringValue = "正在保存分组…"
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
  private func move(_ id: String, to index: Int) {
    guard !saving else { return }
    guard let old = working.firstIndex(where: { $0.id == id }), working.indices.contains(index)
    else { return }
    let group = working.remove(at: old)
    working.insert(group, at: index)
    renderSidebar()
  }
  private func groupMenu(_ id: String) -> NSMenu {
    let menu = NSMenu()
    menu.autoenablesItems = false
    menu.delegate = destructiveTitles
    guard let index = working.firstIndex(where: { $0.id == id }) else {
      let remove = NSMenuItem(
        title: "删除订阅及全部线路…", action: #selector(deleteSubscription(_:)), keyEquivalent: "")
      remove.target = self
      remove.representedObject = id
      remove.image = PaxIcon.image("trash")
      remove.isEnabled = !saving && store.canDeleteSubscriptions
      destructiveTitles.mark(remove)
      menu.addItem(remove)
      let automatic = NSMenuItem(title: "订阅分组由来源自动维护", action: nil, keyEquivalent: "")
      automatic.isEnabled = false
      menu.addItem(automatic)
      return menu
    }
    for (title, command, symbol) in [
      ("编辑名称…", "rename", "pencil"), ("上移", "up", "arrow.up"), ("下移", "down", "arrow.down"),
      ("删除分组", "delete", "trash"),
    ] {
      let item = NSMenuItem(title: title, action: #selector(groupAction(_:)), keyEquivalent: "")
      item.target = self
      item.representedObject = [id, command]
      item.image = PaxIcon.image(symbol)
      item.preferVisibleImage()
      item.isEnabled =
        (command == "delete" || command == "rename")
        || (command == "up" ? index > 0 : index < working.count - 1)
      if command == "delete" { destructiveTitles.mark(item) }
      menu.addItem(item)
    }
    return menu
  }
  @objc private func deleteSelectedSubscription() {
    guard let id = selectedID else { return }
    let item = NSMenuItem()
    item.representedObject = id
    deleteSubscription(item)
  }
  @objc private func deleteSubscription(_ item: NSMenuItem) {
    guard !saving, store.canDeleteSubscriptions, let id = item.representedObject as? String,
      let group = displayedGroups.first(where: { $0.id == id && $0.kind == .subscription }),
      let window = view.window
    else { return }
    let memberCount = store.nodes.filter { "subscription:" + $0.sourceID == id }.count
    let alert = NSAlert()
    alert.messageText = "删除订阅“\(group.name)”及 \(memberCount) 条线路？"
    alert.informativeText =
      "将删除此订阅来源、所有所属线路（含隐藏线路和本地修改）、关联测量记录，并从自定义分组移除成员。其他线路和自定义分组保留。此操作立即生效，不等待保存分组；无法撤销。测量记录清理如失败，会单独提示。"
    alert.addButton(withTitle: "取消")
    alert.addButton(withTitle: "删除订阅及线路")
    alert.beginSheetModal(for: window) { [weak self] response in
      Task { @MainActor [weak self] in
        guard let self, response == .alertSecondButtonReturn, !self.saving else { return }
        self.saving = true
        self.new.isEnabled = false
        self.pane.status.stringValue = "正在删除订阅…"
        self.pane.status.isHidden = false
        do {
          let committedRevision = try await self.store.deleteSubscriptionGroup(
            id, expectedRevision: self.expectedRevision)
          let existing = Set(self.store.nodes.map(\.id))
          for index in self.working.indices {
            self.working[index].members.formIntersection(existing)
          }
          self.expectedRevision = committedRevision
          self.selectedID = self.working.first?.id ?? self.store.subscriptionGroups.first?.id
          self.saving = false
          self.new.isEnabled = true
          self.removed = nil
          self.pane.status.stringValue = "已删除订阅及所属线路"
          self.render()
        } catch {
          self.saving = false
          self.new.isEnabled = true
          self.showError(error)
        }
      }
    }
  }
  @objc private func groupAction(_ item: NSMenuItem) {
    guard !saving else { return }
    guard let args = item.representedObject as? [String], args.count == 2,
      let index = working.firstIndex(where: { $0.id == args[0] })
    else { return }
    if args[1] == "rename" {
      showNameEditor(groupID: args[0])
    } else if args[1] == "delete" {
      removed = (working[index], index)
      working.remove(at: index)
      if selectedID == args[0] {
        selectedID = working.first?.id ?? store.subscriptionGroups.first?.id
      }
      render()
    } else {
      move(args[0], to: index + (args[1] == "up" ? -1 : 1))
    }
  }
  @objc private func restoreGroup() {
    guard !saving else { return }
    guard let (group, index) = removed else { return }
    working.insert(group, at: min(index, working.count))
    selectedID = group.id
    removed = nil
    render()
  }
}
