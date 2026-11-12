import AppKit

/// 数据表只接收稳定 ID、显示值与单元格工厂；不读取业务 Store。
public final class PaxDataTable: NSView, NSTableViewDelegate {
  /// Only main page lists opt into an independently scrolling body.
  public enum ScrollMode { case page, fixedHeader }
  public let scrollMode: ScrollMode
  private var contentMinimum: NSLayoutConstraint?
  private var bodyHeight: CGFloat {
    rows.isEmpty
      ? (metrics.headerHeight > 0 ? metrics.rowHeight : 0)
      : rows.reduce(0) { $0 + (heightForRow?($1.id) ?? metrics.rowHeight) }
  }
  public override var isFlipped: Bool { true }
  public override var isHidden: Bool {
    didSet { if scrollMode == .page { contentMinimum?.isActive = !isHidden } }
  }
  public override var intrinsicContentSize: NSSize {
    scrollMode == .page
      ? NSSize(width: NSView.noIntrinsicMetric, height: metrics.headerHeight + bodyHeight)
      : super.intrinsicContentSize
  }
  public enum Selection { case none, browse, single, multiple }
  public enum RowAppearance { case normal, current }
  public enum ContentLayout { case centered, fill }
  public struct Metrics {
    public var rowHeight: CGFloat
    public var headerHeight: CGFloat
    public var rounded: Bool
    public var rowSeparators: Bool
    public var selectionRail: Bool
    public var alternateBackground: NSColor?
    public var background: NSColor
    public var headerTopBorder: Bool
    public var headerBackground: NSColor
    public var selectionInset: CGFloat
    public init(
      rowHeight: CGFloat = PaxTheme.Table.row, headerHeight: CGFloat = PaxTheme.Table.header,
      rounded: Bool = true, rowSeparators: Bool = true, selectionInset: CGFloat = 0,
      selectionRail: Bool = false, background: NSColor = PaxTheme.Table.fill,
      headerBackground: NSColor = PaxTheme.Table.headerFill, headerTopBorder: Bool = false,
      alternateBackground: NSColor? = nil
    ) {
      self.alternateBackground = alternateBackground
      self.rowHeight = rowHeight
      self.headerHeight = headerHeight
      self.rounded = rounded
      self.rowSeparators = rowSeparators
      self.selectionInset = selectionInset
      self.selectionRail = selectionRail
      self.background = background
      self.headerBackground = headerBackground
      self.headerTopBorder = headerTopBorder
    }
  }
  public struct Column {
    public let title: String
    public let width: CGFloat
    public let growth: CGFloat
    public let numeric: Bool
    public let alignment: NSTextAlignment
    public let symbol: String?
    public let inset: CGFloat
    public let sortable: Bool
    public let contentLayout: ContentLayout
    public let toolTip: String?
    public init(
      _ title: String, width: CGFloat, numeric: Bool = false, growth: CGFloat = 1,
      alignment: NSTextAlignment? = nil, symbol: String? = nil,
      inset: CGFloat = PaxTheme.Table.inset, sortable: Bool? = nil,
      contentLayout: ContentLayout = .centered, toolTip: String? = nil
    ) {
      self.toolTip = toolTip
      self.title = title
      self.width = width
      self.growth = growth
      self.numeric = numeric
      self.alignment = alignment ?? (numeric ? .right : .left)
      self.symbol = symbol
      self.inset = inset
      self.sortable = sortable ?? !numeric
      self.contentLayout = contentLayout
    }
  }
  public struct Row {
    public let id: String
    public let values: [String]
    public let subtitle: String
    public let numericValues: [Int: Double]
    public init(
      id: String, values: [String], subtitle: String = "", numericValues: [Int: Double] = [:]
    ) {
      self.id = id
      self.values = values
      self.subtitle = subtitle
      self.numericValues = numericValues
    }
  }
  public var canSelectRow: ((String) -> Bool)?
  public var onActivate: ((String) -> Void)?
  public var onSelection: ((Set<String>) -> Void)?
  public var onSelect: ((String?) -> Void)?
  public var onScrollPosition: ((Bool) -> Void)?
  public private(set) var applyCount = 0
  public private(set) var fullReloadCount = 0
  public private(set) var partialReloadCount = 0
  public var onRowClick: ((String) -> Void)?
  public var onMove: ((String, Int) -> Void)?
  public var canMoveRow: ((String) -> Bool)?
  /// 分组说明与列表结束提示可跨列呈现；普通数据行仍由 cellProvider 构建。
  public var rowContent: ((String) -> NSView?)?
  public var heightForRow: ((String) -> CGFloat)?
  /// Updates an existing custom content view during native cell reuse.
  public var cellUpdater: ((NSView, String, Int) -> Bool)?
  public var cellProvider: ((String, Int) -> NSView?)?
  public var rowAppearance: ((String) -> RowAppearance)?
  public var menuForRow: ((String) -> NSMenu?)?
  /// An owning store may sort raw domain values while the table owns header feedback.
  public var onSort: ((Int, Bool) -> Void)?
  public var contextOnReturn = false
  private let columns: [Column]
  let metrics: Metrics
  private let selection: Selection
  var rows: [Row] = []
  let table = PaxNativeTable()
  let scroll = NSScrollView()
  private var source: PaxTableDataSource?
  private var sortedColumn: Int?
  private var ascending = true
  private var applying = false
  private var initialScroll = true
  private var revokedViewport: (ids: [String], origin: NSPoint, top: Int, offset: CGFloat)?
  private let emptyLabel = PaxText.label("暂无数据", secondary: true)
  private let roundedMask = CAShapeLayer()
  public var displayedRowCount: Int { rows.count }
  public init(
    columns: [Column], rows: [Row] = [], metrics: Metrics = .init(), selection: Selection = .single,
    scrollMode: ScrollMode = .page
  ) {
    self.scrollMode = scrollMode
    self.columns = columns
    self.metrics = metrics
    self.selection = selection
    super.init(frame: .zero)
    wantsLayer = true
    if metrics.rounded { layer?.mask = roundedMask }
    table.allowsMultipleSelection = selection == .multiple
    table.style = .plain
    table.selectionHighlightStyle = .regular
    table.rowHeight = metrics.rowHeight
    table.intercellSpacing = .zero
    table.columnAutoresizingStyle = .noColumnAutoresizing
    table.backgroundColor = metrics.background
    table.focusRingType = .none
    table.cornerView = nil
    let header = PaxDataHeader()
    header.height = metrics.headerHeight
    header.fill = metrics.headerBackground
    header.topBorder = metrics.headerTopBorder
    table.headerView = metrics.headerHeight > 0 ? header : nil
    for (index, config) in columns.enumerated() {
      let column = NSTableColumn(identifier: .init(String(index)))
      column.minWidth = 0
      column.maxWidth = .greatestFiniteMagnitude
      column.width = config.width
      column.isEditable = false
      column.resizingMask = []
      column.headerCell = PaxDataHeaderCell(config)
      column.headerToolTip = config.toolTip
      table.addTableColumn(column)
    }
    source = PaxTableDataSource(tableView: table) {
      [weak self] _, column, row, _ in
      self?.makeCell(tableColumn: column, row: row) ?? NSView()
    }
    source?.owner = self
    source?.rowViewProvider = { [weak self] table, row, _ in
      self?.tableView(table, rowViewForRow: row) ?? NSTableRowView()
    }
    table.delegate = self
    table.activate = { [weak self] in self?.activateRow() }
    table.context = { [weak self] index in
      guard let self, self.rows.indices.contains(index) else { return nil }
      return self.menuForRow?(self.rows[index].id)
    }
    table.registerForDraggedTypes([.init("app.pax.table-row")])
    table.setDraggingSourceOperationMask(.move, forLocal: true)
    table.target = self
    table.action = #selector(clickRow)
    table.doubleAction = #selector(doubleClickRow(_:))
    if scrollMode == .fixedHeader {
      scroll.documentView = table
      scroll.contentView.postsBoundsChangedNotifications = true
      NotificationCenter.default.addObserver(
        self, selector: #selector(scrolled), name: NSView.boundsDidChangeNotification,
        object: scroll.contentView)
      scroll.hasVerticalScroller = true
      scroll.hasHorizontalScroller = true
      scroll.autohidesScrollers = true
      scroll.drawsBackground = false
      scroll.automaticallyAdjustsContentInsets = false
      scroll.translatesAutoresizingMaskIntoConstraints = false
      addSubview(scroll)
      NSLayoutConstraint.activate([
        scroll.leadingAnchor.constraint(equalTo: leadingAnchor),
        scroll.trailingAnchor.constraint(equalTo: trailingAnchor),
        scroll.topAnchor.constraint(equalTo: topAnchor),
        scroll.bottomAnchor.constraint(equalTo: bottomAnchor),
      ])
    } else {
      addSubview(table)
      if let header = table.headerView { addSubview(header) }
      contentMinimum = heightAnchor.constraint(
        greaterThanOrEqualToConstant: metrics.headerHeight + metrics.rowHeight)
      contentMinimum?.isActive = true
      setContentHuggingPriority(.defaultHigh, for: .vertical)
    }
    emptyLabel.translatesAutoresizingMaskIntoConstraints = false
    addSubview(emptyLabel)
    emptyLabel.alignment = .center
    NSLayoutConstraint.activate([
      emptyLabel.centerXAnchor.constraint(equalTo: centerXAnchor),
      emptyLabel.centerYAnchor.constraint(
        equalTo: centerYAnchor, constant: metrics.headerHeight / 2),
      emptyLabel.widthAnchor.constraint(
        lessThanOrEqualTo: widthAnchor, constant: -PaxTheme.Spacing.medium * 2),
    ])
    table.setAccessibilityLabel("数据表")
    apply(rows: rows)
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public func setTableIdentifier(_ identifier: String) {
    table.setAccessibilityIdentifier(identifier)
  }
  public func apply(
    rows: [Row], emptyMessage: String = "暂无数据", selectedID: String? = nil,
    reloading changedIDs: Set<String>? = nil, selectedIDs: Set<String>? = nil,
    reusingVisibleCells: Bool = false
  ) {
    applyCount += 1
    let oldIDs = Set(
      table.selectedRowIndexes.compactMap {
        self.rows.indices.contains($0) ? self.rows[$0].id : nil
      })
    let oldSelected =
      table.selectedRow >= 0 && self.rows.indices.contains(table.selectedRow)
      ? self.rows[table.selectedRow].id : nil
    applying = true
    defer { applying = false }
    let previousIDs = self.rows.map(\.id)
    let savedViewport = rows.isEmpty ? nil : revokedViewport
    let scrollOrigin = savedViewport?.origin ?? scroll.contentView.bounds.origin
    let topRow = savedViewport?.top ?? table.row(at: NSPoint(x: 0, y: scrollOrigin.y))
    let anchorRows = savedViewport?.ids ?? previousIDs
    let anchorIndex = max(0, topRow)
    let anchorCandidates =
      Array(anchorRows.dropFirst(anchorIndex))
      + Array(anchorRows.prefix(anchorIndex).reversed())
    let nextIDs = Set(rows.map(\.id))
    let anchorID = anchorCandidates.first { nextIDs.contains($0) }
    let anchorOffset =
      savedViewport?.offset
      ?? (topRow >= 0 ? scrollOrigin.y - table.rect(ofRow: topRow).minY : 0)
    self.rows = rows
    sortRows()
    let structureChanged = previousIDs != self.rows.map(\.id)
    var snapshot = NSDiffableDataSourceSnapshot<String, String>()
    snapshot.appendSections(["rows"])
    snapshot.appendItems(self.rows.map(\.id))
    if changedIDs == nil {
      source?.apply(snapshot, animatingDifferences: false)
      fullReloadCount += 1
      table.reloadData()
    } else {
      if structureChanged {
        NSAnimationContext.runAnimationGroup { context in
          context.duration = 0
          context.allowsImplicitAnimation = false
          source?.apply(snapshot, animatingDifferences: true)
        }
      }
      // Insertions create cells; reload only surviving IDs.
      let survivingChanges = (changedIDs ?? []).intersection(Set(previousIDs))
      if !survivingChanges.isEmpty {
        let indices = IndexSet(
          self.rows.indices.filter { survivingChanges.contains(self.rows[$0].id) })
        partialReloadCount += 1
        let reload =
          reusingVisibleCells ? updateVisibleCells(in: indices) : indices
        if !reload.isEmpty {
          // AppKit invalidates offscreen cells lazily; never force their view creation.
          table.reloadData(
            forRowIndexes: reload, columnIndexes: IndexSet(integersIn: 0..<columns.count))
        }
      }
    }
    let id = selectedID ?? oldSelected
    if selection == .multiple {
      let selected = selectedIDs ?? (oldIDs.isEmpty ? Set([selectedID].compactMap { $0 }) : oldIDs)
      table.selectRowIndexes(
        IndexSet(self.rows.indices.filter { selected.contains(self.rows[$0].id) }),
        byExtendingSelection: false)
    } else if let id, let index = self.rows.firstIndex(where: { $0.id == id }), selection != .none {
      table.selectRowIndexes(IndexSet(integer: index), byExtendingSelection: false)
    } else {
      table.deselectAll(nil)
    }
    emptyLabel.stringValue = emptyMessage
    emptyLabel.isHidden = !rows.isEmpty || emptyMessage.isEmpty
    if scrollMode == .fixedHeader, changedIDs != nil {
      needsLayout = true
      layoutSubtreeIfNeeded()
      var origin = scrollOrigin
      if structureChanged, let anchorID,
        let index = self.rows.firstIndex(where: { $0.id == anchorID })
      {
        origin.y = table.rect(ofRow: index).minY + anchorOffset
      }
      let clip = scroll.contentView
      clip.scroll(
        to: clip.constrainBoundsRect(.init(origin: origin, size: clip.bounds.size)).origin)
      scroll.reflectScrolledClipView(scroll.contentView)
    }
    if scrollMode == .page {
      contentMinimum?.constant = metrics.headerHeight + bodyHeight
      invalidateIntrinsicContentSize()
    }
    if !rows.isEmpty { revokedViewport = nil }
    needsLayout = true
  }
  /// Revoke private row contents immediately while keeping only an opaque reading anchor.
  public func revokeRows(emptyMessage: String) {
    if revokedViewport == nil, !rows.isEmpty, scrollMode == .fixedHeader {
      let origin = scroll.contentView.bounds.origin
      let top = table.row(at: NSPoint(x: 0, y: origin.y))
      revokedViewport = (
        rows.map(\.id), origin, max(0, top),
        top >= 0 ? origin.y - table.rect(ofRow: top).minY : 0
      )
    }
    apply(rows: [], emptyMessage: emptyMessage, reloading: [])
  }
  /// Reuse visible cells in either scroll mode; offscreen cells are invalidated lazily.
  private func updateVisibleCells(in changed: IndexSet) -> IndexSet {
    guard let cellUpdater else { return changed }
    let visible = table.rows(in: table.visibleRect)
    guard visible.location != NSNotFound, visible.length > 0 else { return changed }
    var reload = changed
    for row in changed.intersection(IndexSet(integersIn: visible.location..<NSMaxRange(visible))) {
      var updated = true
      for column in columns.indices {
        guard let cell = table.view(atColumn: column, row: row, makeIfNecessary: false),
          let content = cell.subviews.first,
          cellUpdater(content, rows[row].id, column)
        else {
          updated = false
          continue
        }
      }
      if updated { reload.remove(row) }
    }
    return reload
  }

  @objc private func scrolled() {
    if !applying, revokedViewport == nil {
      onScrollPosition?(
        scroll.contentView.bounds.origin.y + scroll.contentView.contentInsets.top <= 1)
    }
  }
  /// Prepare the bounded rows of an embedded page table when its data arrives,
  /// rather than creating their content on the first scrolling display deadline.
  /// Full scrolling lists retain NSTableView's normal visible-row virtualization.
  public func preparePageRows() {
    guard scrollMode == .page else { return }
    for row in rows.indices {
      for column in columns.indices {
        _ = table.view(atColumn: column, row: row, makeIfNecessary: true)
      }
    }
  }

  /// Opt-in stable content host used by history/analysis cellUpdater consumers.
  public static func reusableContent(_ content: NSView, reuseKey: AnyHashable? = nil) -> NSView {
    PaxTableContentHost(content, reuseKey: reuseKey)
  }
  /// Preserve an unchanged value-only cell even when the row identity changes.
  public static func updateContent(
    _ existing: NSView, reusing key: AnyHashable, makeContent: () -> NSView
  ) -> Bool {
    guard let host = existing as? PaxTableContentHost else { return false }
    guard host.reuseKey != key else { return true }
    host.replace(makeContent())
    host.reuseKey = key
    return true
  }
  public static func updateContent(_ existing: NSView, content: NSView) -> Bool {
    guard let host = existing as? PaxTableContentHost else { return false }
    host.replace(content)
    return true
  }
  public func setColumnTitle(_ title: String, at index: Int, symbol: String? = nil) {
    guard table.tableColumns.indices.contains(index) else { return }
    let cell = table.tableColumns[index].headerCell
    let symbolChanged = symbol.map { (cell as? PaxDataHeaderCell)?.symbol != $0 } ?? false
    guard cell.stringValue != title || symbolChanged else { return }
    cell.stringValue = title
    if let symbol, let header = cell as? PaxDataHeaderCell { header.symbol = symbol }
    table.headerView?.needsDisplay = true
  }
  public override func layout() {
    super.layout()
    if scrollMode == .page {
      table.frame = NSRect(
        x: 0, y: metrics.headerHeight, width: bounds.width,
        height: max(bodyHeight, bounds.height - metrics.headerHeight))
      table.headerView?.frame = NSRect(
        x: 0, y: 0, width: bounds.width, height: metrics.headerHeight)
    }
    let available = scrollMode == .fixedHeader ? scroll.contentSize.width : bounds.width
    let base = columns.reduce(CGFloat(0)) { $0 + $1.width }
    let growth = columns.reduce(CGFloat(0)) { $0 + $1.growth }
    let extra = max(0, available - base)
    for (index, column) in table.tableColumns.enumerated() {
      let width = columns[index].width + (growth > 0 ? extra * columns[index].growth / growth : 0)
      if column.width != width { column.width = width }
    }
    table.headerView?.frame.size.height = metrics.headerHeight
    if scrollMode == .fixedHeader {
      scroll.tile()
      // Native insertions can add rows to the empty viewport's minimum height.
      let height = max(bodyHeight, scroll.contentSize.height)
      if table.frame.height != height {
        table.setFrameSize(NSSize(width: table.frame.width, height: height))
      }
    }
    if scrollMode == .fixedHeader, initialScroll, window != nil, !rows.isEmpty,
      scroll.contentSize.height > 0
    {
      initialScroll = false
      Task { @MainActor [weak self] in
        await Task.yield()
        guard let self, self.window != nil else { return }
        self.layoutSubtreeIfNeeded()
        self.scroll.contentView.scroll(to: .zero)
        self.scroll.reflectScrolledClipView(self.scroll.contentView)
        self.table.scrollRowToVisible(0)
      }
    }
    if metrics.rounded {
      roundedMask.frame = bounds
      roundedMask.path = CGPath(
        roundedRect: bounds, cornerWidth: PaxTheme.Table.radius,
        cornerHeight: PaxTheme.Table.radius, transform: nil)
    }
  }
  private func makeCell(tableColumn: NSTableColumn?, row: Int) -> NSView? {
    guard rows.indices.contains(row),
      let index = tableColumn.flatMap({ Int($0.identifier.rawValue) }),
      columns.indices.contains(index)
    else { return nil }
    let item = rows[row]
    let config = columns[index]
    let identifier = NSUserInterfaceItemIdentifier("pax-column-\(index)")
    let cell =
      (table.makeView(withIdentifier: identifier, owner: self) as? PaxReusableTableCell)
      ?? PaxReusableTableCell()
    cell.identifier = identifier
    if let existing = cell.subviews.first, cellUpdater?(existing, item.id, index) == true {
      return cell
    }
    NSLayoutConstraint.deactivate(cell.contentConstraints)
    cell.contentConstraints = []
    for view in cell.subviews { view.removeFromSuperview() }
    cell.textField = nil
    let content: NSView
    if let custom = cellProvider?(item.id, index) {
      content = custom
    } else {
      let value = item.values.indices.contains(index) ? item.values[index] : "—"
      let label = PaxText.label(
        value,
        font: config.numeric
          ? PaxTheme.Typography.metric(size: PaxTheme.Table.font)
          : PaxTheme.Typography.label(size: PaxTheme.Table.font))
      label.textColor = PaxTheme.Table.text
      label.alignment = config.alignment
      label.lineBreakMode = .byTruncatingTail
      cell.textField = label
      if index == 0 && !item.subtitle.isEmpty {
        let sub = PaxText.label(item.subtitle, font: PaxTheme.Typography.caption(), secondary: true)
        sub.lineBreakMode = .byTruncatingTail
        let stack = NSStackView(views: [label, sub])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = PaxTheme.Spacing.xs
        label.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
        content = stack
      } else {
        content = label
      }
    }
    content.translatesAutoresizingMaskIntoConstraints = false
    cell.addSubview(content)
    cell.contentConstraints = [
      content.leadingAnchor.constraint(equalTo: cell.leadingAnchor, constant: config.inset),
      content.trailingAnchor.constraint(equalTo: cell.trailingAnchor, constant: -config.inset),
      content.centerYAnchor.constraint(equalTo: cell.centerYAnchor),
      content.heightAnchor.constraint(lessThanOrEqualTo: cell.heightAnchor),
    ]
    if config.contentLayout == .fill {
      cell.contentConstraints.append(content.heightAnchor.constraint(equalTo: cell.heightAnchor))
    } else if content.intrinsicContentSize.height == NSView.noIntrinsicMetric {
      // NSStackView 没有 intrinsic height，但 fittingSize 能给出紧凑的文字/标签高度。
      cell.contentConstraints.append(
        content.heightAnchor.constraint(
          equalToConstant: min(metrics.rowHeight, content.fittingSize.height)))
    }
    NSLayoutConstraint.activate(cell.contentConstraints)
    return cell
  }
  public func tableView(_ tableView: NSTableView, shouldSelectRow row: Int) -> Bool {
    selection != .none && rows.indices.contains(row) && canSelectRow?(rows[row].id) != false
  }
  public func tableViewSelectionDidChange(_ notification: Notification) {
    guard !applying else { return }
    let row = table.selectedRow
    onSelect?(rows.indices.contains(row) ? rows[row].id : nil)
    onSelection?(
      Set(table.selectedRowIndexes.compactMap { rows.indices.contains($0) ? rows[$0].id : nil }))
  }
  public func tableView(_ tableView: NSTableView, rowViewForRow row: Int) -> NSTableRowView? {
    let view = PaxDataRowView()
    view.current = rows.indices.contains(row) && rowAppearance?(rows[row].id) == .current
    view.highlightsSelection = selection == .single || selection == .multiple
    view.rowSeparators = metrics.rowSeparators
    view.selectionInset = metrics.selectionInset
    view.selectionRail = metrics.selectionRail
    view.fill = metrics.background
    view.alternateFill = metrics.alternateBackground
    view.table = table
    if rows.indices.contains(row), let content = rowContent?(rows[row].id) {
      content.translatesAutoresizingMaskIntoConstraints = false
      view.addSubview(content)
      view.setAccessibilityChildren([content])
      NSLayoutConstraint.activate([
        content.leadingAnchor.constraint(
          equalTo: view.leadingAnchor, constant: PaxTheme.Table.inset),
        content.trailingAnchor.constraint(
          equalTo: view.trailingAnchor, constant: -PaxTheme.Table.inset),
        content.centerYAnchor.constraint(equalTo: view.centerYAnchor),
      ])
    }
    return view
  }
  public func tableView(_ tableView: NSTableView, heightOfRow row: Int) -> CGFloat {
    guard rows.indices.contains(row) else { return metrics.rowHeight }
    return heightForRow?(rows[row].id) ?? metrics.rowHeight
  }
  public func tableView(_ tableView: NSTableView, didClick tableColumn: NSTableColumn) {
    guard let index = Int(tableColumn.identifier.rawValue), columns[index].sortable else { return }
    ascending = sortedColumn == index ? !ascending : true
    sortedColumn = index
    if let onSort {
      onSort(index, ascending)
      return
    }
    setSortIndicator(column: index, ascending: ascending)
    apply(rows: rows)
  }
  public func setSortIndicator(column index: Int?, ascending: Bool = true) {
    sortedColumn = index
    self.ascending = ascending
    for (i, column) in table.tableColumns.enumerated() {
      column.headerCell.stringValue =
        columns[i].title + (i == index ? (ascending ? " ↑" : " ↓") : "")
      column.headerCell.image =
        i == index
        ? PaxIcon.image(
          ascending ? "arrow.up" : "arrow.down", size: PaxTheme.Control.navigationIcon) : nil
    }
    table.headerView?.needsDisplay = true
  }
  private func sortRows() {
    guard onSort == nil, let column = sortedColumn else { return }
    rows.sort { left, right in
      if columns[column].numeric {
        let l = left.numericValues[column]
        let r = right.numericValues[column]
        if l == nil || r == nil { return l != nil && r == nil }
        if let l, let r, l != r { return ascending ? l < r : l > r }
        return left.id < right.id
      }
      let l = left.values.indices.contains(column) ? left.values[column] : ""
      let r = right.values.indices.contains(column) ? right.values[column] : ""
      let result = l.localizedStandardCompare(r)
      return result == .orderedSame
        ? left.id < right.id
        : ascending ? result == .orderedAscending : result == .orderedDescending
    }
  }
  @objc private func doubleClickRow(_ sender: NSTableView) {
    guard rows.indices.contains(sender.clickedRow) else { return }
    onActivate?(rows[sender.clickedRow].id)
  }
  @objc private func clickRow() {
    if rows.indices.contains(table.clickedRow) { onRowClick?(rows[table.clickedRow].id) }
  }
  public func tableView(_ tableView: NSTableView, pasteboardWriterForRow row: Int)
    -> NSPasteboardWriting?
  {
    guard onMove != nil, rows.indices.contains(row), canMoveRow?(rows[row].id) != false else {
      return nil
    }
    let item = NSPasteboardItem()
    item.setString(rows[row].id, forType: .init("app.pax.table-row"))
    return item
  }
  public func tableView(
    _ tableView: NSTableView, validateDrop info: NSDraggingInfo, proposedRow row: Int,
    proposedDropOperation operation: NSTableView.DropOperation
  ) -> NSDragOperation {
    guard onMove != nil, info.draggingSource as? NSTableView === table,
      let id = info.draggingPasteboard.string(forType: .init("app.pax.table-row")),
      canMoveRow?(id) != false
    else { return [] }
    table.setDropRow(row, dropOperation: .above)
    return .move
  }
  public func tableView(
    _ tableView: NSTableView, acceptDrop info: NSDraggingInfo, row: Int,
    dropOperation: NSTableView.DropOperation
  ) -> Bool {
    guard let onMove, let id = info.draggingPasteboard.string(forType: .init("app.pax.table-row")),
      let old = rows.firstIndex(where: { $0.id == id }), canMoveRow?(id) != false
    else { return false }
    onMove(id, min(rows.count - 1, max(0, row > old ? row - 1 : row)))
    return true
  }
  @objc private func activateRow() {
    guard rows.indices.contains(table.selectedRow) else { return }
    if contextOnReturn, let menu = table.context?(table.selectedRow) {
      table.present(menu, row: table.selectedRow)
    } else {
      onActivate?(rows[table.selectedRow].id)
    }
  }
}

@MainActor
private final class PaxTableDataSource: NSTableViewDiffableDataSource<String, String> {
  weak var owner: PaxDataTable?
  @objc func tableView(_ tableView: NSTableView, pasteboardWriterForRow row: Int)
    -> NSPasteboardWriting?
  { owner?.tableView(tableView, pasteboardWriterForRow: row) }
  @objc func tableView(
    _ tableView: NSTableView, validateDrop info: NSDraggingInfo, proposedRow row: Int,
    proposedDropOperation operation: NSTableView.DropOperation
  ) -> NSDragOperation {
    owner?.tableView(
      tableView, validateDrop: info, proposedRow: row, proposedDropOperation: operation) ?? []
  }
  @objc func tableView(
    _ tableView: NSTableView, acceptDrop info: NSDraggingInfo, row: Int,
    dropOperation: NSTableView.DropOperation
  ) -> Bool {
    owner?.tableView(tableView, acceptDrop: info, row: row, dropOperation: dropOperation) ?? false
  }
}
final class PaxNativeTable: NSTableView {
  var activate: (() -> Void)?
  var context: ((Int) -> NSMenu?)?
  override func menu(for event: NSEvent) -> NSMenu? {
    let index = row(at: convert(event.locationInWindow, from: nil))
    guard index >= 0 else { return nil }
    return context?(index)
  }
  func present(_ menu: NSMenu, row: Int) {
    let point = NSPoint(x: PaxTheme.Table.inset, y: rect(ofRow: row).midY)
    menu.popUp(positioning: nil, at: point, in: self)
  }
  override func keyDown(with event: NSEvent) {
    if event.keyCode == 109 && event.modifierFlags.contains(.shift), selectedRow >= 0,
      let menu = context?(selectedRow)
    {
      present(menu, row: selectedRow)
    } else if event.keyCode == 36 || event.keyCode == 76 || event.keyCode == 49 {
      activate?()
    } else {
      super.keyDown(with: event)
    }
  }
}
private final class PaxDataRowView: NSTableRowView {
  override var interiorBackgroundStyle: NSView.BackgroundStyle { .normal }
  var current = false
  var highlightsSelection = false
  var rowSeparators = true
  var selectionInset: CGFloat = 0
  var selectionRail = false
  var fill = PaxTheme.Table.fill
  var alternateFill: NSColor?
  weak var table: NSTableView?
  override func drawBackground(in dirtyRect: NSRect) {
    let index = table?.row(for: self) ?? -1
    let background = index >= 0 && index % 2 == 1 ? alternateFill ?? fill : fill
    (current ? PaxTheme.Routes.selectionFill : background).setFill()
    bounds.fill()
    if current {
      PaxTheme.Table.rail.setFill()
      NSRect(x: 0, y: 0, width: PaxTheme.Routes.railWidth, height: bounds.height).fill()
    }
    guard rowSeparators else { return }
    PaxTheme.Table.line.setFill()
    NSRect(
      x: 0, y: bounds.height - PaxTheme.Layout.cardBorder, width: bounds.width,
      height: PaxTheme.Layout.cardBorder
    ).fill()
  }
  override func drawSelection(in dirtyRect: NSRect) {
    guard highlightsSelection else { return }
    PaxTheme.Table.selected.setFill()
    NSBezierPath(
      roundedRect: bounds.insetBy(dx: selectionInset, dy: selectionInset),
      xRadius: selectionInset > 0 ? PaxTheme.Table.selectionRadius : 0,
      yRadius: selectionInset > 0 ? PaxTheme.Table.selectionRadius : 0
    ).fill()
    if selectionRail {
      PaxTheme.Table.rail.setFill()
      NSRect(x: 0, y: 0, width: PaxTheme.Routes.railWidth, height: bounds.height).fill()
    }
    guard rowSeparators else { return }
    PaxTheme.Table.line.setFill()
    NSRect(
      x: 0, y: bounds.height - PaxTheme.Layout.cardBorder, width: bounds.width,
      height: PaxTheme.Layout.cardBorder
    ).fill()
  }
}
private final class PaxReusableTableCell: NSTableCellView {
  var contentConstraints: [NSLayoutConstraint] = []
}

private final class PaxTableContentHost: NSView {
  var reuseKey: AnyHashable?
  init(_ content: NSView, reuseKey: AnyHashable? = nil) {
    self.reuseKey = reuseKey
    super.init(frame: .zero)
    replace(content)
    self.reuseKey = reuseKey
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override var intrinsicContentSize: NSSize {
    subviews.first?.intrinsicContentSize
      ?? NSSize(width: NSView.noIntrinsicMetric, height: NSView.noIntrinsicMetric)
  }
  func replace(_ content: NSView) {
    reuseKey = nil
    for child in subviews { child.removeFromSuperview() }
    addSubview(content)
    content.translatesAutoresizingMaskIntoConstraints = false
    NSLayoutConstraint.activate([
      content.leadingAnchor.constraint(equalTo: leadingAnchor),
      content.trailingAnchor.constraint(equalTo: trailingAnchor),
      content.topAnchor.constraint(equalTo: topAnchor),
      content.bottomAnchor.constraint(equalTo: bottomAnchor),
    ])
    invalidateIntrinsicContentSize()
  }
}
