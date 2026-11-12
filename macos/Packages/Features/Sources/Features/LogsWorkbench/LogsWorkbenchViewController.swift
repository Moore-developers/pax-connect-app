import AppKit
import Observation
import PaxCore
import PaxUI

/// 固定工具栏与底栏；两张共享表格独立滚动。
@MainActor
final class LogsWorkbenchViewController: NSViewController {
  let store: LogsWorkbenchStore
  private let inspectorHost = NSView()
  private let objectIcons: WorkbenchObjectIcons
  private let connection: ConnectionStore?
  private let session: WorkbenchDemoStore?
  private let mode = PaxTabs(["代理", "直连"], style: .mode)
  private lazy var toolsHost = LogsFilterBar(level: level, range: range)
  private let level = PaxTabs(["全部级别", "错误", "警告", "信息"])
  private var renderedDimension: LogsWorkbenchStore.Dimension?

  private var inspector: LogEventDetailController?
  private var cardTrailing: NSLayoutConstraint?
  private var inspectorWidth: NSLayoutConstraint?
  private var detailInvalidation: UInt64 = 0
  private var revokedDirectoryOrigin: NSPoint?
  private lazy var previousPage = PaxRouteButton(
    "上一页", style: .quiet, target: self, action: #selector(previousRecords))
  private lazy var nextPage = PaxRouteButton(
    "下一页", style: .quiet, target: self, action: #selector(nextRecords))
  private let pageLabel = PaxText.label("第1页", font: PaxTheme.Typography.caption(), secondary: true)
  @ObservationIgnored private var renderedObjects: [LogsWorkbenchStore.ObjectRow] = []
  @ObservationIgnored private var renderedEvents: [LogPreviewEvent] = []
  private var renderedRecords: [LogRecord] = []
  private var renderedEmptyMessage = ""
  private var renderedSelection = ""
  var pageVisible = false
  let pageRefresh: PaxRefresh?
  var entryPending = false
  /// 第 1 页加载中到来的推送：加载完成后再拉一次最新。
  var latestPending = false
  var latestPendingGeneration: UInt64?
  private let tabs = PaxTabs(["应用", "域名", "系统"])
  private let search = PaxRouteSearchField()
  private let range = PaxRoutePicker(style: .toolbar)
  var loadTask: Task<Void, Never>?
  var detailTask: Task<Void, Never>?
  var detailGeneration: UInt64 = 0
  var onRoute: ((String) -> Void)?
  var onTraffic: ((String) -> Void)?
  private var initialLogMessage: String {
    guard store.telemetry != nil, store.initialized, store.liveMessage.isEmpty,
      !store.privacyInvalid, store.selectedObject == "all", store.level == .all
    else { return store.emptyMessage }
    return connection?.proxyEnabled == true
      ? "等待访问记录；正常上网后自动显示，Pax 操作也会记录在这里"
      : "当前未采集网络访问；开启代理后开始记录，已保存的访问和操作仍可查看"
  }
  private let sourceLabel = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private let countLabel = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private lazy var moreObjectsButton = PaxRouteButton(
    "更多", style: .quiet, target: self, action: #selector(loadMoreObjects))
  private lazy var directoryFooter = NSStackView(views: [countLabel, NSView(), moreObjectsButton])
  private let directoryScroll = NSScrollView()
  private let directory: PaxDataTable
  private let events: PaxDataTable
  private let systemEvents: PaxDataTable
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
        background: PaxTheme.Logs.directoryFill))
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
      title: "运行日志",
      accessory: ProxyStatusAccessory(mode: mode, connection: connection, presenter: self))
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
  private func buildColumns(in body: NSView) {
    let left = WorkbenchSurface()
    left.surfaceColor = PaxTheme.Logs.directoryFill
    let right = NSView()
    let divider = NSBox()
    divider.boxType = .separator
    for item in [left, divider, right] {
      item.translatesAutoresizingMaskIntoConstraints = false
      body.addSubview(item)
      item.topAnchor.constraint(equalTo: body.topAnchor).isActive = true
      item.bottomAnchor.constraint(equalTo: body.bottomAnchor).isActive = true
    }
    NSLayoutConstraint.activate([
      left.leadingAnchor.constraint(equalTo: body.leadingAnchor),
      left.widthAnchor.constraint(equalToConstant: PaxTheme.Logs.directoryWidth),
      divider.leadingAnchor.constraint(equalTo: left.trailingAnchor),
      divider.widthAnchor.constraint(equalToConstant: PaxTheme.Layout.cardBorder),
      right.leadingAnchor.constraint(equalTo: divider.trailingAnchor),
      right.trailingAnchor.constraint(equalTo: body.trailingAnchor),
    ])
    tabs.distributesEqually = true
    search.placeholderString = store.dimension == .system ? "筛选系统分类" : "筛选当前分类"
    search.sendsSearchStringImmediately = true
    search.sendsWholeSearchString = false
    search.setAccessibilityIdentifier("logs-object-search")
    search.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    search.setContentHuggingPriority(.defaultLow, for: .horizontal)
    let directoryDocument = WorkbenchDocument()
    directoryDocument.translatesAutoresizingMaskIntoConstraints = false
    directoryScroll.documentView = directoryDocument
    directoryScroll.hasVerticalScroller = true
    directoryScroll.autohidesScrollers = true
    directoryScroll.drawsBackground = false
    directoryDocument.widthAnchor.constraint(equalTo: directoryScroll.contentView.widthAnchor)
      .isActive = true
    directory.translatesAutoresizingMaskIntoConstraints = false
    directoryDocument.addSubview(directory)
    NSLayoutConstraint.activate([
      directory.leadingAnchor.constraint(equalTo: directoryDocument.leadingAnchor),
      directory.trailingAnchor.constraint(equalTo: directoryDocument.trailingAnchor),
      directory.topAnchor.constraint(equalTo: directoryDocument.topAnchor),
      directory.bottomAnchor.constraint(equalTo: directoryDocument.bottomAnchor),
    ])
    for item in [tabs, search, directoryScroll, directoryFooter] {
      item.translatesAutoresizingMaskIntoConstraints = false
      left.addSubview(item)
    }
    let inset = PaxTheme.Spacing.medium
    NSLayoutConstraint.activate([
      tabs.leadingAnchor.constraint(equalTo: left.leadingAnchor, constant: inset),
      tabs.trailingAnchor.constraint(equalTo: left.trailingAnchor, constant: -inset),
      tabs.topAnchor.constraint(equalTo: left.topAnchor, constant: inset),
      search.leadingAnchor.constraint(equalTo: tabs.leadingAnchor),
      search.trailingAnchor.constraint(equalTo: tabs.trailingAnchor),
      search.topAnchor.constraint(equalTo: tabs.bottomAnchor, constant: PaxTheme.Spacing.tight),
      search.heightAnchor.constraint(equalToConstant: PaxTheme.Routes.controlHeight),
      directoryScroll.leadingAnchor.constraint(equalTo: left.leadingAnchor),
      directoryScroll.trailingAnchor.constraint(equalTo: left.trailingAnchor),
      directoryScroll.topAnchor.constraint(
        equalTo: search.bottomAnchor, constant: PaxTheme.Spacing.tight),
      directoryScroll.bottomAnchor.constraint(equalTo: directoryFooter.topAnchor),
      directoryFooter.leadingAnchor.constraint(equalTo: tabs.leadingAnchor),
      directoryFooter.trailingAnchor.constraint(equalTo: tabs.trailingAnchor),
      directoryFooter.bottomAnchor.constraint(equalTo: left.bottomAnchor),
      directoryFooter.heightAnchor.constraint(equalToConstant: PaxTheme.Logs.footerHeight),
    ])
    level.setAccessibilityLabel("日志级别")
    level.setAccessibilityIdentifier("logs-level")
    range.addItems(withTitles: ["30分钟", "6小时", "24小时"])
    range.selectItem(at: store.rangeIndex)
    range.target = self
    range.action = #selector(rangeChanged)
    toolsHost.setAccessibilityIdentifier("logs-filters")
    range.setAccessibilityIdentifier("logs-range")
    let foot = NSStackView(views: [pageLabel, NSView(), previousPage, nextPage])
    foot.spacing = PaxTheme.Spacing.tight
    for item in [toolsHost, events, systemEvents, foot] {
      item.translatesAutoresizingMaskIntoConstraints = false
      right.addSubview(item)
    }
    NSLayoutConstraint.activate([
      toolsHost.leadingAnchor.constraint(equalTo: right.leadingAnchor, constant: inset),
      toolsHost.trailingAnchor.constraint(equalTo: right.trailingAnchor, constant: -inset),
      toolsHost.topAnchor.constraint(equalTo: right.topAnchor),
      toolsHost.heightAnchor.constraint(
        equalToConstant: PaxTheme.Logs.toolsHeight),
      events.leadingAnchor.constraint(equalTo: right.leadingAnchor),
      events.trailingAnchor.constraint(equalTo: right.trailingAnchor),
      events.topAnchor.constraint(equalTo: toolsHost.bottomAnchor),
      events.bottomAnchor.constraint(equalTo: foot.topAnchor),
      foot.leadingAnchor.constraint(equalTo: toolsHost.leadingAnchor),
      foot.trailingAnchor.constraint(equalTo: toolsHost.trailingAnchor),
      foot.bottomAnchor.constraint(equalTo: right.bottomAnchor),
      foot.heightAnchor.constraint(equalToConstant: PaxTheme.Logs.footerHeight),
    ])
    NSLayoutConstraint.activate([
      systemEvents.leadingAnchor.constraint(equalTo: events.leadingAnchor),
      systemEvents.trailingAnchor.constraint(equalTo: events.trailingAnchor),
      systemEvents.topAnchor.constraint(equalTo: events.topAnchor),
      systemEvents.bottomAnchor.constraint(equalTo: events.bottomAnchor),
    ])
    systemEvents.isHidden = true
    let line = NSBox()
    line.boxType = .separator
    line.translatesAutoresizingMaskIntoConstraints = false
    right.addSubview(line)
    NSLayoutConstraint.activate([
      line.leadingAnchor.constraint(equalTo: right.leadingAnchor),
      line.trailingAnchor.constraint(equalTo: right.trailingAnchor),
      line.bottomAnchor.constraint(equalTo: foot.topAnchor),
    ])
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
          title: "查看详情", action: #selector(self.openContextDetail(_:)), keyEquivalent: "")
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
      return PaxText.label(
        self.store.dimension == .ip ? "目标 IP" : self.store.dimension.title,
        font: PaxTheme.Typography.caption(), secondary: true)
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
          id: id, column: column, store: self.store, icons: self.objectIcons)
      else { return nil }
      guard self.store.telemetry != nil, column == 0 else { return content }
      return PaxDataTable.reusableContent(
        content, reuseKey: logObjectCellKey(id: id, store: self.store))
    }
    directory.cellUpdater = { [weak self] existing, id, column in
      guard let self, self.store.telemetry != nil else { return false }
      if column == 1 {
        guard let label = existing as? NSTextField,
          let object = self.store.objects.first(where: { $0.id == id })
        else { return false }
        let value = String(object.count)
        if label.stringValue != value { label.stringValue = value }
        return true
      }
      return PaxDataTable.updateContent(
        existing, reusing: logObjectCellKey(id: id, store: self.store)
      ) {
        logObjectCell(id: id, column: column, store: self.store, icons: self.objectIcons)
          ?? NSView()
      }
    }
    directory.onSelect = { [weak self] id in
      if let id {
        self?.closeInspector()
        self?.store.perform(.object(id))
      }
    }
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
  private func render() {
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
        events.revokeRows(emptyMessage: "正在重新读取日志")
        systemEvents.revokeRows(emptyMessage: "正在重新读取日志")
        renderedRecords = []
      }
      if !telemetry.isShown(.logs, focus: pageRefresh == nil) { return }
    }
    mode.selectedIndex = connection?.proxyEnabled == true || session?.isRunning == true ? 0 : 1
    mode.isEnabled = connection?.canToggleProxy ?? (session != nil)
    directory.setColumnTitle("记录", at: 1)
    let dimensionChanged = renderedDimension != store.dimension
    if dimensionChanged {
      events.setColumnTitle(
        store.dimension == .domain ? "域名 → 目标" : "应用 → 目标", at: 1, symbol: "arrow.up.right")
      renderedDimension = store.dimension
    }
    previousPage.isEnabled = store.canPreviousPage
    nextPage.isEnabled = store.canNextPage
    pageLabel.stringValue = store.pageTitle

    tabs.selectedIndex = store.dimension == .application ? 0 : store.dimension == .domain ? 1 : 2
    systemEvents.isHidden = store.dimension != .system
    events.isHidden = store.dimension == .system
    search.placeholderString = store.dimension == .system ? "筛选系统分类" : "筛选当前分类"
    search.setAccessibilityLabel(search.placeholderString)
    if search.currentEditor() == nil { search.stringValue = store.query }
    moreObjectsButton.isHidden = store.telemetry == nil || store.objectCursor.isEmpty
    if dimensionChanged || renderedObjects != store.objects
      || renderedSelection != store.selectedObject
    {
      let previous = Dictionary(uniqueKeysWithValues: renderedObjects.map { ($0.id, $0) })
      var changed = Set(
        store.objects.filter { dimensionChanged || previous[$0.id] != $0 }.map(\.id))
      if renderedSelection != store.selectedObject {
        changed.formUnion([renderedSelection, store.selectedObject])
      }
      directory.apply(
        rows: store.objects.enumerated().flatMap { index, object -> [PaxDataTable.Row] in
          let row = PaxDataTable.Row(id: object.id, values: [object.title, String(object.count)])
          return index == 0 ? [row, .init(id: "group", values: ["", ""])] : [row]
        },
        selectedID: store.selectedObject, reloading: store.telemetry == nil ? nil : changed,
        reusingVisibleCells: true)
      renderedObjects = store.objects
      renderedSelection = store.selectedObject
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
      ? "本机保存 · " + store.message

      : store.backendAvailable
        ? "v2rayN / sing-box · 演示数据（\(store.events.count) 条）" : "日志数据尚未接入后台"
    countLabel.stringValue =
      store.telemetry != nil && (store.privacyInvalid || !store.initialized)
      ? "正在读取目录"
      : store.query.isEmpty
        ? (store.telemetry == nil ? "" : "已加载 ")
          + "\(max(0, store.objects.count - 1)) 个\(store.dimension.title)"
        : "\(max(0, store.objects.count - 1)) 个匹配对象"
    if store.telemetry != nil {
      let records = store.visibleRecords
      let emptyMessage = initialLogMessage
      if dimensionChanged || renderedRecords != records
        || (records.isEmpty && renderedEmptyMessage != emptyMessage)
      {
        let old = Dictionary(uniqueKeysWithValues: renderedRecords.map { ($0.id, $0) })
        let changed = Set(records.filter { dimensionChanged || old[$0.id] != $0 }.map(\.id))
        activeTable.apply(
          rows: records.map { .init(id: $0.id, values: []) }, emptyMessage: emptyMessage,
          selectedID: store.selectedEvent, reloading: changed, reusingVisibleCells: true)
        if store.followsLatestPage, store.atTop { activeTable.scrollToTop() }
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
    guard !store.isLoading, let telemetry = store.telemetry,
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
          onTraffic: self.onTraffic, onRoute: self.onRoute)
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
        if !Task.isCancelled, self?.detailGeneration == request {
          self?.sourceLabel.stringValue = ProductErrorPresentation.display(error)
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
  @objc private func rangeChanged() {
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
