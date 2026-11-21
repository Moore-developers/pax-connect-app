import AppKit
import PaxCore
import PaxUI

extension LogsWorkbenchViewController {
  func configureLanguage(header: PaxPageHeader) {
    interfaceCopy.bind(header, key: "navigation.logs") { $0.apply(title: $1) }
    interfaceCopy.onChange = { [weak self] in self?.applyLanguage() }
    interfaceCopy.button(previousPage, key: "logs.previous")
    interfaceCopy.button(nextPage, key: "logs.next")
    interfaceCopy.button(moreObjectsButton, key: "common.more")
  }
  func applyLanguage() {
    mode.apply(labels: [interfaceCopy.text("common.proxy"), interfaceCopy.text("common.direct")])
    tabs.apply(
      labels: ["traffic_details.63c73c4730", "logs.domains", "logs.system"].map {
        interfaceCopy.text($0)
      })
    level.apply(
      labels: ["logs.all_levels", "logs.error", "logs.warning", "logs.info"].map {
        interfaceCopy.text($0)
      })
    level.setAccessibilityLabel(interfaceCopy.text("logs.level"))
    let selected = range.indexOfSelectedItem
    for (index, key) in ["logs.thirty_minutes", "logs.six_hours", "logs.day"].enumerated() {
      range.item(at: index)?.title = interfaceCopy.text(key)
    }
    if selected >= 0 { range.selectItem(at: selected) }
    directory.setColumnTitle(interfaceCopy.text("logs.records"), at: 1)
    let layout = PaxTheme.Logs.self
    let english = interfaceCopy.context.language == .english
    let accessWidths =
      english
      ? layout.englishAccessColumns
      : [
        layout.timeColumn, layout.applicationColumn, layout.stateColumn, layout.connectionColumn,
        layout.durationColumn, layout.transferColumn,
      ]
    let systemWidths =
      english
      ? layout.englishSystemColumns
      : [layout.timeColumn, layout.applicationColumn, layout.stateColumn, layout.connectionColumn]
    for (index, key) in [
      "logs.time", store.dimension == .domain ? "logs.domain_target" : "logs.app_target",
      "logs.status", "logs.connection", "logs.latency", "logs.transfer",
    ].enumerated() {
      events.setColumnTitle(interfaceCopy.text(key), at: index)
      events.setColumnWidth(accessWidths[index], at: index)
    }
    for (index, key) in ["logs.time", "logs.event", "logs.status", "logs.context"].enumerated() {
      systemEvents.setColumnTitle(interfaceCopy.text(key), at: index)
      systemEvents.setColumnWidth(systemWidths[index], at: index)
    }
    render()
    for table in [events, systemEvents, directory] { table.refreshVisibleCopy() }
  }
}
