import AppKit
import Observation
import PaxCore
import PaxUI

/// 固定工具栏与底栏；两张共享表格独立滚动。
@MainActor
final class LogsWorkbenchViewController: NSViewController, InterfaceLanguageConsumer {
  let interfaceCopy = InterfaceCopyBindings()
  let store: LogsWorkbenchStore
  private let inspectorHost = NSView()
  private let objectIcons: WorkbenchObjectIcons
  private let connection: ConnectionStore?
  private let session: WorkbenchDemoStore?
  let mode = PaxTabs(["代理", "直连"], style: .mode)
  lazy var toolsHost = LogsFilterBar(level: level, range: range)
  let level = PaxTabs(["全部级别", "错误", "警告", "信息"])
  private var renderedDimension: LogsWorkbenchStore.Dimension?

  private var inspector: LogEventDetailController?
  private var cardTrailing: NSLayoutConstraint?
  private var inspectorWidth: NSLayoutConstraint?
  private var detailInvalidation: UInt64 = 0
  private var revokedDirectoryOrigin: NSPoint?
  lazy var previousPage = PaxRouteButton(
    "上一页", style: .quiet, target: self, action: #selector(previousRecords))
  lazy var nextPage = PaxRouteButton(
    "下一页", style: .quiet, target: self, action: #selector(nextRecords))
  let pageLabel = PaxText.label("第1页", font: PaxTheme.Typography.caption(), secondary: true)
  @ObservationIgnored private var renderedObjects: [LogsWorkbenchStore.ObjectRow] = []
  @ObservationIgnored private var renderedEvents: [LogPreviewEvent] = []
  private var renderedRecords: [LogRecord] = []
  private var renderedEmptyMessage = ""
  private var renderedSelection = ""
  private var renderedPendingObject: String?
  var pageVisible = false
  let pageRefresh: PaxRefresh?
  var entryPending = false
  /// 第 1 页加载中到来的推送：加载完成后再拉一次最新。
  var latestPending = false
  var latestPendingGeneration: UInt64?
  let tabs = PaxTabs(["应用", "域名", "系统"])
  let search = PaxRouteSearchField()
  let range = PaxRoutePicker(style: .toolbar)
  var loadTask: Task<Void, Never>?
  var detailTask: Task<Void, Never>?
  var detailGeneration: UInt64 = 0
  var onRoute: ((String) -> Void)?
  var onTraffic: ((String) -> Void)?
  private var initialLogMessage: String {
    guard store.telemetry != nil, store.initialized, store.liveMessage.isEmpty,
      !store.privacyInvalid, store.selectedObject == "all", store.level == .all
    else { return store.emptyMessage(context: interfaceCopy.context) }
    return connection?.proxyEnabled == true
      ? "等待访问记录；正常上网后自动显示，Pax 操作也会记录在这里"
      : "当前未采集网络访问；开启代理后开始记录，已保存的访问和操作仍可查看"
  }
  private let sourceLabel = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private let countLabel = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  lazy var moreObjectsButton = PaxRouteButton(
    "更多", style: .quiet, target: self, action: #selector(loadMoreObjects))
  lazy var directoryFooter = NSStackView(views: [countLabel, NSView(), moreObjectsButton])
  let directoryScroll = NSScrollView()
  let directory: PaxDataTable
  let events: PaxDataTable
  let systemEvents: PaxDataTable
  private var activeTable: PaxDataTable { store.dimension == .system ? systemEvents : events }
  init(
    store: LogsWorkbenchStore = LogsWorkbenchStore(), connection: ConnectionStore? = nil,
    session: WorkbenchDemoStore? = nil, objectIcons: WorkbenchObjectIcons = WorkbenchObjectIcons(),
    refresh: PaxRefresh? = nil
  ) {
    self.store = store
    pageRefresh = refresh
    self.objectIcons = objectIcons
    self.connection = connection
    self.session = session
    directory = PaxDataTable(
      columns: [
        .init("对象", width: 0, growth: 1, sortable: false),
        .init(
          "事件", width: PaxTheme.Layout.timeColumn, numeric: true, growth: 0,
          inset: PaxTheme.Spacing.tight, sortable: false),
      ],
      metrics: .init(
        rowHeight: PaxTheme.Logs.directoryRow, headerHeight: 0, rounded: false,
        rowSeparators: false, selectionInset: PaxTheme.Table.selectionInset,
        background: PaxTheme.Logs.directoryFill),
      selection: store.telemetry == nil ? .single : .browse)
    events = PaxDataTable(
      columns: [
        .init("时间", width: PaxTheme.Logs.timeColumn, growth: 0, symbol: "clock", sortable: false),
        .init(
          "应用 → 目标", width: PaxTheme.Logs.applicationColumn, growth: 0.46, symbol: "arrow.up.right",
          inset: PaxTheme.Logs.cellInset, sortable: false),
        .init(
          "状态", width: PaxTheme.Logs.stateColumn, growth: 0.26, symbol: "checkmark.circle",
          inset: PaxTheme.Logs.cellInset,
          sortable: false),
        .init(
          "实际连接", width: PaxTheme.Logs.connectionColumn, growth: 0.22,
          symbol: "point.3.connected.trianglepath.dotted",
          inset: PaxTheme.Logs.cellInset, sortable: false),
        .init(
          "响应延迟", width: PaxTheme.Logs.durationColumn, growth: 0.06, symbol: "timer",
          inset: PaxTheme.Logs.cellInset,
          sortable: false),
        .init(
          "上传／下载", width: PaxTheme.Logs.transferColumn, growth: 0.12, symbol: "arrow.up.arrow.down",
          sortable: false, contentLayout: .fill),
      ],
      metrics: .init(
        rowHeight: PaxTheme.Logs.eventRow, headerHeight: PaxTheme.Logs.headerHeight, rounded: false,
        selectionRail: true, headerBackground: PaxTheme.Logs.directoryFill, headerTopBorder: true,
        alternateBackground: PaxTheme.Logs.alternateBackground), scrollMode: .fixedHeader)
    systemEvents = PaxDataTable(
      columns: [
        .init("时间", width: PaxTheme.Logs.timeColumn, growth: 0, symbol: "clock", sortable: false),
        .init(
          "操作／事件", width: PaxTheme.Logs.applicationColumn, growth: 0.3, symbol: "gearshape",
          sortable: false),
        .init(
          "状态", width: PaxTheme.Logs.stateColumn, growth: 0.4, symbol: "checkmark.circle",
          sortable: false),
        .init(
          "相关线路", width: PaxTheme.Logs.connectionColumn, growth: 0.3,
          symbol: "point.3.connected.trianglepath.dotted", sortable: false),
      ],
      metrics: .init(
        rowHeight: PaxTheme.Logs.eventRow, headerHeight: PaxTheme.Logs.headerHeight, rounded: false,
        selectionRail: true, headerBackground: PaxTheme.Logs.directoryFill, headerTopBorder: true,
        alternateBackground: PaxTheme.Logs.alternateBackground), scrollMode: .fixedHeader)
    super.init(nibName: nil, bundle: nil)
    pageRefresh?.bind(.logs) { [weak self] in self?.fetchLatest() }
  }

  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    view = WorkbenchSurface()
    view.setAccessibilityIdentifier("logs-page")
    let header = PaxPageHeader(
      title: self.interfaceCopy.text("navigation.logs"),
      accessory: ProxyStatusAccessory(mode: mode, connection: connection, presenter: self))
    configureLanguage(header: header)
    mode.setAccessibilityIdentifier("logs-power")
    mode.onSelect = { [weak self] index in
      guard let self else { return }
      self.mode.selectedIndex =
        self.connection?.proxyEnabled == true || self.session?.isRunning == true ? 0 : 1
      if let connection = self.connection {
        ProxyTransitionPresenter.request(from: self, connection: connection, enabled: index == 0)
      } else if let session = self.session, (index == 0) != session.isRunning {
        session.perform(.toggleProxy)
      }
    }
    let card = PaxCard(insets: NSEdgeInsets())
    card.clipsContent = true
    let body = NSView()
    card.contentView = body
    body.bottomAnchor.constraint(equalTo: card.bottomAnchor).isActive = true
    for item in [header, card, sourceLabel] {
      item.translatesAutoresizingMaskIntoConstraints = false
      view.addSubview(item)
    }
    NSLayoutConstraint.activate([
      header.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: PaxTheme.Spacing.page),
      header.trailingAnchor.constraint(
        equalTo: view.trailingAnchor, constant: -PaxTheme.Spacing.page),
      header.topAnchor.constraint(
        equalTo: view.safeAreaLayoutGuide.topAnchor, constant: PaxTheme.Spacing.pageTop),
      header.heightAnchor.constraint(equalToConstant: PaxTheme.Dashboard.pageHeader),
      card.leadingAnchor.constraint(equalTo: header.leadingAnchor),

      card.topAnchor.constraint(equalTo: header.bottomAnchor, constant: PaxTheme.Spacing.medium),
      card.bottomAnchor.constraint(
        equalTo: sourceLabel.topAnchor, constant: -PaxTheme.Spacing.medium),
      sourceLabel.leadingAnchor.constraint(equalTo: card.leadingAnchor),
      sourceLabel.trailingAnchor.constraint(equalTo: card.trailingAnchor),
      sourceLabel.bottomAnchor.constraint(
        equalTo: view.bottomAnchor, constant: -PaxTheme.Spacing.page),
    ])
    let trailing = card.trailingAnchor.constraint(equalTo: header.trailingAnchor)
    trailing.isActive = true
    cardTrailing = trailing
    inspectorHost.translatesAutoresizingMaskIntoConstraints = false
    view.addSubview(inspectorHost)
    let width = inspectorHost.widthAnchor.constraint(equalToConstant: 0)
    width.isActive = true
    inspectorWidth = width
    NSLayoutConstraint.activate([
      inspectorHost.trailingAnchor.constraint(equalTo: header.trailingAnchor),
      inspectorHost.topAnchor.constraint(equalTo: card.topAnchor),
      inspectorHost.bottomAnchor.constraint(equalTo: card.bottomAnchor),
    ])
    inspectorHost.isHidden = true
    buildColumns(in: body)
    wireActions()
    observe()
  }
  private func wireActions() {
    tabs.onSelect = { [weak self] index in
      let dimension: LogsWorkbenchStore.Dimension = [.application, .domain, .system][index]
      self?.closeInspector()
      self?.store.perform(.dimension(dimension))
    }
    search.target = self
    search.action = #selector(searchChanged)
    level.onSelect = { [weak self] index in
      self?.closeInspector()
      if let value = LogsWorkbenchStore.Level(rawValue: index) {
        self?.store.perform(.level(value))
      }
    }
    for table in [events, systemEvents] {
      table.menuForRow = { [weak self] id in
        guard let self else { return nil }
        let menu = NSMenu()
        let item = LogDetailMenuItem(
          title: self.interfaceCopy.text("interface_detail.a748cc074f"),
          action: #selector(self.openContextDetail(_:)), keyEquivalent: "")
        item.target = self
        item.recordID = id
        menu.addItem(item)
        return menu
      }
    }
    directory.setTableIdentifier("logs-objects")
    events.setTableIdentifier("logs-events")
    systemEvents.setTableIdentifier("logs-system-events")
    directory.rowContent = { [weak self] id in
      guard id == "group", let self else { return nil }
      let dimension = self.store.dimension
      let heading = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
      return self.interfaceCopy.bind(heading) { label, context in
        label.stringValue =
          dimension == .ip
          ? context.text("log_detail.target_ip") : dimension.title(context: context)
      }
    }
    directory.menuForRow = { [weak self] id in
      guard let self, self.store.dimension == .domain,
        let object = self.store.objects.first(where: { $0.id == id })
      else { return nil }
      return self.objectIcons.menu(host: object.title)
    }
    directory.canSelectRow = { $0 != "group" }
    directory.cellProvider = { [weak self] id, column in
      guard let self,
        let content = logObjectCell(
          id: id, column: column, store: self.store, icons: self.objectIcons,
          context: self.interfaceCopy.context)
      else { return nil }
      guard self.store.telemetry != nil, column == 0 else { return content }
      return PaxDataTable.reusableContent(
        content,
        reuseKey: logObjectCellKey(id: id, store: self.store, context: self.interfaceCopy.context))
    }
    directory.cellUpdater = { [weak self] existing, id, column in
      guard let self, self.store.telemetry != nil else { return false }
      if column == 1 {
        if id == self.store.requestedObject && id != self.store.selectedObject {
          return !(existing is NSTextField)
        }
        guard let label = existing as? NSTextField,
          let object = self.store.objects.first(where: { $0.id == id })
        else { return false }
        let value = String(object.count)
        if label.stringValue != value { label.stringValue = value }
        return true
      }
      return PaxDataTable.updateContent(
        existing,
        reusing: logObjectCellKey(id: id, store: self.store, context: self.interfaceCopy.context)
      ) {
        logObjectCell(
          id: id, column: column, store: self.store, icons: self.objectIcons,
          context: self.interfaceCopy.context)
          ?? NSView()
      }
    }
    let chooseObject: (String) -> Void = { [weak self] id in
      self?.store.perform(.object(id))
    }
    directory.onSelect = { id in if let id { chooseObject(id) } }
    if store.telemetry != nil { directory.onRowClick = chooseObject }
    for table in [events, systemEvents] {
      table.onScrollPosition = { [weak self] atTop in
        guard let self else { return }
        self.store.atTop = atTop
        self.store.reading = !atTop || self.store.selectedEvent != nil || self.store.pageIndex > 0
      }
      table.onSelect = { [weak self] id in
        if id != nil { self?.store.reading = true }
        self?.store.perform(.select(id))
        if self?.store.usesViews == true { self?.render() }
      }
      table.onActivate = { [weak self] id in self?.showDetail(id) }
      table.cellProvider = { [weak self] id, column in
        guard let self, let content = self.cell(id, column: column) else { return nil }
        guard self.store.telemetry != nil, !(content is NSTextField) else { return content }
        return PaxDataTable.reusableContent(content, reuseKey: self.transferCellKey(id))
      }
      table.cellUpdater = { [weak self] existing, id, column in
        guard let self, self.store.telemetry != nil, let content = self.cell(id, column: column)
        else { return false }
        if let label = existing as? NSTextField, let next = content as? NSTextField {
          updateLogTextCell(label, from: next)
          return true
        }
        return PaxDataTable.updateContent(existing, reusing: self.transferCellKey(id)) { content }
      }
    }
  }
  private func observe() {
    // 选中行只改变详情按钮，不重建表格，保留双击与滚动位置。
    withObservationTracking {
      syncRefresh()
      render()
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observe() }
    }
  }
  func render() {
    level.selectedIndex = store.level.rawValue
    range.selectItem(at: store.rangeIndex)
    if let telemetry = store.telemetry {
      if store.privacyInvalid && detailInvalidation != telemetry.invalidationGeneration {
        detailInvalidation = telemetry.invalidationGeneration
        closeInspector()
        if revokedDirectoryOrigin == nil {
          revokedDirectoryOrigin = directoryScroll.contentView.bounds.origin
        }
        store.revokeSnapshot()
        events.revokeRows(emptyMessage: self.interfaceCopy.text("interface_detail.850e6a9b37"))
        systemEvents.revokeRows(
          emptyMessage: self.interfaceCopy.text("interface_detail.850e6a9b37"))
        renderedRecords = []
      }
      if !telemetry.isShown(.logs, focus: pageRefresh == nil) { return }
    }
    mode.selectedIndex = connection?.proxyEnabled == true || session?.isRunning == true ? 0 : 1
    mode.isEnabled = connection?.canToggleProxy ?? (session != nil)
    directory.setColumnTitle(interfaceCopy.text("logs.records"), at: 1)
    let dimensionChanged = renderedDimension != store.dimension
    if dimensionChanged {
      events.setColumnTitle(
        interfaceCopy.text(store.dimension == .domain ? "logs.domain_target" : "logs.app_target"),
        at: 1, symbol: "arrow.up.right")
      renderedDimension = store.dimension
    }
    previousPage.isEnabled = store.canPreviousPage
    nextPage.isEnabled = store.canNextPage
    pageLabel.stringValue = store.pageTitle(context: interfaceCopy.context)

    tabs.selectedIndex = store.dimension == .application ? 0 : store.dimension == .domain ? 1 : 2
    systemEvents.isHidden = store.dimension != .system
    events.isHidden = store.dimension == .system
    search.placeholderString = interfaceCopy.text(
      store.dimension == .system ? "logs.search_system" : "logs.search")
    search.setAccessibilityLabel(search.placeholderString)
    if search.currentEditor() == nil { search.stringValue = store.query }
    moreObjectsButton.isHidden = store.telemetry == nil || store.objectCursor.isEmpty
    let objectChanged = renderedSelection != store.selectedObject
    let pendingObject = store.requestedObject == store.selectedObject ? nil : store.requestedObject
    if dimensionChanged || renderedObjects != store.objects || objectChanged
      || renderedPendingObject != pendingObject
    {
      let previous = Dictionary(uniqueKeysWithValues: renderedObjects.map { ($0.id, $0) })
      var changed = Set(
        store.objects.filter { dimensionChanged || previous[$0.id] != $0 }.map(\.id))
      if objectChanged {
        changed.formUnion([renderedSelection, store.selectedObject])
      }
      if renderedPendingObject != pendingObject {
        changed.formUnion([renderedPendingObject, pendingObject].compactMap { $0 })
      }
      directory.apply(
        rows: store.objects.enumerated().flatMap { index, object -> [PaxDataTable.Row] in
          let row = PaxDataTable.Row(
            id: object.id,
            values: [
              object.id == "all"
                ? store.allTitle(context: interfaceCopy.context)
                : object.id == "unknown"
                  ? store.dimension.unknown(context: interfaceCopy.context) : object.title,
              String(object.count),
            ])
          return index == 0 ? [row, .init(id: "group", values: ["", ""])] : [row]
        },
        selectedID: store.selectedObject, reloading: store.telemetry == nil ? nil : changed,
        reusingVisibleCells: true)
      renderedObjects = store.objects
      renderedSelection = store.selectedObject
      renderedPendingObject = pendingObject
      if !store.privacyInvalid, let origin = revokedDirectoryOrigin {
        directoryScroll.layoutSubtreeIfNeeded()
        directoryScroll.contentView.scroll(to: origin)
        directoryScroll.reflectScrolledClipView(directoryScroll.contentView)
        revokedDirectoryOrigin = nil
      }
    }
    let visible = store.visibleEvents
    sourceLabel.stringValue =
      store.telemetry != nil
      ? self.interfaceCopy.text("interface_detail.304ee84bba")
        + store.message(context: interfaceCopy.context)

      : store.backendAvailable
        ? self.interfaceCopy.text(
          "interface_detail.6089733c57", ["arg0": String(describing: store.events.count)])
        : self.interfaceCopy.text("interface_detail.61c63305fa")
    countLabel.stringValue =
      store.directoryIsSyncing
      ? self.interfaceCopy.text("interface_detail.a56af3c003")
      : store.telemetry != nil && (store.privacyInvalid || !store.initialized)
        ? self.interfaceCopy.text("interface_detail.dcf97098be")
        : store.query.isEmpty
          ? (store.telemetry == nil ? "" : self.interfaceCopy.text("interface_detail.4f191e636a"))
            + self.interfaceCopy.text(
              "interface_detail.25a3ca9484",
              [
                "arg0": String(describing: max(0, store.objects.count - 1)),
                "arg1": String(describing: store.dimension.title(context: interfaceCopy.context)),
              ])
          : self.interfaceCopy.text(
            "interface_detail.62ebc1540a",
            ["arg0": String(describing: max(0, store.objects.count - 1))])
    if store.telemetry != nil {
      let records = store.visibleRecords
      let emptyMessage = initialLogMessage
      if dimensionChanged || objectChanged || renderedRecords != records
        || (records.isEmpty && renderedEmptyMessage != emptyMessage)
      {
        let old = Dictionary(uniqueKeysWithValues: renderedRecords.map { ($0.id, $0) })
        let changed = Set(records.filter { dimensionChanged || old[$0.id] != $0 }.map(\.id))
        activeTable.apply(
          rows: records.map { .init(id: $0.id, values: []) }, emptyMessage: emptyMessage,
          selectedID: objectChanged ? "" : store.selectedEvent, reloading: changed,
          reusingVisibleCells: true)
        if objectChanged {
          closeInspector()
          activeTable.scrollToTop()
        } else if store.followsLatestPage, store.atTop {
          activeTable.scrollToTop()
        }
        renderedRecords = records
        renderedEmptyMessage = emptyMessage
      }
    } else if dimensionChanged || renderedEvents != visible {
      activeTable.apply(
        rows: visible.map { .init(id: $0.id, values: [$0.time, $0.title]) },
        emptyMessage: initialLogMessage, selectedID: store.selectedEvent)
      renderedEvents = visible
    }
  }
  func focusSearch() { view.window?.makeFirstResponder(search) }
  @objc private func searchChanged() { store.perform(.search(search.stringValue)) }
  func showEvent(_ id: String) {
    guard store.requestedObject == nil, !store.isLoading, let telemetry = store.telemetry,
      presentedViewControllers?.isEmpty != false
    else {
      return
    }
    invalidateDetailRequest()
    let request = detailGeneration
    let session = telemetry.sessionGeneration
    let previousHistory = telemetry.history
    let filter = store.filter()
    let readToken = store.readToken
    let selectedRecord = store.visibleRecords.first(where: { $0.id == id })
    let perspective: LogDetailPresentation.Perspective =
      store.dimension == .domain ? .domain : store.dimension == .system ? .system : .application
    detailTask = Task { [weak self] in
      do {
        let detail: HistoryLogDetail
        if self?.store.usesViews == true,
          let record = selectedRecord,
          !record.recordID.isEmpty
        {
          var q = LogDetailFilter()
          q.recordID = record.recordID
          q.query = filter
          q.query.readToken = readToken
          detail = try await telemetry.recordDetail(q)
        } else {
          detail = try await telemetry.detail(id)
        }
        guard let self, !Task.isCancelled, self.detailGeneration == request,
          session == telemetry.sessionGeneration,
          !telemetry.logsInvalidated(since: previousHistory, from: filter.from),
          telemetry.connected, telemetry.isShown(.logs, focus: pageRefresh == nil),
          self.store.filter() == filter, self.store.readToken == readToken,
          self.presentedViewControllers?.isEmpty != false
        else { return }
        let controller = LogEventDetailController(
          detail: detail, telemetry: telemetry,
          perspective: perspective,
          onTraffic: self.onTraffic, onRoute: self.onRoute, context: self.interfaceCopy.context)
        if self.store.usesViews && self.view.bounds.width >= PaxTheme.Logs.inspectorThreshold {
          self.closeInspector(cancelRequest: false)
          self.inspector = controller
          controller.onClose = { [weak self] in self?.closeInspector() }
          controller.preferredContentSize = .zero
          self.addChild(controller)
          let pane = controller.view
          pane.translatesAutoresizingMaskIntoConstraints = true
          pane.autoresizingMask = [.width, .height]
          pane.frame = self.inspectorHost.bounds
          self.inspectorHost.addSubview(pane)
          self.inspectorHost.isHidden = false
          self.inspectorWidth?.constant = PaxTheme.Logs.inspectorWidth
          self.cardTrailing?.constant = -PaxTheme.Logs.inspectorWidth - PaxTheme.Spacing.medium
        } else {
          self.presentWorkbenchModal(controller)
        }
      } catch {
        if !Task.isCancelled, let self, self.detailGeneration == request {
          self.sourceLabel.stringValue = ProductErrorPresentation.display(
            error, context: self.interfaceCopy.context)
        }
      }
    }
  }
  private func showDetail(_ id: String) {
    if store.telemetry != nil {
      showEvent(id)
      return
    }
    guard presentedViewControllers?.isEmpty != false,
      let event = store.events.first(where: { $0.id == id })
    else { return }
    presentWorkbenchModal(LogEventDetailController(event: event))
  }
  private func invalidateDetailRequest() {
    detailTask?.cancel()
    detailTask = nil
    detailGeneration &+= 1
  }
  private func closeInspector(cancelRequest: Bool = true) {
    if cancelRequest { invalidateDetailRequest() }
    inspector?.view.removeFromSuperview()
    inspector?.removeFromParent()
    inspector = nil
    inspectorHost.isHidden = true
    inspectorWidth?.constant = 0
    cardTrailing?.constant = 0
  }
  override func viewDidLayout() {
    super.viewDidLayout()
    inspector?.view.frame = inspectorHost.bounds
    if inspector != nil && view.bounds.width < PaxTheme.Logs.inspectorThreshold { closeInspector() }
  }
  override func viewWillAppear() {
    super.viewWillAppear()
    pageVisible = true
    entryPending = true
    syncRefresh()
    pageRefresh?.pageDidAppear(.logs)
  }
  override func viewWillDisappear() {
    super.viewWillDisappear()
    pageVisible = false
    pageRefresh?.pageDidDisappear(.logs)
    syncRefresh()
    detailTask?.cancel()
    detailGeneration &+= 1
    closeInspector()
  }
  @objc private func previousRecords() {
    closeInspector()
    store.changePage(-1)
  }
  @objc private func nextRecords() {
    closeInspector()
    store.changePage(1)
  }
  @objc func rangeChanged() {
    closeInspector()
    store.rangeIndex = range.indexOfSelectedItem
    store.resetRange()
  }
  @objc private func loadMoreObjects() { store.scheduleReload(moreObjects: true) }
  @objc private func openContextDetail(_ sender: LogDetailMenuItem) { showDetail(sender.recordID) }

}

extension LogsWorkbenchStore.Level {
  var presentationColor: NSColor {
    switch self {
    case .error: PaxTheme.Logs.errorText
    case .warning: PaxTheme.Logs.warningText
    case .info: PaxTheme.Logs.infoText
    case .all: PaxTheme.Color.text
    }
  }
}

@MainActor private final class LogDetailMenuItem: NSMenuItem { var recordID = "" }
