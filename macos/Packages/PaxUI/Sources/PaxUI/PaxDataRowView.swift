import AppKit

final class PaxDataRowView: NSTableRowView {
  var readout: PaxHoverReadout?
  var readoutContent: (() -> NSView?)?
  private var hoverTracking: NSTrackingArea?
  override func updateTrackingAreas() {
    super.updateTrackingAreas()
    if let hoverTracking { removeTrackingArea(hoverTracking) }
    guard readout != nil else { return }
    let area = NSTrackingArea(
      rect: .zero,
      options: [.mouseEnteredAndExited, .mouseMoved, .activeInKeyWindow, .inVisibleRect],
      owner: self)
    hoverTracking = area
    addTrackingArea(area)
  }
  override func mouseEntered(with event: NSEvent) { showReadout() }
  override func mouseMoved(with event: NSEvent) { showReadout() }
  override func mouseExited(with event: NSEvent) { readout?.leave(from: self) }
  override func viewDidMoveToWindow() {
    super.viewDidMoveToWindow()
    if window == nil { readout?.close(from: self) }
  }
  func showReadout(focus: Bool = false) {
    guard let readout, !readout.isShowing(from: self), let content = readoutContent?() else {
      return
    }
    readout.show(content, from: self)
    if focus { readout.focus() }
  }
  override func accessibilityPerformShowMenu() -> Bool {
    guard readoutContent != nil else { return super.accessibilityPerformShowMenu() }
    showReadout()
    readout?.focus()
    return true
  }
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
