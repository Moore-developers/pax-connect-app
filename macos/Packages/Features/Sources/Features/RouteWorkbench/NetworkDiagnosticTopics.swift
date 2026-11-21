import AppKit
import PaxCore
import PaxUI

extension NetworkDiagnosticView {
  func showIPBlock() {
    let exit = report?.exit
    let readings = [
      (
        context.text("native_diagnostic.b50f6bb571"),
        exit?.ip ?? context.text("native_diagnostic.e7954a2f3a")
      ),
      (
        context.text("native_diagnostic.0b5daa9276"),
        [exit?.country, exit?.city].compactMap { $0 }.filter { !$0.isEmpty }.joined(
          separator: " · ")
      ),
      (
        context.text("native_diagnostic.1df0f2a76c"),
        [exit?.asn ?? "", P.organization(intelligenceReport, context: context)].filter {
          !$0.isEmpty
        }.joined(
          separator: " · ")
      ),
    ]
    var rows: [NSView] = []
    for (title, value) in readings {
      if !rows.isEmpty { rows.append(divider()) }
      let detail =
        [
          context.text("native_diagnostic.5dda3f9bb8")
            + (report?.targetRouteName ?? context.text("native_diagnostic.e7954a2f3a")),
          context.text("native_diagnostic.2f3e61e480") + MeasurementTimestamp.text(report?.at),
          context.text("native_diagnostic.beedab4aae")
            + P.value(exit?.country ?? "", context: context),
          context.text("native_diagnostic.485cd74fe4")
            + P.value(exit?.city ?? "", context: context),
          "ASN：" + P.value(exit?.asn ?? "", context: context),
          context.text("native_diagnostic.a2c7b936df")
            + P.organization(intelligenceReport, context: context),
          context.text("native_diagnostic.1680846442")
            + P.typeName(intelligenceReport?.ipType ?? "", context: context),
          context.text("native_diagnostic.0bddcd655b")
            + MeasurementTimestamp.text(intelligenceReport?.at),
        ]
        + (intelligenceReport?.score.map {
          [context.text("native_diagnostic.f6f1d6b0d5", ["arg0": String(describing: $0)])]
        } ?? [])
      let details = detail.joined(separator: "\n")
      rows.append(
        diagnosticRow(
          title: title,
          subtitle: value.isEmpty ? context.text("native_diagnostic.e7954a2f3a") : value,
          symbol: "circle.grid.2x2",
          detail: details))
    }
    let summary = vertical(rows, spacing: 0)
    summary.setAccessibilityIdentifier("diagnostic-ip-attributes")
    let cards = [
      card(sourcesContent(), flexibleHeight: true), card(servicesContent(), flexibleHeight: true),
      card(
        vertical([
          heading(context.text("native_diagnostic.dd5fe5361f"), cardTitle: true),
          text(
            (store.stale
              ? context.text("native_diagnostic.b8d2a37276")
              : context.text("native_diagnostic.7394b49888"))
              + MeasurementTimestamp.text(report?.at),
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
      (
        context.text("native_diagnostic.34a917cd44"),
        ["proxy", "VPN", "Tor", "icloud_relay", "anonymous_exit"]
      ),
      (context.text("native_diagnostic.174ec5a4ab"), ["mail_dnsbl", "shodan_tags"]),
      (
        context.text("native_diagnostic.6cafeab656"),
        ["open_ports", "known_vulnerabilities", "self_signed_certificate"]
      ),
    ]
    let rightCategories: [(String, [String])] = [
      (
        context.text("native_diagnostic.553b6296f9"),
        ["abuse", "proxy_risk", "crawler", "threat_list", "malicious_scanning"]
      ),
      (
        context.text("native_diagnostic.97b31b5d63"),
        ["datacenter", "mobile", "residential_proxy", "low_human_traffic", "broadcast_ip"]
      ),
    ]
    let risks = groups["risk"] ?? []
    let columns = twoColumns(
      riskColumn(leftCategories, risks: risks), riskColumn(rightCategories, risks: risks),
      ratio: PaxTheme.NetworkDiagnostics.equalColumnRatio, spacing: PaxTheme.Spacing.page)
    return vertical([
      heading(context.text("native_diagnostic.2ef6b672c5"), cardTitle: true),
      text(
        P.acquisitionSummary(intelligenceReport, context: context) + " · "
          + MeasurementTimestamp.text(intelligenceReport?.at)
          + (store.stale || intelligenceReport?.at != report?.at
            ? context.text("native_diagnostic.bdcc02259f") : ""),
        secondary: true),
      assessmentText(P.riskOverview(risks, context: context)), columns,
    ])
  }

  private func riskColumn(_ categories: [(String, [String])], risks: [NetworkDiagnosticFact])
    -> NSView
  {
    var views: [NSView] = []
    for (category, labels) in categories {
      let values = risks
      var rows: [NSView] = [heading(category)]
      for label in labels {
        if rows.count > 1 { rows.append(divider()) }
        let fact = values.first { P.factKind($0) == label }
        let judgment = fact.map { P.riskAssessment($0, context: context) }
        let value =
          judgment?.text
          ?? (report == nil
            ? context.text("native_diagnostic.4d2966c438")
            : context.text("native_diagnostic.2ccd13e2cd"))
        let detail = P.factDetails(fact, report: intelligenceReport, context: context)
        rows.append(
          diagnosticRow(
            title: fact.map { P.factLabel($0, context: context) }
              ?? context.text("diagnostic.fact." + label), subtitle: value,
            symbol: "circle.grid.2x2", detail: detail,
            tone: judgment?.tone ?? .pending))
      }
      let list = vertical(rows, spacing: 0)
      list.setAccessibilityIdentifier("diagnostic-risk-" + category)
      views.append(list)
    }
    return vertical(views)
  }
}
