import AppKit
import PaxCore
import PaxUI

extension NetworkDiagnosticView {
  func sourcesContent() -> NSView {
    let filter = action(store.onlyDifferences ? "全部来源" : "仅看分歧 / 缺失") { [weak self] in
      self?.store.onlyDifferences.toggle()
    }
    filter.setAccessibilityIdentifier("network-source-filter")
    var rows: [NSView] = []
    for source in P.sourceRows(intelligenceReport, onlyDifferences: store.onlyDifferences) {
      if !rows.isEmpty { rows.append(divider()) }
      rows.append(
        diagnosticRow(
          title: source.title, subtitle: source.value, symbol: "globe", detail: source.detail,
          tone: source.tone))
    }
    let list = vertical(rows, spacing: 0)
    list.setAccessibilityIdentifier("diagnostic-sources-table")
    let views: [NSView] = [
      horizontal([heading("多源对照", cardTitle: true), NSView(), filter]),
      text(
        "指定线路：" + (report?.targetRouteName ?? "尚未检测") + " · 检测于 "
          + MeasurementTimestamp.text(report?.at) + (store.stale ? " · 上次检测" : ""), secondary: true),
      assessmentText(P.sourceListOverview(intelligenceReport)), list,
      text(P.acquisitionSummary(intelligenceReport), secondary: true),
      text(rows.isEmpty ? "当前没有可展示的来源" : "分歧表示资料口径不同，不自动判定哪一源正确。", secondary: true),
      text(
        "资料查询于 " + MeasurementTimestamp.text(intelligenceReport?.at)
          + (intelligenceReport?.at != report?.at
            ? " · 上次同 IP 资料；本次：" + P.problem(report?.providerError ?? "") : ""),
        secondary: true),
    ]
    return vertical(views)
  }

}
