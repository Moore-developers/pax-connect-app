import AppKit
import PaxUI

@MainActor
func logObjectCell(
  id: String, column: Int, store: LogsWorkbenchStore, icons: WorkbenchObjectIcons
) -> NSView? {
  if id == "group" { return NSView() }
  guard let object = store.objects.first(where: { $0.id == id }) else { return nil }
  if column == 1 {
    let count = PaxText.label(
      String(object.count),
      font: PaxTheme.Typography.metric(size: PaxTheme.Logs.directoryCountFont), secondary: true)
    count.alignment = .right
    return count
  }
  let iconSize =
    store.dimension == .domain || store.dimension == .system
    ? PaxTheme.Logs.directoryCompactIconSize : PaxTheme.Logs.directoryIconSize
  let image =
    id == "all"
    ? PaxIcon.image("list.bullet", size: iconSize) ?? NSImage()
    : icons.image(
      kind: store.dimension.iconKind, id: object.id, name: object.title)
  let text = PaxText.label(
    object.title, font: PaxTheme.Typography.label(size: PaxTheme.Logs.directoryFont))
  text.lineBreakMode = .byTruncatingTail
  text.toolTip = object.title
  let color = id == store.selectedObject ? PaxTheme.Table.rail : PaxTheme.Table.text
  text.textColor = color
  if id == "all" || store.dimension == .system {
    let symbol = id == "all" ? "list.bullet" : object.symbol.isEmpty ? "gearshape" : object.symbol
    let image =
      store.dimension == .system
      ? PaxIcon.image(symbol, size: iconSize) ?? NSImage() : image
    return PaxObjectIdentityView(
      image: image, content: text, iconSize: iconSize, tint: color)
  }
  return icons.identityView(
    kind: store.dimension.iconKind, id: object.id, name: object.title,
    content: text, size: iconSize,
    tint: id == store.selectedObject ? PaxTheme.Table.rail : PaxTheme.Logs.directoryIcon)
}

/// Identity content excludes the changing count; selecting another object recolors both rows.
@MainActor
func logObjectCellKey(id: String, store: LogsWorkbenchStore) -> [String] {
  let object = store.objects.first { $0.id == id }
  return [
    id, object?.title ?? "", object?.symbol ?? "", String(store.dimension.rawValue),
    String(id == store.selectedObject),
  ]
}

/// Update displayed properties without detaching the live text field from the table.
@MainActor
func updateLogTextCell(_ label: NSTextField, from next: NSTextField) {
  if label.stringValue != next.stringValue { label.stringValue = next.stringValue }
  if label.toolTip != next.toolTip { label.toolTip = next.toolTip }
  if label.textColor != next.textColor { label.textColor = next.textColor }
}
