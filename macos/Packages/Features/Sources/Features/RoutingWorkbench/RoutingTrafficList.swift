import AppKit
import PaxCore
import PaxUI

/// 流量页与概览「流量分析」共用的列表：列标题、列宽比例、图标、对齐、单元格、行高、
/// 右键菜单与渲染都只在这里定义。两页各持一个实例，只决定数据窗口、滚动模式和详情落点。
@MainActor
final class RoutingTrafficList: NSObject {
  struct Style {
    var scrollMode: PaxDataTable.ScrollMode = .fixedHeader
    var selection: PaxDataTable.Selection = .single
    var identifiers = (application: "routing-traffic", system: "routing-system-services")
    /// 流量页的滚动与选中会冻结实时刷新；概览按固定周期刷新，不冻结。
    var freezesLive = true
    /// 流量页分页追加；概览固定只看前几行，不出现“加载更多”。
    var paging = true
    /// 隐藏的逻辑列（按 `titles` 下标：2 最近命中规则、3 延迟）。系统表按同一逻辑列隐藏；
    /// 剩余列按各自增长权重重排。默认全显示。
    var hiddenColumns: Set<Int> = []
    /// 概览「流量分析」：随页面滚动、不选中、不冻结、不分页，显示完整六列，保留延迟样本图。
    static var dashboard: Style {
      Style(
        scrollMode: .page, selection: .none,
        identifiers: (application: "workbench-traffic", system: "workbench-traffic-system"),
        freezesLive: false, paging: false)
    }
  }
  static let titles = ["应用", "实际连接", "最近命中规则", "延迟", "连接数", "上传 / 下载"]
  private static let symbols = [
    "app", "point.3.connected.trianglepath.dotted", "line.3.horizontal.decrease.circle",
    "timer", "number", "arrow.up.arrow.down",
  ]
  private static let systemTitles = ["系统与进程", "实际连接", "最近命中规则", "响应延迟", "连接数", "上传 / 下载"]
  private static let systemSymbols = [
    "gearshape", "point.3.connected.trianglepath.dotted", "line.3.horizontal.decrease.circle",
    "timer", "number", "arrow.up.arrow.down",
  ]
  /// 系统与进程复用相同六列，仅对象及延迟表头不同。
  private static let systemColumns = [0, 1, 2, 3, 4, 5]
  /// 可见表列 → 逻辑列（应用表）；系统表同理，二者都保留逻辑列序。
  private let applicationLogical: [Int]
  private let systemLogical: [Int]
  /// 逻辑列在可见应用表里的位置；被隐藏则为 nil。
  private func applicationIndex(_ logical: Int) -> Int? {
    applicationLogical.firstIndex(of: logical)
  }
  let store: RoutingWorkbenchStore
  let table: PaxDataTable
  let systemTable: PaxDataTable
  var onOpen: ((String) -> Void)?
  private let style: Style
  let icons: WorkbenchObjectIcons
  let tooltip: PaxChartTooltipCoordinator
  private var renderedRows: [PaxCore.TrafficRow] = []
  private var renderedScope: String?
  private var renderedHasMore = false
  private var renderedMinimumRows = 0
  private var renderedEmptyMessage = ""
  private var atTop = true
  private var selectedID: String?
  var activeTable: PaxDataTable { store.dimension == .system ? systemTable : table }
  private var activeColumns: [Int] {
    store.dimension == .system ? systemLogical : applicationLogical
  }
  private var scope: String {
    let tableID =
      store.dimension == .system ? style.identifiers.system : style.identifiers.application
    return
      "\(tableID)-\(store.dimension.rawValue)-\(store.period.rawValue)-\(store.direction.rawValue)-\(store.search)"
  }

  init(
    store: RoutingWorkbenchStore, icons: WorkbenchObjectIcons, tooltip: PaxChartTooltipCoordinator,
    style: Style = Style()
  ) {
    self.store = store
    self.icons = icons
    self.tooltip = tooltip
    self.style = style
    let application = Self.titles.indices.filter { !style.hiddenColumns.contains($0) }
    let system = Self.systemColumns.filter { !style.hiddenColumns.contains($0) }
    applicationLogical = application
    systemLogical = system
    table = Self.makeApplicationTable(style, logical: application)
    systemTable = Self.makeSystemTable(style, logical: system)
    super.init()
    table.setTableIdentifier(style.identifiers.application)
    systemTable.setTableIdentifier(style.identifiers.system)
    wire(table, logical: { [applicationLogical] in applicationLogical[$0] })
    wire(systemTable, logical: { [systemLogical] in systemLogical[$0] })
  }

  private static func makeApplicationTable(_ style: Style, logical: [Int]) -> PaxDataTable {
    PaxDataTable(
      columns: logical.map { index in
        let title = titles[index]
        return .init(
          title, width: columnWidth(title, style: style), growth: PaxTheme.Routing.columns[index],
          alignment: (2...4).contains(index) ? .center : .left, symbol: symbols[index],
          inset: style.scrollMode == .page
            ? PaxTheme.Dashboard.cellInset
            : index == 0 || index == 5 ? PaxTheme.Dashboard.inset : PaxTheme.Dashboard.cellInset,
          sortable: false, contentLayout: index == 2 || index == 4 ? .centered : .fill)
      },
      metrics: .workbench(),
      selection: style.selection, scrollMode: style.scrollMode)
  }

  private static func makeSystemTable(_ style: Style, logical: [Int]) -> PaxDataTable {
    let widths = PaxTheme.Routing.columns
    return PaxDataTable(
      columns: logical.map { column in
        let index = systemColumns.firstIndex(of: column) ?? 0
        let title = systemTitles[index]
        return .init(
          title, width: columnWidth(title, style: style), growth: widths[index],
          alignment: (2...4).contains(index) ? .center : .left,
          symbol: systemSymbols[index],
          inset: style.scrollMode == .page ? PaxTheme.Dashboard.cellInset : PaxTheme.Table.inset,
          sortable: false,
          contentLayout: index == 2 || index == 4 ? .centered : .fill)
      },
      metrics: .workbench(rounded: true),
      selection: style.selection, scrollMode: style.scrollMode)
  }

  /// 概览六列先保留完整表头和一致的内边距，再按共享权重分配余量。
  /// 对象标题按最长的「系统与进程」预留，切换维度不改变列位置。
  private static func columnWidth(_ title: String, style: Style) -> CGFloat {
    guard style.scrollMode == .page else { return 0 }
    let title = titles.firstIndex(of: title).map { systemTitles[$0] } ?? title
    let text = (title as NSString).size(withAttributes: [.font: PaxTheme.Typography.caption()])
    return ceil(text.width) + PaxTheme.Control.navigationIcon + PaxTheme.Spacing.xs
      + 2 * PaxTheme.Dashboard.cellInset
  }

  func syncHeaders() {
    table.setColumnTitle(
      store.dimension.title, at: 0,
      symbol: store.dimension == .application
        ? "app" : store.dimension == .domain ? "globe" : "gearshape")
    if let count = applicationIndex(4) { table.setColumnTitle("连接数", at: count, symbol: "number") }
  }

  var collecting: (() -> Bool)?
  func emptyMessage(unknownSourceBelow: Bool) -> String {
    if let message = store.dataMessage { return message }
    if store.telemetry == nil { return store.search.isEmpty ? "此时间窗暂无流量" : "没有匹配的目标" }
    if !store.search.isEmpty || store.direction != .all { return "此时间窗没有符合筛选的流量" }
    if collecting?() == false { return "当前未采集；开启代理后记录网络活动，已保存历史仍可查看" }
    if collecting?() == true && store.livePage.items.isEmpty { return "等待网络活动；打开平时访问的网站后查看" }
    if store.dimension == .system { return "当前时间与搜索范围暂无系统或进程连接" }
    if store.dimension == .application, unknownSourceBelow, store.livePage.unknownSource != nil {
      return "暂无已识别应用，未识别来源见下方"
    }
    return "此时间窗暂无流量"
  }

  /// 提交 Store 的当前结果；返回是否需要准备表格结构。
  /// `limit` 截取前几行，`minimumRows` 用占位行补足固定高度。
  @discardableResult
  func render(emptyMessage: String, limit: Int = .max, minimumRows: Int = 0) -> Bool {
    syncHeaders()
    guard store.telemetry != nil else {
      let ids = store.visibleRows.prefix(limit).map(\.id)
      activeTable.apply(
        rows: slots(
          ids, hasMore: style.paging && !store.livePage.nextCursor.isEmpty, minimum: minimumRows),
        emptyMessage: emptyMessage)
      return true
    }
    let page = store.livePage
    let rows = Array(page.items.prefix(limit))
    let hasMore = style.paging && !page.nextCursor.isEmpty
    let scope = self.scope
    let scopeChanged = scope != renderedScope
    let rowsChanged = rows != renderedRows
    let structureChanged =
      scopeChanged || hasMore != renderedHasMore || minimumRows != renderedMinimumRows
      || (rowsChanged && rows.map(\.id) != renderedRows.map(\.id))
    let emptyChanged =
      rows.isEmpty && !hasMore && minimumRows == 0 && emptyMessage != renderedEmptyMessage
    guard structureChanged || rowsChanged || emptyChanged else { return false }

    let table = activeTable
    if structureChanged || emptyChanged {
      table.apply(
        rows: slots(rows.map(\.id), hasMore: hasMore, minimum: minimumRows),
        emptyMessage: emptyMessage, reloading: [])
    }
    if renderedScope != nil && (scopeChanged || rowsChanged) {
      let columns = activeColumns
      let changes = changedCells(
        in: rows, columns: columns, scope: scope, scopeChanged: scopeChanged)
      table.updateCells(changes)
    }
    renderedRows = rows
    renderedScope = scope
    renderedHasMore = hasMore
    renderedMinimumRows = minimumRows
    renderedEmptyMessage = emptyMessage
    return structureChanged
  }

  private func changedCells(
    in rows: [PaxCore.TrafficRow], columns: [Int], scope: String, scopeChanged: Bool
  ) -> [String: IndexSet] {
    if scopeChanged {
      // A reattached table can still have cells created in its previous query.
      let allColumns = IndexSet(integersIn: columns.indices)
      return Dictionary(uniqueKeysWithValues: rows.map { ($0.id, allColumns) })
    }
    let previousRows = Dictionary(uniqueKeysWithValues: renderedRows.map { ($0.id, $0) })
    let dimension = store.dimension
    var changes: [String: IndexSet] = [:]
    for row in rows {
      guard let previous = previousRows[row.id], row != previous else { continue }
      let pinKey = trafficPinKey(id: row.id, scope: scope)
      let changed = IndexSet(
        columns.indices.filter { index in
          let column = columns[index]
          return routingTrafficCellKey(row, column: column, dimension: dimension, pinKey: pinKey)
            != routingTrafficCellKey(previous, column: column, dimension: dimension, pinKey: pinKey)
        })
      if !changed.isEmpty { changes[row.id] = changed }
    }
    return changes
  }

  private func trafficPinKey(id: String, scope: String) -> String {
    "traffic-list-\(scope)-\(id)"
  }

  private func cellKey(_ id: String, column: Int) -> RoutingTrafficCellKey? {
    guard let fact = store.liveFact(id: id) else { return nil }
    return routingTrafficCellKey(
      fact, column: column, dimension: store.dimension, pinKey: trafficPinKey(id: id, scope: scope))
  }

  func cell(_ id: String, column: Int, key: RoutingTrafficCellKey? = nil) -> NSView? {
    if id.hasPrefix("placeholder-") { return NSView() }
    return makeRoutingTrafficCell(
      id, column: column, key: key ?? cellKey(id, column: column), store: store,
      icons: icons, tooltip: tooltip
    ) {
      [weak self] in self?.onOpen?(id)
    }
  }

  private func slots(_ ids: [String], hasMore: Bool, minimum: Int) -> [PaxDataTable.Row] {
    var rows = ids.map { PaxDataTable.Row(id: $0, values: []) }
    if hasMore { rows.append(.init(id: "load-more", values: ["加载更多"])) }
    for index in rows.count..<max(rows.count, minimum) {
      rows.append(.init(id: "placeholder-\(index)", values: []))
    }
    return rows
  }

  private func wire(_ table: PaxDataTable, logical: @escaping (Int) -> Int) {
    table.cellProvider = { [weak self] id, column in
      guard let self else { return nil }
      let column = logical(column)
      let key = self.cellKey(id, column: column)
      guard let content = self.cell(id, column: column, key: key) else { return nil }
      // Empty slots must rebuild their cell constraints when a real row arrives.
      guard let key else { return content }
      return PaxDataTable.reusableContent(content, reuseKey: key)
    }
    table.cellUpdater = { [weak self] existing, id, column in
      guard let self else { return false }
      let column = logical(column)
      guard let key = self.cellKey(id, column: column) else { return false }
      return PaxDataTable.updateContent(
        existing, reusing: key,
        updating: {
          updateRoutingTrafficCell($0, key: key, store: self.store, tooltip: self.tooltip)
        },
        makeContent: { self.cell(id, column: column, key: key) ?? NSView() })
    }
    if style.freezesLive {
      table.onScrollPosition = { [weak self] atTop in
        guard let self, self.store.telemetry != nil else { return }
        self.atTop = atTop
        self.updateReading()
      }
      table.onSelect = { [weak self] id in
        guard let self, self.store.telemetry != nil else { return }
        self.selectedID = id
        self.updateReading()
      }
    }
    table.menuForRow = { [weak self] id in
      guard id != "load-more", !id.hasPrefix("placeholder-") else {
        return nil
      }
      let item = NSMenuItem(
        title: "查看详情", action: #selector(RoutingTrafficList.openContextDetail(_:)),
        keyEquivalent: "")
      item.target = self
      item.representedObject = id
      let menu = NSMenu()
      menu.addItem(item)
      return menu
    }
    table.heightForRow = { _ in PaxTheme.Table.memberRow }
    table.onActivate = { [weak self] id in
      guard id != "load-more", !id.hasPrefix("placeholder-") else {
        return
      }
      self?.onOpen?(id)
    }
  }

  private func updateReading() {
    if atTop && selectedID == nil { store.resumeLiveStructure() } else { store.liveFrozen = true }
  }

  @objc private func openContextDetail(_ item: NSMenuItem) {
    if let id = item.representedObject as? String { onOpen?(id) }
  }
}
