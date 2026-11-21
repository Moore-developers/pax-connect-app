import AppKit
import PaxCore
import PaxUI

extension LogsWorkbenchViewController {
  func buildColumns(in body: NSView) {
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
    search.placeholderString = interfaceCopy.text(
      store.dimension == .system ? "logs.search_system" : "logs.search")
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
    level.setAccessibilityLabel(self.interfaceCopy.text("logs.level"))
    level.setAccessibilityIdentifier("logs-level")
    range.addItems(withTitles: [
      self.interfaceCopy.text("logs.thirty_minutes"), self.interfaceCopy.text("logs.six_hours"),
      self.interfaceCopy.text("settings.logs.day"),
    ])
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
}
