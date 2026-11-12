import AppKit
import PaxUI

@MainActor
class RoutingModalController: WorkbenchModalController {
  override func button(_ title: String, _ action: Selector, primary: Bool = false) -> PaxRouteButton
  {
    let value = super.button(title, action, primary: primary)
    if primary { value.keyEquivalent = "\r" }
    return value
  }
  override func showError(_ error: Error) {
    super.showError(error)
    pane.status.stringValue = (error as? RoutingWorkbenchStore.EditError)?.message ?? "操作失败，请检查输入。"
  }
  func picker(_ title: String, items: [String], selected: Int, action: Selector) -> PaxRoutePicker {
    let input = PaxRoutePicker()
    input.addItems(withTitles: items)
    if items.indices.contains(selected) { input.selectItem(at: selected) }
    input.target = self
    input.action = action
    input.setAccessibilityLabel(title)
    pane.add(PaxText.label(title, font: PaxTheme.Typography.caption(), secondary: true))
    pane.add(input)
    return input
  }
}

@MainActor
final class RoutingPackageEditor: RoutingModalController {
  private let store: RoutingWorkbenchStore
  private var draft: RoutingWorkbenchStore.PackageDraft
  private var name: NSTextField?
  private var destination: NSPopUpButton?
  private var kind: NSPopUpButton?
  private var source: NSTextField?
  init(store: RoutingWorkbenchStore, id: String? = nil) {
    self.store = store
    draft = id.flatMap(store.packageDraft) ?? store.newPackageDraft()
    super.init(title: id == nil ? "添加包" : "编辑规则包", symbol: "folder")
  }
  override func loadView() {
    super.loadView()
    render()
  }
  private func capture() {
    if let name { draft.name = name.stringValue }
    if let destination,
      store.destinationOptions(current: draft.destination).indices.contains(
        destination.indexOfSelectedItem)
    {
      draft.destination =
        store.destinationOptions(current: draft.destination)[destination.indexOfSelectedItem]
    }
    if let source { draft.source = source.stringValue }
  }
  private func render() {
    pane.clear()
    if draft.id == nil {
      kind = picker(
        "规则由谁维护", items: ["自己维护", "从链接订阅"], selected: draft.kind == .local ? 0 : 1,
        action: #selector(kindChanged))
    }
    let (nameView, input) = field("名称", value: draft.name, identifier: "routing-package-name")
    name = input
    pane.add(nameView)
    if draft.kind == .subscription {
      let (sourceView, input) = field(
        "兼容规则集 HTTPS 地址", value: draft.source, identifier: "routing-source")
      source = input
      pane.add(sourceView)
    } else {
      source = nil
    }
    destination = picker(
      "包内目标统一走",
      items: store.destinationOptions(current: draft.destination).map(store.destinationName),
      selected: store.destinationOptions(current: draft.destination).firstIndex(
        of: draft.destination) ?? 0,
      action: #selector(destinationChanged))
    pane.setActions([button("取消", #selector(close)), button("保存", #selector(save), primary: true)])
  }
  @objc private func kindChanged() {
    capture()
    draft.kind = kind?.indexOfSelectedItem == 1 ? .subscription : .local
    render()
  }
  @objc private func destinationChanged() { capture() }
  @objc private func save() {
    capture()
    let intent = RoutingWorkbenchStore.EditIntent.package(draft)
    Task { @MainActor [weak self] in
      guard let self else { return }
      do {
        try await store.saveLibrary(intent)
        close()
      } catch { showError(error) }
    }
  }
}

@MainActor
final class RoutingRuleEditor: RoutingModalController, NSTextFieldDelegate {
  private let store: RoutingWorkbenchStore
  private var draft: RoutingWorkbenchStore.RuleDraft
  private var target: NSTextField?
  private var method: NSPopUpButton?
  private var application: PaxDataTable?
  private let objectIcons: WorkbenchObjectIcons
  private var scope: NSPopUpButton?
  private var importedMethod: NSPopUpButton?
  private var destination: RoutingDestinationPicker?
  private var requiresRoute = false
  private let kind = PaxTabs(["应用", "域名", "IP"])
  private var kindIndex = 1
  private var appIDs: [String] = []
  private let appSearch = PaxRouteSearchField()
  private let previewLabel = PaxText.label("", secondary: true, wrapping: true)
  private var checks: [NSButton] = []
  private let importedNote = PaxText.label(
    "域名规则对其他应用也生效；不会自动添加未来域名。", secondary: true, wrapping: true)
  init(
    store: RoutingWorkbenchStore, packageID: String, ruleID: String? = nil,
    objectIcons: WorkbenchObjectIcons = WorkbenchObjectIcons()
  ) {
    self.objectIcons = objectIcons
    self.store = store
    if store.routeSelectionStore == nil && store.telemetry == nil {
      store.routeSelectionStore = RouteWorkbenchStore(session: WorkbenchDemoStore())
    }
    draft =
      ruleID.flatMap { store.ruleDraft(packageID: packageID, ruleID: $0) }
      ?? store.newRuleDraft(packageID: packageID)
    requiresRoute = ruleID == nil && store.package(packageID)?.kind == .priority
    kindIndex = draft.method == .application ? 0 : [.ip, .cidr].contains(draft.method) ? 2 : 1
    super.init(title: ruleID == nil ? "添加规则" : "编辑规则", symbol: "arrow.triangle.branch")
  }
  override func loadView() {
    super.loadView()
    kind.onSelect = { [weak self] index in self?.changeKind(index) }
    appSearch.placeholderString = "搜索应用"
    appSearch.target = self
    appSearch.action = #selector(filterApps)
    appSearch.sendsSearchStringImmediately = true
    appSearch.setAccessibilityLabel("搜索应用")
    render()
  }
  private func capture() {
    if let target { draft.target = target.stringValue }
    requiresRoute = destination != nil && destination?.selection == nil
    if let selection = destination?.selection { draft.destination = selection }
    if let method, kindIndex != 0 {
      draft.method =
        kindIndex == 2
        ? (method.indexOfSelectedItem == 1 ? .cidr : .ip)
        : (method.indexOfSelectedItem == 1 ? .domainSuffix : .domain)
    }
    draft.selectedDomains = checks.filter { $0.state == .on }.map(\.title)
    if let importedMethod {
      draft.domainMethod = importedMethod.indexOfSelectedItem == 1 ? .domainSuffix : .domain
    }
  }
  private func changeKind(_ index: Int) {
    capture()
    kindIndex = index
    draft.target = ""
    draft.importDomains = false
    draft.method = index == 0 ? .application : index == 2 ? .ip : .domain
    render()
  }
  private func render() {
    pane.clear()
    target = nil
    method = nil
    application = nil
    importedMethod = nil
    checks = []
    kind.selectedIndex = kindIndex
    pane.add(kind)
    if kindIndex == 0 {
      draft.method = .application
      pane.add(appSearch)
      appIDs = matchingApplications()
      if draft.target.isEmpty { draft.target = appIDs.first ?? "" }
      let candidates = PaxDataTable(
        columns: [.init("选择应用", width: 0, sortable: false, contentLayout: .fill)],
        metrics: .init(
          rowHeight: PaxTheme.Table.memberRow, headerHeight: PaxTheme.Table.compactHeader),
        selection: .single)
      application = candidates
      candidates.setTableIdentifier("routing-application-candidates")
      candidates.cellProvider = { [weak self] id, _ in
        guard let self, let app = self.store.applications.first(where: { $0.id == id }) else {
          return nil
        }
        let text = PaxText.label(app.name, font: PaxTheme.Typography.caption())
        text.toolTip = app.name + "\n" + app.id
        return self.objectIcons.identityView(
          kind: .application, id: app.id, name: app.name, content: text,
          size: PaxTheme.Logs.directoryIconSize, gap: PaxTheme.Spacing.tight,
          tint: PaxTheme.Color.textSecondary)
      }
      candidates.onSelect = { [weak self] id in
        guard let self, let id, self.appIDs.contains(id), id != self.draft.target else { return }
        self.capture()
        self.draft.target = id
        self.draft.selectedDomains = []
        self.render()
      }
      candidates.apply(
        rows: appIDs.map { .init(id: $0, values: []) }, emptyMessage: "没有匹配的应用，请换一个关键词",
        selectedID: draft.target)
      pane.add(candidates)
      scope = picker(
        "匹配范围", items: ["整个应用", "选择已观测域名"], selected: draft.importDomains ? 1 : 0,
        action: #selector(scopeChanged))
      if draft.importDomains {
        let domains = store.observedDomains(draft.target)
        if domains.isEmpty { pane.add(PaxText.label("此应用尚无已观测域名。", secondary: true)) }
        for domain in domains {
          let check = NSButton(
            checkboxWithTitle: domain, target: self, action: #selector(valueChanged))
          check.state = draft.selectedDomains.contains(domain) ? .on : .off
          checks.append(check)
          pane.add(check)
        }
        pane.add(importedNote)
        importedMethod = picker(
          "域名匹配方式", items: ["精确域名", "域名及子域"],
          selected: draft.domainMethod == .domainSuffix ? 1 : 0, action: #selector(valueChanged))
      } else {
        pane.add(
          PaxText.label("包含已确认归属的辅助进程、新域名与直接 IP；未知进程不凭名称合并。", secondary: true, wrapping: true))
      }
    } else {
      let (box, field) = field(
        kindIndex == 2 ? "IP 或网段" : "域名", value: draft.target, identifier: "routing-rule-target")
      field.delegate = self
      target = field
      pane.add(box)
      method = picker(
        "匹配范围", items: kindIndex == 2 ? ["单 IP", "IP 网段"] : ["精确域名", "域名及子域"],
        selected: [.cidr, .domainSuffix].contains(draft.method) ? 1 : 0,
        action: #selector(valueChanged))
    }
    if store.package(draft.packageID)?.kind == .priority {
      let picker = RoutingDestinationPicker(
        browser: store.routeSelectionStore,
        current: draft.destination, requiresRoute: requiresRoute,
        onChange: { [weak self] in self?.valueChanged() },
        onResize: { [weak self] in self?.pane.invalidateContentSize() })
      destination = picker
      pane.add(PaxRouteSectionHeading("这条规则的去向", symbol: "arrow.triangle.branch"))
      pane.add(picker)
    } else {
      destination = nil
    }
    pane.add(previewLabel)
    let save = button("保存规则", #selector(save), primary: true)
    save.isEnabled =
      store.canEdit && (destination == nil || destination?.selection != nil)
      && (kindIndex != 0 || appIDs.contains(draft.target))
      && !(draft.importDomains && store.observedDomains(draft.target).isEmpty)
    pane.setActions([button("取消", #selector(close)), save])
    previewLabel.stringValue = store.draftSummary(draft)
    pane.invalidateContentSize()
  }
  @objc private func scopeChanged() {
    capture()
    draft.importDomains = scope?.indexOfSelectedItem == 1
    render()
  }
  private func matchingApplications() -> [String] {
    let query = appSearch.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
    return store.applications.filter {
      query.isEmpty || ($0.name + " " + $0.id).localizedCaseInsensitiveContains(query)
    }.map(\.id)
  }
  @objc private func filterApps() {
    capture()
    appIDs = matchingApplications()
    application?.apply(
      rows: appIDs.map { .init(id: $0, values: []) }, emptyMessage: "没有匹配的应用，请换一个关键词",
      selectedID: draft.target)
    previewLabel.stringValue =
      appIDs.contains(draft.target) ? store.draftSummary(draft) : "请选择搜索结果中的应用；原选择和草稿保留。"
    pane.actions.arrangedSubviews.compactMap { $0 as? NSButton }.last?.isEnabled =
      store.canEdit && appIDs.contains(draft.target)
      && (destination == nil || destination?.selection != nil)
  }
  @objc private func valueChanged() {
    capture()
    previewLabel.stringValue =
      destination != nil && destination?.selection == nil
      ? "请选择一条线路，再保存规则。" : store.draftSummary(draft)
    pane.actions.arrangedSubviews.compactMap { $0 as? NSButton }.last?.isEnabled =
      store.canEdit && (destination == nil || destination?.selection != nil)
      && (kindIndex != 0 || appIDs.contains(draft.target))
    pane.invalidateContentSize()
  }
  func controlTextDidChange(_ notification: Notification) { valueChanged() }
  @objc private func save() {
    guard destination == nil || destination?.selection != nil else {
      showError(RoutingWorkbenchStore.EditError("请选择一条线路。"))
      return
    }
    capture()
    let intent = RoutingWorkbenchStore.EditIntent.rule(draft)
    Task { @MainActor [weak self] in
      guard let self else { return }
      do {
        try await store.saveLibrary(intent)
        close()
      } catch { showError(error) }
    }
  }
}
