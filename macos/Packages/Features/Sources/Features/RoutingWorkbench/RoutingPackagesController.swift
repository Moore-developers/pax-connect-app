import AppKit
import Observation
import PaxCore
import PaxUI

@MainActor
final class RoutingPackagesController: NSViewController {
  let store: RoutingWorkbenchStore
  private let objectIcons: WorkbenchObjectIcons
  private let packages = RoutingPackagesController.packageTable(height: PaxTheme.Routing.packageRow)
  private let priority = RoutingPackagesController.packageTable(
    height: PaxTheme.Routing.fixedPackageRow)
  private let customSection = NSStackView(views: [
    PaxText.label("自定义包", font: PaxTheme.Typography.caption(), secondary: true)
  ])
  private let geo = RoutingPackagesController.packageTable(height: PaxTheme.Routing.fixedPackageRow)
  private let rules = RoutingPackagesController.ruleTable(priority: false)
  private let priorityRules = RoutingPackagesController.ruleTable(priority: true)
  private let ruleArea = NSView()
  private let ruleCount = PaxText.label("", font: PaxTheme.Typography.caption())
  private let browseGeo = PaxRouteButton("查看规则", symbol: "list.bullet")
  private let checkUpdate = PaxRouteButton("检查更新")
  private var builtinHeight: NSLayoutConstraint?
  private var packagesHeight: NSLayoutConstraint?
  private var rulesHeight: NSLayoutConstraint?
  private let heading = PaxText.label(
    "", font: PaxTheme.Typography.label(size: PaxTheme.Routes.failureFont, weight: .medium))
  private let subtitle = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private let enabled = NSButton(checkboxWithTitle: "已启用", target: nil, action: nil)
  private let destination = PaxRoutePicker(style: .toolbar)
  private let search = PaxRouteSearchField()
  private let add = PaxRouteButton("添加规则", symbol: "plus", style: .primary)
  private let more = PaxRouteButton("", symbol: "ellipsis", style: .quiet)
  private let status = PaxText.label(
    "", font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  private let routeRow = NSStackView()
  private let detail = NSStackView()
  let updateArea = NSStackView()
  var showsUpdates = false
  private var updateTask: Task<Void, Never>?
  init(store: RoutingWorkbenchStore, objectIcons: WorkbenchObjectIcons = WorkbenchObjectIcons()) {
    self.objectIcons = objectIcons
    self.store = store
    super.init(nibName: nil, bundle: nil)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  private static func packageTable(height: CGFloat) -> PaxDataTable {
    PaxDataTable(
      columns: [
        .init(
          "", width: 0, inset: PaxTheme.Routing.sidebarInset, sortable: false, contentLayout: .fill)
      ],
      metrics: .init(
        rowHeight: height, headerHeight: 0, rounded: false, rowSeparators: false,
        selectionInset: 0), selection: .browse)
  }
  private static func ruleTable(priority: Bool) -> PaxDataTable {
    let titles = priority ? ["匹配目标", "匹配范围", "去向", ""] : ["匹配目标", "匹配范围", ""]
    let shares = priority ? PaxTheme.Routing.priorityColumns : PaxTheme.Routing.ruleColumns
    return PaxDataTable(
      columns: [
        .init(
          "序号", width: PaxTheme.Routing.ruleNumberWidth, growth: 0,
          alignment: .center, inset: PaxTheme.Routing.ruleInset, sortable: false)
      ]
        + titles.enumerated().map { index, title in
          .init(
            title, width: 0, growth: shares[index],
            inset: PaxTheme.Routing.ruleInset, sortable: false)
        },
      metrics: .init(
        rowHeight: PaxTheme.Table.memberRow, headerHeight: PaxTheme.Routing.ruleHeader,
        rounded: false), selection: .single)
  }
  override func loadView() {
    let card = PaxCard(insets: NSEdgeInsets())
    card.clipsContent = true
    card.setAccessibilityIdentifier("routing-rule-library")
    let outer = NSScrollView()
    outer.drawsBackground = false
    outer.hasVerticalScroller = true
    outer.autohidesScrollers = true
    let document = WorkbenchDocument()
    document.translatesAutoresizingMaskIntoConstraints = false
    outer.documentView = document
    document.widthAnchor.constraint(equalTo: outer.contentView.widthAnchor).isActive = true
    view = outer
    card.translatesAutoresizingMaskIntoConstraints = false
    document.addSubview(card)
    let compactHeight = card.heightAnchor.constraint(equalToConstant: 0)
    compactHeight.priority = .init(1)
    compactHeight.isActive = true
    NSLayoutConstraint.activate([
      card.leadingAnchor.constraint(equalTo: document.leadingAnchor),
      card.trailingAnchor.constraint(equalTo: document.trailingAnchor),
      card.topAnchor.constraint(equalTo: document.topAnchor),
      card.bottomAnchor.constraint(equalTo: document.bottomAnchor),
    ])
    let library = NSView()
    card.contentView = library
    library.bottomAnchor.constraint(equalTo: card.bottomAnchor).isActive = true
    let new = PaxRouteButton("添加包", target: self, action: #selector(newPackage))
    new.setAccessibilityIdentifier("routing-add-package")
    let leftHeader = NSStackView(views: [PaxText.label("规则包"), NSView(), new])
    leftHeader.spacing = PaxTheme.Spacing.tight
    leftHeader.edgeInsets = NSEdgeInsets(
      top: 0, left: PaxTheme.Routing.sidebarInset, bottom: 0,
      right: PaxTheme.Routing.sidebarInset)
    let section = NSStackView(views: [
      PaxText.label("内置包", font: PaxTheme.Typography.caption(), secondary: true)
    ])
    section.edgeInsets = NSEdgeInsets(
      top: 0, left: PaxTheme.Routing.sidebarInset, bottom: 0,
      right: PaxTheme.Routing.sidebarInset)
    let separator = ruleDivider()
    separator.heightAnchor.constraint(equalToConstant: PaxTheme.Layout.cardBorder).isActive = true
    customSection.edgeInsets = section.edgeInsets
    let left = NSStackView(views: [
      leftHeader, priority, customSection, packages, separator, section, geo,
    ])
    let sidebarHeight = card.heightAnchor.constraint(
      greaterThanOrEqualTo: left.heightAnchor, constant: PaxTheme.Routing.routeInset * 2)
    sidebarHeight.priority = .required
    left.orientation = .vertical
    left.alignment = .leading
    left.spacing = PaxTheme.Spacing.tight
    left.setCustomSpacing(PaxTheme.Spacing.regular, after: priority)
    left.setCustomSpacing(PaxTheme.Spacing.xs, after: section)
    for item in left.arrangedSubviews {
      item.widthAnchor.constraint(equalTo: left.widthAnchor).isActive = true
    }
    for (table, identifier, height) in [
      (priority, "routing-priority-package", PaxTheme.Routing.fixedPackageRow),
      (packages, "routing-packages", CGFloat(0)),
      (geo, "routing-geo-package", PaxTheme.Routing.fixedPackageRow),
    ] {
      table.setTableIdentifier(identifier)
      table.cellProvider = { [weak self] id, _ in self?.packageCell(id) }
      table.rowAppearance = { [weak self] id in
        self?.store.selectedPackageID == id ? .current : .normal
      }
      table.onSelect = { [weak self] id in
        if let id { self?.store.perform(.selectPackage(id)) }
      }
      let constraint = table.heightAnchor.constraint(equalToConstant: height)
      constraint.isActive = true
      if table === packages { packagesHeight = constraint }
      if table === geo { builtinHeight = constraint }
    }
    let leftScroll = NSScrollView()
    leftScroll.drawsBackground = false
    leftScroll.hasVerticalScroller = true
    leftScroll.autohidesScrollers = true
    let leftDocument = WorkbenchDocument()
    leftScroll.documentView = leftDocument
    leftDocument.translatesAutoresizingMaskIntoConstraints = false
    leftDocument.addSubview(left)
    left.translatesAutoresizingMaskIntoConstraints = false
    NSLayoutConstraint.activate([
      leftDocument.widthAnchor.constraint(equalTo: leftScroll.contentView.widthAnchor),
      left.leadingAnchor.constraint(
        equalTo: leftDocument.leadingAnchor),
      left.trailingAnchor.constraint(
        equalTo: leftDocument.trailingAnchor),
      left.topAnchor.constraint(
        equalTo: leftDocument.topAnchor,
        constant: PaxTheme.Routing.routeInset),
      left.bottomAnchor.constraint(
        equalTo: leftDocument.bottomAnchor,
        constant: -PaxTheme.Routing.routeInset),
    ])
    let identity = NSStackView(views: [heading, subtitle])
    identity.orientation = .vertical
    identity.alignment = .leading
    identity.spacing = PaxTheme.Spacing.xs
    identity.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    let head = NSStackView(views: [identity, NSView(), enabled, more])
    head.spacing = PaxTheme.Spacing.tight
    head.heightAnchor.constraint(
      equalToConstant: heading.intrinsicContentSize.height + subtitle.intrinsicContentSize.height
        + PaxTheme.Spacing.xs
    ).isActive = true
    enabled.target = self
    enabled.action = #selector(togglePackage)
    more.target = self
    more.action = #selector(managePackage)
    more.imagePosition = .imageOnly
    more.toolTip = "管理规则包"
    more.setAccessibilityLabel("管理规则包")
    let routeDescription = NSStackView(views: [
      PaxText.label("包内目标统一走", font: PaxTheme.Typography.caption()),
      PaxText.label("匹配到本包的目标使用此去向", font: PaxTheme.Typography.time, secondary: true),
    ])
    routeDescription.orientation = .vertical
    routeDescription.alignment = .leading
    routeDescription.spacing = PaxTheme.Spacing.xs
    routeRow.setViews([routeDescription, NSView(), destination], in: .leading)
    routeRow.spacing = PaxTheme.Spacing.tight
    destination.target = self
    destination.action = #selector(destinationChanged)
    destination.setAccessibilityLabel("包内目标统一去向")
    search.placeholderString = "搜索包内规则"
    search.sendsSearchStringImmediately = true
    search.target = self
    search.action = #selector(searchChanged)
    search.setAccessibilityIdentifier("routing-rule-search")
    search.heightAnchor.constraint(equalToConstant: PaxTheme.Routes.controlHeight).isActive = true
    search.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    add.target = self
    add.action = #selector(addRule)
    add.setAccessibilityIdentifier("routing-add-rule")
    let routeSurface = WorkbenchSurface()
    routeSurface.surfaceColor = PaxTheme.Routes.canvas
    routeSurface.wantsLayer = true
    routeSurface.layer?.cornerRadius = PaxTheme.Routing.routeRadius
    routeSurface.layer?.masksToBounds = true
    routeSurface.setAccessibilityIdentifier("routing-package-destination")
    routeRow.translatesAutoresizingMaskIntoConstraints = false
    routeSurface.addSubview(routeRow)
    NSLayoutConstraint.activate([
      routeRow.leadingAnchor.constraint(
        equalTo: routeSurface.leadingAnchor,
        constant: PaxTheme.Routing.routeInset),
      routeRow.trailingAnchor.constraint(
        equalTo: routeSurface.trailingAnchor,
        constant: -PaxTheme.Routing.routeInset),
      routeRow.topAnchor.constraint(
        equalTo: routeSurface.topAnchor,
        constant: PaxTheme.Routing.routeInset),
      routeRow.bottomAnchor.constraint(
        equalTo: routeSurface.bottomAnchor,
        constant: -PaxTheme.Routing.routeInset),
    ])
    checkUpdate.target = self
    checkUpdate.action = #selector(update)
    browseGeo.target = self
    browseGeo.action = #selector(viewGeoRules)
    browseGeo.setAccessibilityIdentifier("routing-browse-geo")
    let tools = NSStackView(views: [ruleCount, NSView(), search, add, browseGeo, checkUpdate])
    tools.spacing = PaxTheme.Spacing.tight
    tools.heightAnchor.constraint(equalToConstant: PaxTheme.Routes.controlHeight).isActive = true
    let searchWidth = search.widthAnchor.constraint(equalToConstant: PaxTheme.Routing.ruleWidth)
    searchWidth.priority = .defaultHigh
    searchWidth.isActive = true
    search.isHidden = true
    for (table, identifier) in [
      (rules, "routing-rules"), (priorityRules, "routing-priority-rules"),
    ] {
      table.setTableIdentifier(identifier)
      table.cellProvider = { [weak self] id, column in self?.ruleCell(id, column: column) }
      table.onActivate = { [weak self] id in self?.editRule(id) }
      table.translatesAutoresizingMaskIntoConstraints = false
      ruleArea.addSubview(table)
      NSLayoutConstraint.activate([
        table.leadingAnchor.constraint(equalTo: ruleArea.leadingAnchor),
        table.trailingAnchor.constraint(equalTo: ruleArea.trailingAnchor),
        table.topAnchor.constraint(equalTo: ruleArea.topAnchor),
        table.bottomAnchor.constraint(equalTo: ruleArea.bottomAnchor),
      ])
    }
    detail.setViews([head, routeSurface, tools, ruleArea, updateArea, status], in: .leading)
    detail.orientation = .vertical
    detail.alignment = .leading
    detail.spacing = PaxTheme.Spacing.medium
    rulesHeight = ruleArea.heightAnchor.constraint(equalToConstant: PaxTheme.Routing.ruleHeader)
    rulesHeight?.priority = .defaultHigh
    rulesHeight?.isActive = true
    for item in detail.arrangedSubviews {
      item.widthAnchor.constraint(equalTo: detail.widthAnchor).isActive = true
    }
    updateArea.orientation = .vertical
    updateArea.alignment = .leading
    updateArea.spacing = PaxTheme.Spacing.tight
    let divider = ruleDivider()
    for item in [leftScroll, divider, detail] {
      item.translatesAutoresizingMaskIntoConstraints = false
      library.addSubview(item)
    }
    NSLayoutConstraint.activate([
      leftScroll.leadingAnchor.constraint(equalTo: library.leadingAnchor),
      leftScroll.topAnchor.constraint(equalTo: library.topAnchor),
      leftScroll.bottomAnchor.constraint(equalTo: library.bottomAnchor),
      leftScroll.widthAnchor.constraint(equalToConstant: PaxTheme.Routing.librarySidebar),
      divider.leadingAnchor.constraint(equalTo: leftScroll.trailingAnchor),
      divider.widthAnchor.constraint(equalToConstant: PaxTheme.Layout.cardBorder),
      divider.topAnchor.constraint(equalTo: library.topAnchor),
      divider.bottomAnchor.constraint(equalTo: library.bottomAnchor),
      detail.leadingAnchor.constraint(
        equalTo: divider.trailingAnchor,
        constant: PaxTheme.Routing.libraryInset),
      detail.trailingAnchor.constraint(
        equalTo: library.trailingAnchor,
        constant: -PaxTheme.Routing.libraryInset),
      detail.topAnchor.constraint(
        equalTo: library.topAnchor,
        constant: PaxTheme.Routing.libraryInset),
      detail.bottomAnchor.constraint(
        lessThanOrEqualTo: library.bottomAnchor,
        constant: -PaxTheme.Routing.libraryInset),
    ])
    routeSurface.identifier = .init("routing-route-surface")
    sidebarHeight.isActive = true
    observe()
  }
  private func ruleDivider() -> NSView {
    let divider = WorkbenchSurface()
    divider.surfaceColor = PaxTheme.Color.cardBorder
    return divider
  }
  private func observe() {
    withObservationTracking { [weak self] in
      self?.render()
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observe() }
    }
  }
  func render() {
    let custom = store.packages.filter { [.local, .subscription].contains($0.kind) }
    packages.apply(
      rows: custom.map { .init(id: $0.id, values: []) }, selectedID: store.selectedPackageID
    )
    packages.isHidden = custom.isEmpty
    customSection.isHidden = custom.isEmpty
    packagesHeight?.constant = CGFloat(custom.count) * PaxTheme.Routing.packageRow
    priority.apply(
      rows: store.packages.filter { $0.kind == .priority }.map { .init(id: $0.id, values: []) },
      selectedID: store.selectedPackageID)
    let builtins = store.packages.filter { [.adblock, .geo].contains($0.kind) }
    geo.apply(
      rows: builtins.map { .init(id: $0.id, values: []) }, selectedID: store.selectedPackageID)
    builtinHeight?.constant = CGFloat(builtins.count) * PaxTheme.Routing.fixedPackageRow
    status.stringValue = store.packageMessage
    status.isHidden = status.stringValue.isEmpty
    guard let pack = store.selectedPackage else { return }
    if search.currentEditor() == nil { search.stringValue = store.rulesSearch }
    heading.stringValue = pack.name
    subtitle.stringValue = RoutingGeoPresentation.subtitle(pack, live: store.telemetry != nil)
    enabled.isHidden = pack.kind == .priority
    enabled.state = pack.enabled ? .on : .off
    enabled.attributedTitle = NSAttributedString(
      string: pack.enabled ? "已启用" : "已停用",
      attributes: [.foregroundColor: pack.enabled ? PaxTheme.Color.text : PaxTheme.Color.error])
    enabled.isEnabled = store.canEdit && !pack.updating
    routeRow.superview?.isHidden = [.priority, .geo, .adblock].contains(pack.kind)
    destination.removeAllItems()
    destination.addItems(
      withTitles: store.destinationOptions(current: pack.destination).map(store.destinationName))
    if let index = store.destinationOptions(current: pack.destination).firstIndex(
      of: pack.destination)
    {
      destination.selectItem(at: index)
    }
    destination.isEnabled = store.canEdit && !pack.updating
    add.isHidden = ![.local, .priority].contains(pack.kind)
    add.isEnabled = store.canEdit
    more.isHidden = [.priority, .geo, .adblock].contains(pack.kind)
    more.isEnabled = store.canEdit && !pack.updating
    let editable = [.local, .priority].contains(pack.kind)
    checkUpdate.isHidden = editable
    checkUpdate.title =
      store.libraryBusy
      ? "更新中…" : pack.kind == .geo && store.telemetry != nil ? "更新 Geo 基础包" : "检查更新"
    checkUpdate.isEnabled = store.canEdit && !pack.updating
    browseGeo.isHidden = pack.kind != .geo || store.telemetry == nil
    browseGeo.isEnabled = store.telemetry?.routingLibraryAvailable == true
    ruleCount.stringValue =
      "包内规则 · \(pack.rules.count) 条"
      + ([.subscription, .adblock].contains(pack.kind) ? " · 只读" : "")
      + (store.rulesSearch.isEmpty ? "" : " · 匹配 \(store.visibleRules.count) 条")
    rules.isHidden = pack.kind == .priority
    priorityRules.isHidden = pack.kind != .priority
    rules.setColumnTitle(editable ? "" : "去向", at: 3)
    let table = pack.kind == .priority ? priorityRules : rules
    table.apply(
      rows: store.visibleRules.map { .init(id: $0.id, values: []) },
      emptyMessage: store.rulesSearch.isEmpty ? "包内暂无规则，添加第一条规则" : "没有匹配的规则",
      selectedID: store.selectedRuleID)
    rulesHeight?.constant =
      PaxTheme.Routing.ruleHeader
      + (store.visibleRules.isEmpty
        ? PaxTheme.Routing.emptyRulesHeight
        : CGFloat(store.visibleRules.count) * PaxTheme.Table.memberRow)
    status.stringValue = store.packageMessage
    status.isHidden = status.stringValue.isEmpty
    renderUpdates(pack)
  }
  @objc private func viewGeoRules() {
    guard let telemetry = store.telemetry else { return }
    presentWorkbenchModal(RoutingGeoRulesController(telemetry: telemetry))
  }
  @objc private func update() {
    guard updateTask == nil else { return }
    let id = store.selectedPackageID
    updateTask = Task { @MainActor [weak self] in
      guard let self else { return }
      await self.store.perform(.update(id, self.store.session.scenario != .failed))
      self.updateTask = nil
    }
  }

  func focusSearch() {
    search.isHidden = false
    view.window?.makeFirstResponder(search)
  }
  @objc private func searchChanged() { store.perform(.search(search.stringValue)) }
  @objc private func togglePackage() {
    store.perform(.enablePackage(store.selectedPackageID, enabled.state == .on))
  }
  @objc private func destinationChanged() {
    let values = store.destinationOptions(current: store.selectedPackage?.destination ?? .global)
    if values.indices.contains(destination.indexOfSelectedItem) {
      store.perform(
        .packageDestination(
          store.selectedPackageID, values[destination.indexOfSelectedItem]))
    }
  }
  @objc func movePackage(_ sender: NSButton) {
    if let id = sender.identifier?.rawValue { store.perform(.movePackage(id, sender.tag)) }
  }
  @objc private func newPackage() { presentWorkbenchModal(RoutingPackageEditor(store: store)) }
  @objc private func addRule() {
    presentWorkbenchModal(
      RoutingRuleEditor(store: store, packageID: store.selectedPackageID, objectIcons: objectIcons))
  }
  private func editRule(_ id: String) {
    guard store.canEdit, [.local, .priority].contains(store.selectedPackage?.kind) else { return }
    presentWorkbenchModal(
      RoutingRuleEditor(
        store: store, packageID: store.selectedPackageID, ruleID: id, objectIcons: objectIcons))
  }
  @objc func edit(_ sender: NSButton) {
    if let id = sender.identifier?.rawValue { editRule(id) }
  }
  @objc func remove(_ sender: NSButton) {
    guard let id = sender.identifier?.rawValue else { return }
    confirm("移除这条规则？", detail: "只影响新连接；当前连接和历史记录保留。") { [weak self] in
      guard let self else { return }
      self.store.perform(.removeRule(self.store.selectedPackageID, id))
    }
  }
  @objc func enableRule(_ sender: NSButton) {
    if let id = sender.identifier?.rawValue {
      store.perform(.enableRule(store.selectedPackageID, id, sender.state == .on))
    }
  }
  private func confirm(_ title: String, detail: String, apply: @escaping @MainActor () -> Void) {
    guard let window = view.window else { return }
    let alert = NSAlert()
    alert.messageText = title
    alert.informativeText = detail
    alert.addButton(withTitle: "取消")
    alert.addButton(withTitle: "确认")
    alert.beginSheetModal(for: window) { response in
      if response == .alertSecondButtonReturn { apply() }
    }
  }
  @objc private func managePackage(_ sender: NSButton) {
    let menu = NSMenu()
    menu.autoenablesItems = false
    let values = [("重命名…", #selector(rename)), ("删除规则包…", #selector(deletePackage))]
    for (title, action) in values {
      let item = NSMenuItem(title: title, action: action, keyEquivalent: "")
      item.target = self
      menu.addItem(item)
    }
    if store.selectedPackage?.kind == .subscription {
      menu.removeAllItems()
      let item = NSMenuItem(title: "维护来源…", action: #selector(rename), keyEquivalent: "")
      item.target = self
      menu.addItem(item)
      let remove = NSMenuItem(title: "删除规则包…", action: #selector(deletePackage), keyEquivalent: "")
      remove.target = self
      menu.addItem(remove)
    }
    menu.popUp(positioning: nil, at: NSPoint(x: 0, y: sender.bounds.height), in: sender)
  }
  @objc private func rename() {
    presentWorkbenchModal(RoutingPackageEditor(store: store, id: store.selectedPackageID))
  }
  @objc private func deletePackage() {
    guard let pack = store.selectedPackage else { return }
    confirm("删除「\(pack.name)」？", detail: "同时删除 \(pack.rules.count) 条包内规则；新连接继续匹配其他包和 Geo。") {
      [weak self] in self?.store.perform(.removePackage(pack.id))
    }
  }
}
