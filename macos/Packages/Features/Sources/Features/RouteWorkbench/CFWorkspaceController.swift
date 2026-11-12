import AppKit
import Observation
import PaxCore
import PaxUI

/// Explicit discovery and displayed-IP tests; idle windows never poll or probe.
@MainActor final class CFWorkspaceController: RouteModalController {
  private let workbench: RouteWorkbenchStore
  private let draft: RouteWorkbenchStore.Draft
  private let store: CFWorkspaceStore
  private let apply: (RouteWorkbenchStore.Candidate) -> Void
  private let table = PaxCFRecordTable()
  private let progress = PaxLoadingStateView()
  private let summary = PaxRouteText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private let search = PaxRouteSearchField()
  private let testButton = PaxRouteButton("测试 IP", symbol: "waveform.path.ecg")
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
    let scan = button("扫描网段", #selector(scan))
    scanButton = scan
    scan.setAccessibilityIdentifier("cf-scan")
    scan.widthAnchor.constraint(equalToConstant: PaxTheme.Routes.cfScanWidth).isActive = true
    scan.toolTip = "在 CF 公布的网段中抽样发现 IP，再精测；计数表示本阶段候选 IP，不是网段数。"
    testButton.target = self
    testButton.action = #selector(showTests)
    testButton.showsDisclosure = true
    testButton.setAccessibilityIdentifier("cf-test-menu")
    let stop = button("停止", #selector(stopTask))
    stop.setAccessibilityIdentifier("cf-stop")
    stopButton = stop
    search.placeholderString = "搜索 IP 或网段"
    search.sendsSearchStringImmediately = true
    search.target = self
    search.action = #selector(searchChanged)
    search.widthAnchor.constraint(equalToConstant: PaxTheme.Layout.searchWidth).isActive = true
    let tools = NSStackView(views: [
      search, NSView(), scan, testButton, button("导出…", #selector(exportRecords)), stop,
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
    let more = button("再显示 50 条", #selector(showMore))
    moreButton = more
    summary.lineBreakMode = .byTruncatingTail
    summary.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    let paging = NSStackView(views: [summary, NSView(), more])
    paging.spacing = PaxTheme.Spacing.tight
    pane.add(paging)
    pane.add(
      PaxRouteText.label(
        "只测试本页已显示的 IP；单轮测速，每个 IP 最多 8 MiB／正文采样 6 秒；并发沿用下载设置，可随时停止。地区是 IP 数据库估计，不代表 CF 实际接入机房；扫描或测试后补全本页地区。使用只填草稿，保存才生效。",
        font: PaxTheme.Typography.caption(), secondary: true, wrapping: true))
    let use = button("验证并使用", #selector(useIP), primary: true)
    useButton = use
    pane.setActions([button("取消", #selector(close)), use])
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
      ? (workspace.total > 0 ? "扫描网段（\(workspace.completed)/\(workspace.total)）" : "扫描网段（准备中）")
      : "扫描网段"
    testButton.isEnabled = available && workbench.hasCFSingleTests && !store.visibleRows.isEmpty
    stopButton?.isHidden = !store.canStopTask
    stopButton?.isEnabled = store.canStopTask
    // Measurements belong to their row; only initial loading and actual errors
    // use a page notice. Scan progress remains inside its toolbar button.
    let message = !store.error.isEmpty ? store.error : store.loading ? "正在读取 IP 列表…" : ""
    progress.apply(
      !store.error.isEmpty ? .failure : store.loading ? .loading : .idle, message: message)
    summary.stringValue =
      "当前入口：\(draft.server) · 已显示 \(store.visibleRows.count)/\(store.displayCount) 个 IP"
    moreButton?.isHidden = !store.canShowMore
    moreButton?.isEnabled = available
  }
  private func renderTable() {
    table.setSort(column: store.sortColumn, ascending: store.sortAscending)
    table.apply(
      store.visibleRows.map {
        CFRecordPresentation.row(
          $0, currentNetwork: store.workspace.currentNetwork, selected: store.selected,
          pendingColumn: store.pendingColumn(for: $0))
      }, selected: store.selected, configured: draft.server, selectedIPs: store.selectedIPs,
      empty: store.emptyMessage,
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
      title: "本页已显示 · \(ips.count) 个 IP（不含隐藏项）", action: nil, keyEquivalent: "")
    scope.isEnabled = false
    menu.addItem(scope)
    menu.addItem(.separator())
    for (title, action, symbol) in [
      ("测试延迟", "check", "timer"), ("测试下载速度", "download", "arrow.down"),
      ("查询 IP 地区", "locate", "globe"),
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
          throw RouteWorkbenchStore.EditError(candidate.measurement.failure ?? "线路验证失败")
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
