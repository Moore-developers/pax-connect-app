import AppKit
import Observation
import PaxCore
import PaxUI

/// Explicit discovery and displayed-IP tests; idle windows never poll or probe.
@MainActor final class CFWorkspaceController: RouteModalController {
  override var localizedTitleKey: String? { "modal.cf.workspace" }
  override var localizedSubtitleKey: String? { "modal.cf.workspace_note" }

  private let workbench: RouteWorkbenchStore
  private let draft: RouteWorkbenchStore.Draft
  private let store: CFWorkspaceStore
  private let apply: (RouteWorkbenchStore.Candidate) -> Void
  private lazy var table = PaxCFRecordTable(
    columnTitles: (
      interfaceCopy.text("cf.workspace.column.ip"),
      interfaceCopy.text("cf.workspace.column.latency"),
      interfaceCopy.text("cf.workspace.column.download"),
      interfaceCopy.text("cf.workspace.column.location"),
      interfaceCopy.text("cf.workspace.column.actions")
    ),
    configuredTitle: interfaceCopy.text("cf.workspace.row.configured"),
    actionLabel: interfaceCopy.text("cf.workspace.row.actions"),
    actionTitles: [
      .latency: interfaceCopy.text("cf.workspace.row.latency"),
      .download: interfaceCopy.text("cf.workspace.row.download"),
      .both: interfaceCopy.text("cf.workspace.row.both"),
      .location: interfaceCopy.text("cf.workspace.row.location"),
    ])
  private let progress = PaxLoadingStateView()
  private let summary = PaxRouteText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private let search = PaxRouteSearchField()
  private let testButton = PaxRouteButton("", symbol: "waveform.path.ecg")
  private var moreButton: PaxRouteButton?
  private var useButton: PaxRouteButton?
  private var scanButton: PaxRouteButton?
  private var stopButton: PaxRouteButton?
  private var validating = false
  private var work: Task<Void, Never>?
  init(
    store: RouteWorkbenchStore, draft: RouteWorkbenchStore.Draft,
    apply: @escaping (RouteWorkbenchStore.Candidate) -> Void
  ) {
    workbench = store
    self.draft = draft
    self.apply = apply
    self.store = CFWorkspaceStore(
      query: .init(
        routeID: draft.chainContext.isEmpty ? draft.id : "", revision: draft.revision,
        document: draft.json), selected: draft.server, locationsEnabled: store.hasCFRegions,
      refresh: store.refresh,
      command: { try await store.cfRequest($0) },
      cancel: { try await store.cancelCFOperation($0) })
    super.init(
      title: "CF 优选列表", subtitle: "选择入口 IP，保留当前线路的 Host 与 SNI",
      size: PaxTheme.Routes.diagnosticWorkspace, symbol: "cloud")
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    super.loadView()
    let scan = button(self.interfaceCopy.text("action.cf.scan_ranges"), #selector(scan))
    scanButton = scan
    scan.setAccessibilityIdentifier("cf-scan")
    if interfaceCopy.context.language == .chinese {
      scan.widthAnchor.constraint(equalToConstant: PaxTheme.Routes.cfScanWidth).isActive = true
    }
    scan.toolTip = self.interfaceCopy.text("interface_detail.03e265fa31")
    testButton.target = self
    testButton.action = #selector(showTests)
    testButton.title = interfaceCopy.text("cf.workspace.test")
    testButton.showsDisclosure = true
    testButton.setAccessibilityIdentifier("cf-test-menu")
    let stop = button(self.interfaceCopy.text("action.common.stop"), #selector(stopTask))
    stop.setAccessibilityIdentifier("cf-stop")
    stopButton = stop
    search.placeholderString = self.interfaceCopy.text("interface_detail.dbbceb38fe")
    search.sendsSearchStringImmediately = true
    search.target = self
    search.action = #selector(searchChanged)
    search.widthAnchor.constraint(equalToConstant: PaxTheme.Layout.searchWidth).isActive = true
    let tools = NSStackView(views: [
      search, NSView(), scan, testButton,
      button(self.interfaceCopy.text("action.common.export"), #selector(exportRecords)), stop,
    ])
    tools.spacing = PaxTheme.Spacing.tight
    tools.setAccessibilityIdentifier("cf-toolbar")
    pane.add(tools)
    pane.add(progress)
    table.heightAnchor.constraint(greaterThanOrEqualToConstant: PaxTheme.Workbench.fullTable)
      .isActive = true
    table.onSelect = { [weak self] in self?.store.selected = $0 }
    table.onSelection = { [weak self] in self?.store.selectedIPs = $0 }
    table.onSort = { [weak self] column, ascending in
      self?.store.setSort(column: column, ascending: ascending)
    }
    table.onTest = { [weak self] ip, action in self?.testIP(ip, action: action) }
    pane.add(table)
    let more = button(self.interfaceCopy.text("action.common.show_fifty"), #selector(showMore))
    moreButton = more
    summary.lineBreakMode = .byTruncatingTail
    summary.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    let paging = NSStackView(views: [summary, NSView(), more])
    paging.spacing = PaxTheme.Spacing.tight
    pane.add(paging)
    pane.add(
      self.interfaceCopy.routeLabel(
        "action.cf.test_note",
        font: PaxTheme.Typography.caption(), secondary: true, wrapping: true))
    let use = button(
      self.interfaceCopy.text("action.cf.verify_use"), #selector(useIP), primary: true)
    useButton = use
    pane.setActions([button(self.interfaceCopy.text("common.cancel"), #selector(close)), use])
    observe()
    observeTable()
    let cfStore = store
    workbench.refresh?.bind(.cfTask) { [weak cfStore] in cfStore?.releaseTick() }
    work = Task { await store.open() }
  }
  override func viewWillAppear() {
    super.viewWillAppear()
    workbench.refresh?.pageDidAppear(.cfTask)
  }
  private func observe() {
    withObservationTracking {
      render()
    } onChange: { [weak self] in
      Task { @MainActor in self?.observe() }
    }
  }
  private func observeTable() {
    withObservationTracking {
      renderTable()
    } onChange: { [weak self] in
      Task { @MainActor in self?.observeTable() }
    }
  }
  private func render() {
    let workspace = store.workspace
    let running = store.taskRunning
    let available = !store.busy && !validating && !running && !store.loading
    let selected = store.visibleRows.first { $0.ip == store.selected }
    useButton?.isEnabled =
      available && store.selectedIPs.count <= 1 && selected != nil
      && RouteWorkbenchStore.validIP(store.selected)
    scanButton?.isEnabled = available
    scanButton?.title =
      running && ["scan", "discovery", "precision"].contains(workspace.phase)
      ? (workspace.total > 0
        ? self.interfaceCopy.text(
          "interface_detail.6fda08f4da",
          [
            "arg0": String(describing: workspace.completed),
            "arg1": String(describing: workspace.total),
          ]) : self.interfaceCopy.text("interface_detail.637ce1f0ae"))
      : self.interfaceCopy.text("action.cf.scan_ranges")
    testButton.isEnabled = available && workbench.hasCFSingleTests && !store.visibleRows.isEmpty
    stopButton?.isHidden = !store.canStopTask
    stopButton?.isEnabled = store.canStopTask
    // Measurements belong to their row; only initial loading and actual errors
    // use a page notice. Scan progress remains inside its toolbar button.
    let failure = store.failure?.message(context: interfaceCopy.context)
    let message =
      failure ?? (store.loading ? interfaceCopy.text("interface_detail.e3cbebf9cc") : "")
    progress.apply(
      failure != nil ? .failure : store.loading ? .loading : .idle, message: message)
    summary.stringValue =
      self.interfaceCopy.text(
        "interface_detail.730b657f0a",
        [
          "arg0": String(describing: draft.server),
          "arg1": String(describing: store.visibleRows.count),
          "arg2": String(describing: store.displayCount),
        ])
    moreButton?.isHidden = !store.canShowMore
    moreButton?.isEnabled = available
  }
  private func renderTable() {
    table.setSort(column: store.sortColumn, ascending: store.sortAscending)
    table.apply(
      store.visibleRows.map {
        CFRecordPresentation.row(
          $0, currentNetwork: store.workspace.currentNetwork, selected: store.selected,
          pendingColumn: store.pendingColumn(for: $0), context: self.interfaceCopy.context)
      }, selected: store.selected, configured: draft.server, selectedIPs: store.selectedIPs,
      empty: store.emptyMessage(context: interfaceCopy.context),
      actionsEnabled: !store.busy && !store.taskRunning && !validating && workbench.hasCFSingleTests
    )
  }
  @objc private func searchChanged() { store.search = search.stringValue }
  @objc private func showMore() { store.showMore() }
  @objc private func scan() { work = Task { await store.run("scan") } }
  @objc private func stopTask() { Task { await store.stop() } }
  /// Freeze the actual displayed IDs when the menu is opened; hidden pages never join.
  func testMenu() -> NSMenu {
    let menu = NSMenu()
    menu.autoenablesItems = false
    let ips = store.displayedIPs
    let scope = NSMenuItem(
      title: self.interfaceCopy.text(
        "interface_detail.99bf78d41d", ["arg0": String(describing: ips.count)]), action: nil,
      keyEquivalent: "")
    scope.isEnabled = false
    menu.addItem(scope)
    menu.addItem(.separator())
    for (title, action, symbol) in [
      (self.interfaceCopy.text("action.routes.test_latency"), "check", "timer"),
      (self.interfaceCopy.text("action.routes.test_download"), "download", "arrow.down"),
      (self.interfaceCopy.text("action.routes.lookup_exit"), "locate", "globe"),
    ] {
      let item = NSMenuItem(title: title, action: #selector(testScope(_:)), keyEquivalent: "")
      item.target = self
      item.representedObject = CFTestScope(action: action, ips: ips)
      item.isEnabled =
        !ips.isEmpty && !store.busy && !store.taskRunning && !validating
        && (action != "locate" || workbench.hasCFRegions)
        && (action != "download" || ips.count <= 20 || workbench.hasCFDisplayedBatch)
      item.image = PaxRouteIcon.image(symbol)
      item.preferVisibleImage()
      menu.addItem(item)
    }
    return menu
  }
  @objc private func showTests() {
    testMenu().popUp(positioning: nil, at: NSPoint(x: 0, y: testButton.bounds.maxY), in: testButton)
  }
  @objc private func testScope(_ item: NSMenuItem) {
    guard let scope = item.representedObject as? CFTestScope else { return }
    work = Task { await store.run(scope.action, ips: scope.ips) }
  }
  private func testIP(_ ip: String, action: PaxCFRecordTable.Action) {
    guard !store.busy, !store.taskRunning, !validating, workbench.hasCFSingleTests,
      action != .location || workbench.hasCFRegions
    else { return }
    let command =
      action == .latency
      ? "latency_one"
      : action == .download ? "download_one" : action == .location ? "locate" : "measure_one"
    work = Task { await store.run(command, ips: [ip]) }
  }
  @objc private func useIP() {
    guard !validating else { return }
    validating = true
    render()
    renderTable()
    let ip = store.selected
    work = Task { [weak self] in
      guard let self else { return }
      defer {
        validating = false
        render()
        renderTable()
      }
      do {
        let candidate = try await workbench.validateCF(draft, ip: ip)
        guard !Task.isCancelled else { return }
        await store.refresh()
        guard !Task.isCancelled else { return }
        guard candidate.measurement.failure == nil else {
          throw RouteWorkbenchStore.EditError(
            candidate.measurement.failure ?? self.interfaceCopy.text("interface_detail.3c0f3a6f7e"))
        }
        apply(candidate)
        close()
      } catch { if !Task.isCancelled { showError(error) } }
    }
  }
  @objc private func exportRecords() {
    CFRecordPresentation.export(store, in: view.window) { [weak self] in self?.showError($0) }
  }
  override func close() {
    workbench.refresh?.pageDidDisappear(.cfTask)
    work?.cancel()
    work = nil
    store.dismiss()
    super.close()
  }
  override func viewDidDisappear() {
    super.viewDidDisappear()
    workbench.refresh?.pageDidDisappear(.cfTask)
    work?.cancel()
    store.dismiss()
  }
}

private struct CFTestScope {
  let action: String
  let ips: [String]
}
