import AppKit

extension PaxDataTable.Metrics {
  /// Shared workbench table surface used by traffic and AI usage lists.
  public static func workbench(
    rowHeight: CGFloat = PaxTheme.Dashboard.rowHeight, rounded: Bool = false
  ) -> Self {
    .init(rowHeight: rowHeight, headerHeight: PaxTheme.Dashboard.tableHeader, rounded: rounded)
  }
}

// Public scrolling entry points share the table's single viewport.
extension PaxDataTable {
  /// 将指定行滚入可见区域，返回表格容器坐标下的行矩形供浮层定位。
  public func revealRow(_ id: String) -> NSRect? {
    guard let index = rows.firstIndex(where: { $0.id == id }) else { return nil }
    layoutSubtreeIfNeeded()
    if scrollMode == .fixedHeader {
      table.scrollRowToVisible(index)
    } else {
      scrollToVisible(convert(table.rect(ofRow: index), from: table))
    }
    return convert(table.rect(ofRow: index), from: table)
  }
  public func revealFirstRow() { if !rows.isEmpty { table.scrollRowToVisible(0) } }
  /// Reset the viewport including the header, even when the table is empty.
  public func scrollToTop() {
    guard scrollMode == .fixedHeader else {
      scrollToVisible(NSRect(x: 0, y: 0, width: bounds.width, height: metrics.headerHeight))
      return
    }
    layoutSubtreeIfNeeded()
    let top = NSPoint(x: 0, y: -scroll.contentView.contentInsets.top)
    scroll.contentView.scroll(to: top)
    scroll.reflectScrolledClipView(scroll.contentView)
  }
}
