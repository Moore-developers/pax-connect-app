import AppKit
import PaxCore
import PaxUI

@MainActor
final class RoutingRuleEditor: RoutingModalController, NSTextFieldDelegate {
  private enum Kind: Int, CaseIterable {
    case application, process, domain, ip
    var title: String { ["应用", "进程", "域名", "IP"][rawValue] }

    init(_ method: RoutingWorkbenchStore.Method) {
      switch method {
      case .application: self = .application
      case .process: self = .process
      case .ip, .cidr: self = .ip
      default: self = .domain
      }
    }
  }
  private enum CandidateList {
    case idle, loading
    case ready(RoutingProcessCandidates)
    case failed
  }

  private let store: RoutingWorkbenchStore
  private var draft: RoutingWorkbenchStore.RuleDraft
  private let tabs = PaxTabs(Kind.allCases.map(\.title))
  private var kind: Kind
  private let candidates: RoutingRuleCandidateGrid
  private let appOptions = NSStackView()
  private let appSearch = PaxRouteSearchField()
  private let processSearch = PaxRouteSearchField()
  private let processNote = PaxText.label("", secondary: true, wrapping: true)
  private let selectionNote = PaxText.label("", secondary: true, wrapping: true)
  private lazy var processLimitRow = NSStackView(views: [processNote, NSView(), processMore])
  private let footerLine = NSBox()
  private lazy var processMore = PaxRouteButton(
    "", style: .quiet, target: self, action: #selector(toggleProcessLimit))
  private let previewLabel = PaxText.label("", secondary: true, wrapping: true)

  private var target: NSTextField?
  private var method: NSPopUpButton?
  private let scope = PaxTabs(["整个应用", "选择已观测域名"])
  private var importedMethod: NSPopUpButton?
  private var checks: [NSButton] = []
  private var destination: RoutingDestinationPicker?
  private var requiresRoute = false
  private var saveButton: NSButton?
  private var matchingAppIDs: Set<String> = []
  private var candidateList: CandidateList = .idle
  private var candidateTask: Task<Void, Never>?
  private let savedApplication: String?
  private var processSelection: String
  private var showsAllProcesses = false
  private let processPreviewCount = 30

  init(
    store: RoutingWorkbenchStore, packageID: String, ruleID: String? = nil,
    objectIcons: WorkbenchObjectIcons = WorkbenchObjectIcons()
  ) {
    self.store = store
    candidates = RoutingRuleCandidateGrid(icons: objectIcons)
    if store.routeSelectionStore == nil && store.telemetry == nil {
      store.routeSelectionStore = RouteWorkbenchStore(session: WorkbenchDemoStore())
    }
    let draft =
      ruleID.flatMap { store.ruleDraft(packageID: packageID, ruleID: $0) }
      ?? store.newRuleDraft(packageID: packageID)
    self.draft = draft
    kind = Kind(draft.method)
    savedApplication = draft.method == .application ? draft.target : nil
    processSelection = draft.method == .process ? draft.target : ""
    requiresRoute = ruleID == nil && store.package(packageID)?.kind == .priority
    super.init(title: ruleID == nil ? "添加规则" : "编辑规则", symbol: "arrow.triangle.branch")
  }
  override func loadView() {
    super.loadView()
    tabs.onSelect = { [weak self] in self?.changeKind($0) }
    candidates.onSelect = { [weak self] in self?.choose($0) }
    for (field, title) in [(appSearch, "搜索应用"), (processSearch, "搜索进程名称或路径")] {
      field.placeholderString = title
      field.target = self
      field.action = #selector(filterCandidates)
      field.sendsSearchStringImmediately = true
      field.setAccessibilityLabel(title)
    }
    appOptions.orientation = .vertical
    appOptions.alignment = .leading
    appOptions.spacing = PaxTheme.Spacing.tight
    scope.onSelect = { [weak self] index in self?.scopeChanged(index) }
    footerLine.boxType = .separator
    footerLine.translatesAutoresizingMaskIntoConstraints = false
    pane.addSubview(footerLine)
    NSLayoutConstraint.activate([
      footerLine.leadingAnchor.constraint(equalTo: pane.leadingAnchor),
      footerLine.trailingAnchor.constraint(equalTo: pane.trailingAnchor),
      footerLine.bottomAnchor.constraint(equalTo: pane.bodyScroll.bottomAnchor),
    ])
    selectionNote.lineBreakMode = .byCharWrapping
    selectionNote.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    render()
    loadCandidates()
  }
  override func viewWillDisappear() {
    super.viewWillDisappear()
    candidateTask?.cancel()
  }
  private func capture() {
    if let target { draft.target = target.stringValue }
    requiresRoute = destination != nil && destination?.selection == nil
    if let selection = destination?.selection { draft.destination = selection }
    if let method {
      draft.method =
        kind == .ip
        ? (method.indexOfSelectedItem == 1 ? .cidr : .ip)
        : (method.indexOfSelectedItem == 1 ? .domainSuffix : .domain)
    }
    draft.selectedDomains = checks.filter { $0.state == .on }.map(\.title)
    if let importedMethod {
      draft.domainMethod = importedMethod.indexOfSelectedItem == 1 ? .domainSuffix : .domain
    }
  }
  private func changeKind(_ index: Int) {
    guard let newKind = Kind(rawValue: index), newKind != kind else { return }
    capture()
    kind = newKind
    draft.target = ""
    draft.importDomains = false
    draft.selectedDomains = []
    switch newKind {
    case .application:
      draft.method = .application
      draft.target = savedApplication ?? ""
    case .process:
      draft.method = .process
      draft.target = processSelection
    case .domain: draft.method = .domain
    case .ip: draft.method = .ip
    }
    render()
    loadCandidates()
  }
  private func render() {
    pane.performContentUpdates {
      pane.clear()
      target = nil
      method = nil
      destination = nil
      importedMethod = nil
      checks = []
      tabs.selectedIndex = kind.rawValue
      pane.add(tabs)
      let selectingCandidate = kind == .application || kind == .process
      footerLine.isHidden = !selectingCandidate
      pane.status.stringValue = ""
      pane.status.isHidden = true
      switch kind {
      case .application, .process:
        pane.add(PaxSectionHeader("近期有流量的" + kind.title, detail: "按流量从高到低"))
        pane.add(kind == .process ? processSearch : appSearch)
        pane.add(candidates)
        if kind == .process { pane.add(processLimitRow) }
        pane.add(selectionNote)
        renderCandidates()
        if kind == .application {
          pane.add(appOptions)
          renderApplicationOptions()
        }
      case .domain, .ip:
        let (box, field) = field(
          kind == .ip ? "IP 或网段" : "域名", value: draft.target, identifier: "routing-rule-target")
        field.delegate = self
        target = field
        pane.add(box)
        method = picker(
          "匹配范围", items: kind == .ip ? ["单 IP", "IP 网段"] : ["精确域名", "域名及子域"],
          selected: [.cidr, .domainSuffix].contains(draft.method) ? 1 : 0,
          action: #selector(valueChanged))
      }
      if store.package(draft.packageID)?.kind == .priority {
        let picker = RoutingDestinationPicker(
          browser: store.routeSelectionStore, current: draft.destination,
          requiresRoute: requiresRoute,
          onChange: { [weak self] in self?.valueChanged() },
          onResize: { [weak self] in self?.pane.invalidateContentSize() })
        destination = picker
        pane.add(PaxRouteSectionHeading("这条规则的去向", symbol: "arrow.triangle.branch"))
        pane.add(picker)
      }
      if !selectingCandidate { pane.add(previewLabel) }
      let save = button("保存规则", #selector(save), primary: true)
      saveButton = save
      pane.setActions([button("取消", #selector(close)), save])
      updatePreview()
    }
  }
  private func renderCandidates() {
    let isProcess = kind == .process
    let query = (isProcess ? processSearch : appSearch).stringValue.trimmingCharacters(
      in: .whitespacesAndNewlines)
    let values: [RoutingRuleCandidateGrid.Candidate]
    let status: String
    var limited = false
    switch candidateList {
    case .idle, .loading:
      values = []
      status = "正在读取有流量的" + kind.title + "…"
    case .failed:
      values = []
      status = "读取近期流量失败，切换标签可重试。"
    case .ready(let result):
      status = "近期没有记录到有流量的" + kind.title + "。"
      if isProcess {
        limited = result.limited
        values = result.processes.map {
          .init(id: $0.id, name: processName(for: $0.id), path: $0.id, bytes: $0.bytes)
        }
      } else {
        let names = Dictionary(uniqueKeysWithValues: store.applications.map { ($0.id, $0.name) })
        values = result.applications.compactMap { traffic in
          names[traffic.id].map { .init(id: traffic.id, name: $0, bytes: traffic.bytes) }
        }
      }
    }
    let filtered = values.filter {
      query.isEmpty || $0.searchText.localizedCaseInsensitiveContains(query)
    }
    if isProcess {
      processMore.isHidden = !query.isEmpty || values.count <= processPreviewCount
      processMore.title =
        showsAllProcesses
        ? "收起为前 \(processPreviewCount) 条" : "显示全部（\(values.count)）"
      processMore.setAccessibilityLabel(processMore.title)
      let count = showsAllProcesses ? values.count : min(values.count, processPreviewCount)
      processNote.stringValue =
        limited
        ? "仅列流量最高的 \(values.count) 项"
        : (showsAllProcesses ? "当前列表 · 全部 \(count) 项" : "当前列表 · 流量前 \(count) 项")
      processLimitRow.isHidden = processMore.isHidden && !limited
      selectionNote.font = .monospacedSystemFont(ofSize: PaxTheme.Routes.metaFont, weight: .regular)
      selectionNote.stringValue = draft.target.isEmpty ? "" : "精确匹配路径   " + draft.target
      selectionNote.toolTip = draft.target
    } else {
      selectionNote.font = PaxTheme.Typography.caption()
      matchingAppIDs = Set(filtered.map(\.id))
      if draft.target.isEmpty, query.isEmpty { draft.target = filtered.first?.id ?? "" }
      if let savedApplication, draft.target == savedApplication,
        !values.contains(where: { $0.id == savedApplication }),
        let name = store.applications.first(where: { $0.id == savedApplication })?.name
      {
        selectionNote.stringValue = "已选应用  " + name
        selectionNote.toolTip = name
      } else {
        selectionNote.stringValue = ""
      }
    }
    selectionNote.isHidden = selectionNote.stringValue.isEmpty
    let visible =
      isProcess && query.isEmpty && !showsAllProcesses
      ? Array(filtered.prefix(processPreviewCount)) : filtered
    candidates.apply(
      visible, selected: draft.target,
      emptyMessage: values.isEmpty ? status : "没有匹配的" + kind.title,
      isProcess: isProcess)
  }

  private func processName(for path: String) -> String {
    // Claude's native installer uses the version number as the executable filename.
    let claudeVersionPath =
      #"^/(Users/[^/]+|(?:private/)?var/root)/\.local/share/claude/versions/v?[0-9]+(\.[0-9]+)+([-+._][0-9A-Za-z.]+)?$"#
    if path.range(of: claudeVersionPath, options: .regularExpression) != nil { return "claude" }
    return (path as NSString).lastPathComponent
  }

  private func loadCandidates() {
    guard kind == .application || kind == .process else { return }
    switch candidateList {
    case .idle, .failed: candidateList = .loading
    case .loading, .ready: return
    }
    renderCandidates()
    candidateTask = Task { [weak self, store] in
      let list: CandidateList
      do {
        list = .ready(try await store.routingCandidates())
      } catch {
        list = .failed
      }
      guard let self, !Task.isCancelled else { return }
      candidateList = list
      if kind == .application || kind == .process {
        renderCandidates()
        updatePreview()
      }
    }
  }
  @objc private func toggleProcessLimit() {
    showsAllProcesses.toggle()
    renderCandidates()
    pane.invalidateContentSize()
  }
  private func choose(_ candidate: RoutingRuleCandidateGrid.Candidate) {
    guard candidate.id != draft.target else { return }
    capture()
    draft.target = candidate.id
    if kind == .application {
      draft.selectedDomains = []
      renderApplicationOptions()
    } else {
      processSelection = candidate.id
      renderCandidates()
    }
    updatePreview()
  }
  private func renderApplicationOptions() {
    for view in appOptions.arrangedSubviews {
      appOptions.removeArrangedSubview(view)
      view.removeFromSuperview()
    }
    importedMethod = nil
    checks = []
    scope.selectedIndex = draft.importDomains ? 1 : 0
    let title = PaxText.label("匹配范围", font: PaxTheme.Typography.label(weight: .medium))
    title.widthAnchor.constraint(equalToConstant: PaxTheme.Routes.formLabelWidth).isActive = true
    let row = NSStackView(views: [title, scope, NSView()])
    row.spacing = PaxTheme.Spacing.regular
    addAppOption(row)
    guard draft.importDomains else {
      let note = PaxText.label(
        "包含已确认归属的辅助进程、新域名与直接 IP。", font: PaxTheme.Typography.caption(), secondary: true,
        wrapping: true)
      let noteRow = NSStackView(views: [note])
      noteRow.edgeInsets.left = PaxTheme.Routes.formLabelWidth + row.spacing
      addAppOption(noteRow)
      return
    }
    let domains = store.observedDomains(draft.target)
    if domains.isEmpty { addAppOption(PaxText.label("此应用尚无已观测域名。", secondary: true)) }
    for domain in domains {
      let check = NSButton(checkboxWithTitle: domain, target: self, action: #selector(valueChanged))
      check.state = draft.selectedDomains.contains(domain) ? .on : .off
      checks.append(check)
      addAppOption(check)
    }
    addAppOption(
      PaxText.label(
        "域名规则对其他应用也生效；不会自动添加未来域名。", secondary: true, wrapping: true))
    importedMethod = picker(
      "域名匹配方式", items: ["精确域名", "域名及子域"],
      selected: draft.domainMethod == .domainSuffix ? 1 : 0, action: #selector(valueChanged),
      section: appOptions)
  }
  private func addAppOption(_ view: NSView) {
    appOptions.addArrangedSubview(view)
    view.widthAnchor.constraint(equalTo: appOptions.widthAnchor).isActive = true
  }
  private func scopeChanged(_ index: Int) {
    capture()
    draft.importDomains = index == 1
    renderApplicationOptions()
    updatePreview()
  }
  @objc private func filterCandidates() {
    renderCandidates()
    updatePreview()
  }
  @objc private func valueChanged() {
    capture()
    updatePreview()
  }
  private var canSave: Bool {
    guard store.canEdit, destination == nil || destination?.selection != nil else { return false }
    switch kind {
    case .application:
      return selectedApplicationAvailable
        && (!draft.importDomains || !draft.selectedDomains.isEmpty)
    case .process: return !draft.target.isEmpty
    case .domain, .ip: return true
    }
  }
  private var selectedApplicationAvailable: Bool {
    matchingAppIDs.contains(draft.target)
      || (draft.target == savedApplication && store.applications.contains { $0.id == draft.target })
  }
  private func updatePreview() {
    if kind == .application || kind == .process {
      updateCandidateFooter()
    } else {
      previewLabel.stringValue =
        destination != nil && destination?.selection == nil
        ? "请选择一条线路，再保存规则。" : store.draftSummary(draft)
    }
    saveButton?.isEnabled = canSave
    pane.invalidateContentSize()
  }
  private func updateCandidateFooter() {
    let footer = pane.status
    footer.isHidden = false
    footer.textColor = PaxTheme.Color.textSecondary
    guard let pack = store.package(draft.packageID) else {
      footer.stringValue = "规则包已不存在。"
      return
    }
    if destination != nil && destination?.selection == nil {
      footer.stringValue = "请选择一条线路，再保存规则。"
      return
    }
    if draft.target.isEmpty || (kind == .application && !selectedApplicationAvailable) {
      footer.stringValue = draft.target.isEmpty ? "请选择" + kind.title + "。" : "请选择搜索结果中的应用；原选择和草稿保留。"
      return
    }
    do {
      _ = try store.ruleProjection(draft, in: pack)
    } catch {
      footer.stringValue = (error as? RoutingWorkbenchStore.EditError)?.message ?? "请输入有效目标。"
      return
    }
    let target: String
    if kind == .process {
      target = processName(for: draft.target)
    } else if draft.importDomains {
      target =
        "\(draft.selectedDomains.count) 个"
        + (draft.domainMethod == .domainSuffix ? "域名及子域" : "精确域名")
    } else {
      target = store.applications.first { $0.id == draft.target }?.name ?? draft.target
    }
    let dest = pack.kind == .priority ? draft.destination : pack.destination
    let headline = target + " → " + store.destinationName(dest)
    let summary = NSMutableAttributedString(
      string: headline,
      attributes: [
        .font: PaxTheme.Typography.label(weight: .semibold), .foregroundColor: PaxTheme.Color.text,
      ])
    let note = pack.kind == .priority ? "优先规则 · 仅影响新连接" : "沿用「" + pack.name + "」规则包的去向"
    summary.append(
      NSAttributedString(
        string: "\n" + note,
        attributes: [
          .font: PaxTheme.Typography.caption(), .foregroundColor: PaxTheme.Color.textSecondary,
        ]))
    footer.attributedStringValue = summary
  }
  func controlTextDidChange(_ notification: Notification) { valueChanged() }
  @objc private func save() {
    capture()
    guard canSave else { return }
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
