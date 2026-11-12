import AppKit
import PaxCore
import PaxUI

extension NetworkDiagnosticView {
  func showIPBlock() {
    let exit = report?.exit
    let readings = [
      ("线路出口 IP", exit?.ip ?? "未取得"),
      (
        "地区",
        [exit?.country, exit?.city].compactMap { $0 }.filter { !$0.isEmpty }.joined(
          separator: " · ")
      ),
      (
        "ASN / 运营商",
        [exit?.asn ?? "", P.organization(intelligenceReport)].filter { !$0.isEmpty }.joined(
          separator: " · ")
      ),
    ]
    var rows: [NSView] = []
    for (title, value) in readings {
      if !rows.isEmpty { rows.append(divider()) }
      let detail =
        [
          "线路：" + (report?.targetRouteName ?? "未取得"),
          "检测时间：" + MeasurementTimestamp.text(report?.at),
          "国家：" + P.value(exit?.country ?? ""), "城市：" + P.value(exit?.city ?? ""),
          "ASN：" + P.value(exit?.asn ?? ""), "运营商：" + P.organization(intelligenceReport),
          "IP 类型：" + P.typeName(intelligenceReport?.ipType ?? ""),
          "资料时间：" + MeasurementTimestamp.text(intelligenceReport?.at),
        ] + (intelligenceReport?.score.map { ["ipx.al 供应方评分：\($0) / 100"] } ?? [])
      let details = detail.joined(separator: "\n")
      rows.append(
        diagnosticRow(
          title: title, subtitle: value.isEmpty ? "未取得" : value, symbol: "circle.grid.2x2",
          detail: details))
    }
    let summary = vertical(rows, spacing: 0)
    summary.setAccessibilityIdentifier("diagnostic-ip-attributes")
    let cards = [
      card(sourcesContent(), flexibleHeight: true), card(servicesContent(), flexibleHeight: true),
      card(
        vertical([
          heading("IP 属性", cardTitle: true),
          text(
            (store.stale ? "上次线路检测 · " : "线路检测 · ") + MeasurementTimestamp.text(report?.at),
            secondary: true), summary,
        ]), flexibleHeight: true),
      card(pathsContent(), flexibleHeight: true),
    ]
    for index in stride(from: 0, to: cards.count, by: 2) {
      let left = cards[index]
      let right = cards[index + 1]
      let row = twoColumns(left, right, ratio: PaxTheme.NetworkDiagnostics.equalColumnRatio)
      left.heightAnchor.constraint(equalTo: right.heightAnchor).isActive = true
      append(row)
    }
    append(card(riskContent()))
  }

  private func riskContent() -> NSView {
    let leftCategories: [(String, [String])] = [
      ("匿名", ["代理", "VPN", "Tor", "iCloud 中继", "匿名出口"]),
      ("黑名单", ["邮件 DNSBL", "Shodan 标签"]),
      ("暴露面", ["开放端口", "已知漏洞", "自签证书"]),
    ]
    let rightCategories: [(String, [String])] = [
      ("滥用", ["滥用标记", "代理风险 ≥67", "爬虫", "威胁情报名单", "恶意扫描"]),
      ("网络", ["机房 IP", "移动网络", "伪家宽", "人类流量 <50%", "广播 IP"]),
    ]
    let risks = groups["risk"] ?? []
    let columns = twoColumns(
      riskColumn(leftCategories, risks: risks), riskColumn(rightCategories, risks: risks),
      ratio: PaxTheme.NetworkDiagnostics.equalColumnRatio, spacing: PaxTheme.Spacing.page)
    return vertical([
      heading("风险分类", cardTitle: true),
      text(
        P.acquisitionSummary(intelligenceReport) + " · "
          + MeasurementTimestamp.text(intelligenceReport?.at)
          + (store.stale || intelligenceReport?.at != report?.at ? " · 上次资料" : ""), secondary: true),
      assessmentText(P.riskOverview(risks)), columns,
    ])
  }

  private func riskColumn(_ categories: [(String, [String])], risks: [NetworkDiagnosticFact])
    -> NSView
  {
    var views: [NSView] = []
    for (category, labels) in categories {
      let values = risks.filter { $0.value == category }
      var rows: [NSView] = [heading(category)]
      for label in labels {
        if rows.count > 1 { rows.append(divider()) }
        let fact = values.first { $0.label == label }
        let judgment = fact.map(P.riskAssessment)
        let value = judgment?.text ?? (report == nil ? "尚未检测" : "来源资料不足，不能判断")
        let detail = P.factDetails(fact, report: intelligenceReport)
        rows.append(
          diagnosticRow(
            title: label, subtitle: value, symbol: "circle.grid.2x2", detail: detail,
            tone: judgment?.tone ?? .pending))
      }
      let list = vertical(rows, spacing: 0)
      list.setAccessibilityIdentifier("diagnostic-risk-" + category)
      views.append(list)
    }
    return vertical(views)
  }
}
