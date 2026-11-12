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
    for source in filteredSources() {
      if !rows.isEmpty { rows.append(divider()) }
      let judgment = P.sourceAssessment(source, all: intelligenceReport?.sources ?? [])
      let detail = [
        "国家：" + P.countryName(source.country), "城市：" + P.value(source.city),
        "ASN：" + P.value(source.asn), "组织：" + P.value(source.org),
        "来源更新时间：" + P.value(source.providerDate),
        P.providerDetails(intelligenceReport, id: source.providerID),
      ].joined(separator: "\n")
      rows.append(
        diagnosticRow(
          title: source.name + (source.providerID == "ipx.al" ? " · 补充" : ""),
          subtitle: judgment.text, symbol: "globe", detail: detail,
          tone: judgment.tone))
    }
    for provider in intelligenceReport?.providers ?? []
    where ["ipquery", "ipwho.is", "ip.sb", "geojs"].contains(provider.id)
      && !(intelligenceReport?.sources.contains { $0.providerID == provider.id } ?? false)
    {
      if !rows.isEmpty { rows.append(divider()) }
      rows.append(
        diagnosticRow(
          title: provider.id, subtitle: P.providerState(provider),
          symbol: "globe", detail: P.providerDetails(intelligenceReport, id: provider.id),
          tone: .pending))
    }
    let list = vertical(rows, spacing: 0)
    list.setAccessibilityIdentifier("diagnostic-sources-table")
    let views: [NSView] = [
      horizontal([heading("多源对照", cardTitle: true), NSView(), filter]),
      text(
        "指定线路：" + (report?.targetRouteName ?? "尚未检测") + " · 检测于 "
          + MeasurementTimestamp.text(report?.at) + (store.stale ? " · 上次检测" : ""), secondary: true),
      assessmentText(P.sourceOverview(intelligenceReport?.sources ?? [])), list,
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

  private func filteredSources() -> [NetworkDiagnosticReport.Source] {
    (intelligenceReport?.sources ?? []).filter { source in
      guard store.onlyDifferences, let report = intelligenceReport else { return true }
      return [source.country, source.city, source.asn].contains(where: \.isEmpty)
        || P.differences(report, source: source)
    }
  }

}
