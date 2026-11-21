import AppKit
import PaxCore
import PaxUI

/// Pages actual Geo members; it never expands an entire database on the UI thread.
@MainActor final class RoutingGeoRulesController: RoutingModalController {
  override var localizedTitleKey: String? { "rules_ui.geo_title" }
  override var localizedSubtitleKey: String? { "rules_ui.geo_note" }
  private let telemetry: TelemetryStore
  private let search = PaxRouteSearchField()
  private let table = PaxDataTable(
    columns: [
      .init("规则包", width: 130, growth: 0.25, sortable: false),
      .init("匹配目标", width: 230, growth: 0.55, sortable: false),
      .init("范围", width: 90, growth: 0.2, sortable: false),
    ],
    metrics: .init(rowHeight: PaxTheme.Table.memberRow, headerHeight: PaxTheme.Table.compactHeader))
  private let status = PaxText.label("", secondary: true, wrapping: true)
  private let previous = PaxRouteButton("上一页")
  private let next = PaxRouteButton("下一页")
  private var offset: UInt32 = 0
  private var page = GeoRulesPage()
  private var task: Task<Void, Never>?
  init(telemetry: TelemetryStore) {
    self.telemetry = telemetry
    super.init(title: "Geo 基础包规则", subtitle: "Google 代理与中国域名 / IP 直连的实际条目", symbol: "globe")
  }
  override func loadView() {
    super.loadView()
    for (index, key) in ["log_detail.rule_package", "rules_ui.match_target", "rules_ui.range"]
      .enumerated()
    { table.setColumnTitle(interfaceCopy.text(key), at: index) }
    previous.title = interfaceCopy.text("logs.previous")
    next.title = interfaceCopy.text("logs.next")
    search.placeholderString = self.interfaceCopy.text("interface_detail.5519ce2c50")
    search.sendsSearchStringImmediately = true
    search.target = self
    search.action = #selector(filterChanged)
    search.setAccessibilityIdentifier("geo-rule-search")
    pane.add(search)
    table.setTableIdentifier("geo-rules-members")
    table.setTableAccessibilityLabel(interfaceCopy.text("rules_ui.geo_title"))
    pane.add(table)
    pane.add(status)
    previous.target = self
    previous.action = #selector(previousPage)
    next.target = self
    next.action = #selector(nextPage)
    pane.add(NSStackView(views: [previous, NSView(), next]))
    pane.setActions([button(self.interfaceCopy.text("log_detail.completed"), #selector(close))])
    reload()
  }
  @objc private func filterChanged() {
    offset = 0
    reload(debounce: true)
  }
  @objc private func previousPage() {
    offset = offset > 100 ? offset - 100 : 0
    reload()
  }
  @objc private func nextPage() {
    guard page.nextOffset > 0 else { return }
    offset = page.nextOffset
    reload()
  }
  private func reload(debounce: Bool = false) {
    task?.cancel()
    let query = search.stringValue
    let current = offset
    task = Task { [weak self] in
      do {
        if debounce { try await Task.sleep(for: .milliseconds(200)) }
        guard let self else { return }
        let result = try await telemetry.geoRules(search: query, offset: current)
        guard !Task.isCancelled else { return }
        page = result
        table.apply(
          rows: result.rules.map { r in
            .init(
              id: r.id,
              values: [
                r.destination, r.target,
                r.method == "domain_suffix"
                  ? self.interfaceCopy.text("interface_detail.d970428baf")
                  : r.method == "domain"
                    ? self.interfaceCopy.text("interface_detail.3d1aa10e58")
                    : r.method == "domain_regex"
                      ? self.interfaceCopy.text("interface_detail.26940c5e57")
                      : self.interfaceCopy.text("interface_detail.7e49735747"),
              ])
          }, emptyMessage: self.interfaceCopy.text("interface_detail.c224df852c"))
        let version = result.version.replacingOccurrences(
          of: " / Google 内置 ", with: self.interfaceCopy.text("rules_ui.geo_google_version"))
        status.stringValue =
          self.interfaceCopy.text(
            "interface_detail.8e03a51fc3",
            ["arg0": String(describing: result.total), "arg1": version])
          + (result.updatedAt.map { TrafficFormatting.dateMinute($0) }
            ?? self.interfaceCopy.text("interface_detail.bbc83f8430"))
        previous.isEnabled = offset > 0
        next.isEnabled = result.nextOffset > 0
      } catch {
        if !Task.isCancelled, let self {
          self.status.stringValue = ProductErrorPresentation.display(
            error, context: self.interfaceCopy.context)
        }
      }
    }
  }
  override func close() {
    task?.cancel()
    super.close()
  }
  override func viewWillDisappear() {
    task?.cancel()
    super.viewWillDisappear()
  }
}
