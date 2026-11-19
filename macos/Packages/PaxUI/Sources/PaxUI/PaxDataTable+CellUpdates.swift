import AppKit

extension PaxDataTable {
  /// Update the given cells from their provider's current values. Column indexes are
  /// from the table's column configuration, even if the user moves a column.
  public func updateCells(_ changes: [String: IndexSet]) {
    guard !changes.isEmpty else { return }
    var reloadByColumn: [Int: IndexSet] = [:]
    for (row, item) in rows.enumerated() {
      guard let columns = changes[item.id] else { continue }
      for column in columns {
        let nativeColumn = table.column(withIdentifier: .init(String(column)))
        guard nativeColumn >= 0 else { continue }
        if let cellUpdater,
          updateMaterializedCell(
            row: row, nativeColumn: nativeColumn, column: column, using: cellUpdater)
        {
          continue
        }
        reloadByColumn[nativeColumn, default: []].insert(row)
      }
    }
    // Group by column so AppKit only reloads the requested row-column pairs.
    for (column, rows) in reloadByColumn {
      table.reloadData(forRowIndexes: rows, columnIndexes: IndexSet(integer: column))
    }
  }

  func updateMaterializedCell(
    row: Int, nativeColumn: Int, column: Int, using update: (NSView, String, Int) -> Bool
  ) -> Bool {
    guard let cell = table.view(atColumn: nativeColumn, row: row, makeIfNecessary: false),
      let content = cell.subviews.first
    else { return false }
    return update(content, rows[row].id, column)
  }

  /// Opt-in stable content host used by history/analysis cellUpdater consumers.
  public static func reusableContent(_ content: NSView, reuseKey: AnyHashable? = nil) -> NSView {
    PaxTableContentHost(content, reuseKey: reuseKey)
  }
  /// Preserve unchanged content and optionally update the current child for a new key.
  public static func updateContent(
    _ existing: NSView, reusing key: AnyHashable, updating update: ((NSView) -> Bool)? = nil,
    makeContent: () -> NSView
  ) -> Bool {
    guard let host = existing as? PaxTableContentHost else { return false }
    guard host.reuseKey != key else { return true }
    if let child = host.subviews.first, update?(child) == true {
      host.reuseKey = key
      return true
    }
    host.replace(makeContent())
    host.reuseKey = key
    return true
  }
  public static func updateContent(_ existing: NSView, content: NSView) -> Bool {
    guard let host = existing as? PaxTableContentHost else { return false }
    host.replace(content)
    return true
  }
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
