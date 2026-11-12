import AppKit

/// 数据表表头：背景、分隔线与带图标的列标题。
final class PaxDataHeader: NSTableHeaderView {
  var topBorder = false
  var fill = PaxTheme.Table.headerFill
  var height = PaxTheme.Table.header
  override init(frame frameRect: NSRect) {
    super.init(frame: frameRect)
    wantsLayer = true
    layerContentsRedrawPolicy = .onSetNeedsDisplay
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func setFrameSize(_ newSize: NSSize) {
    let changed = frame.size != newSize
    super.setFrameSize(newSize)
    if changed { needsDisplay = true }
  }
  override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }
  override var intrinsicContentSize: NSSize {
    NSSize(width: NSView.noIntrinsicMetric, height: height)
  }
  override func draw(_ dirtyRect: NSRect) {
    fill.setFill()
    bounds.fill()
    guard let tableView else { return }
    for (index, column) in tableView.tableColumns.enumerated() {
      let rect = headerRect(ofColumn: index)
      if needsToDraw(rect) { column.headerCell.draw(withFrame: rect, in: self) }
    }
    PaxTheme.Routes.edge.setFill()
    if topBorder {
      NSRect(x: 0, y: 0, width: bounds.width, height: PaxTheme.Layout.cardBorder).fill()
    }
    NSRect(
      x: 0, y: bounds.height - PaxTheme.Layout.cardBorder, width: bounds.width,
      height: PaxTheme.Layout.cardBorder
    ).fill()
  }
}
final class PaxDataHeaderCell: NSTableHeaderCell {
  private let config: PaxDataTable.Column
  var symbol: String?
  init(_ config: PaxDataTable.Column) {
    self.config = config
    symbol = config.symbol
    super.init(textCell: config.title)
    alignment = config.alignment
  }
  @available(*, unavailable) required init(coder: NSCoder) { fatalError("代码构建") }
  override func draw(withFrame rect: NSRect, in view: NSView) {
    let attrs: [NSAttributedString.Key: Any] = [
      .font: PaxTheme.Typography.caption(), .foregroundColor: PaxTheme.Table.muted,
    ]
    // stringValue retains the direction for accessibility; visual feedback uses
    // an icon instead of an appended arrow character.
    let title = image == nil ? stringValue : String(stringValue.dropLast(2))
    let size = (title as NSString).size(withAttributes: attrs)
    let glyph = symbol == nil ? 0 : PaxTheme.Control.navigationIcon
    let gap = glyph > 0 ? PaxTheme.Spacing.xs : 0
    let sortGlyph = image == nil ? 0 : PaxTheme.Control.navigationIcon
    let sortGap = sortGlyph > 0 ? PaxTheme.Spacing.xs : 0
    let width = size.width + glyph + gap + sortGlyph + sortGap
    let x =
      config.alignment == .center
      ? rect.midX - width / 2
      : config.alignment == .right ? rect.maxX - config.inset - width : rect.minX + config.inset
    if let symbol, let image = PaxRouteIcon.image(symbol, size: glyph) {
      PaxRouteDrawing.icon(
        image, in: NSRect(x: x, y: rect.midY - glyph / 2, width: glyph, height: glyph),
        color: PaxTheme.Table.muted)
    }
    (title as NSString).draw(
      at: NSPoint(x: x + glyph + gap, y: rect.midY - size.height / 2), withAttributes: attrs)
    if let image {
      PaxRouteDrawing.icon(
        image,
        in: NSRect(
          x: x + glyph + gap + size.width + sortGap, y: rect.midY - sortGlyph / 2, width: sortGlyph,
          height: sortGlyph),
        color: PaxTheme.Table.muted)
    }
  }
}
