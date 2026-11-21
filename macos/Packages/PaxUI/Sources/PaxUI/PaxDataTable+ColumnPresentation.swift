import AppKit

// Column copy changes preserve sorting, rows and the current viewport.
extension PaxDataTable {
  public func setColumnWidth(_ width: CGFloat, at index: Int) {
    guard columns.indices.contains(index), columns[index].width != width else { return }
    columns[index].width = width
    needsLayout = true
  }

  public func setColumnTitle(_ title: String, at index: Int, symbol: String? = nil) {
    guard table.tableColumns.indices.contains(index) else { return }
    columns[index].title = title
    let shown = title + (sortedColumn == index ? (ascending ? " ↑" : " ↓") : "")
    let cell = table.tableColumns[index].headerCell
    let symbolChanged = symbol.map { (cell as? PaxDataHeaderCell)?.symbol != $0 } ?? false
    guard cell.stringValue != shown || symbolChanged else { return }
    cell.stringValue = shown
    table.tableColumns[index].headerToolTip = columns[index].toolTip ?? title
    if let symbol, let header = cell as? PaxDataHeaderCell { header.symbol = symbol }
    table.headerView?.needsDisplay = true
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
  public func setTableAccessibilityLabel(_ title: String) { table.setAccessibilityLabel(title) }
  public func setEmptyText(_ text: String) { emptyLabel.stringValue = text }
  public func refreshVisibleCopy() {
    let visible = table.rows(in: table.visibleRect)
    guard visible.location != NSNotFound, visible.length > 0 else { return }
    table.reloadData(
      forRowIndexes: IndexSet(integersIn: visible.location..<(visible.location + visible.length)),
      columnIndexes: IndexSet(integersIn: 0..<table.numberOfColumns))
  }
}
