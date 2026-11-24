import AppKit
import PaxCore
import PaxUI

extension NetworkDiagnosticView {
  enum DiagnosticTopic: Int, CaseIterable {
    case browser, attributes, sources, risk
    var titleKey: String {
      switch self {
      case .browser: "native_diagnostic.ee42d43fd2"
      case .attributes: "native_diagnostic.dd5fe5361f"
      case .sources: "native_diagnostic.9608f6ef4a"
      case .risk: "native_diagnostic.2ef6b672c5"
      }
    }
  }

  func topicContent(_ topic: DiagnosticTopic) -> NSView {
    switch topic {
    case .attributes: return attributeContent()
    case .sources: return sourcesContent()
    case .risk: return riskContent()
    case .browser: return pathsContent()
    }
  }

  func routeSummaryMetadata(_ evidence: NetworkDiagnosticReport?, sourceData: Bool = false)
    -> String
  {
    guard let evidence else { return "" }
    let historical = store.stale || evidence.at != report?.at
    let previous = context.text(
      sourceData ? "diagnostic.overview.previous_data" : "native_diagnostic.f48ea0733c")
    return [
      evidence.targetRouteName, historical ? previous : "", MeasurementTimestamp.text(evidence.at),
    ]
    .filter { !$0.isEmpty }.joined(separator: " · ")
  }

  private func attributeContent() -> NSView {
    let exit = report?.exit
    let intel = intelligenceReport
    let asn = exit?.asn ?? ""
    let organization = P.organization(intel, context: context)
    let missingOrg = P.organization(nil, context: context)
    let readings = [
      (
        context.text("native_diagnostic.b50f6bb571"),
        exit?.ip ?? ""
      ),
      (
        context.text("native_diagnostic.0b5daa9276"),
        [exit?.country, exit?.city].compactMap { $0 }.filter { !$0.isEmpty }.joined(
          separator: " · ")
      ),
      (
        context.text("native_diagnostic.1df0f2a76c"),
        [asn, organization == missingOrg ? "" : organization].filter {
          !$0.isEmpty
        }.joined(
          separator: " · ")
      ),
    ]
    let missing = readings.filter { $0.1.isEmpty }.count
    var rows: [NSView] = []
    for (title, value) in readings {
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
          detail: details, tone: value.isEmpty ? .pending : .information,
          verdict: context.text(
            value.isEmpty ? "browser.check_unavailable" : "browser.check_observed"),
          basis: value.isEmpty ? "—" : value))
    }
    let summary = topicSummary(
      report == nil
        ? context.text("native_diagnostic.4d2966c438")
        : missing > 0 ? context.text("diagnostic.overview.attributes_missing", count: missing) : "",
      tone: report == nil ? .pending : missing > 0 ? .difference : .information,
      metadata: routeSummaryMetadata(report))
    let table = diagnosticTable(rows)
    table.setAccessibilityIdentifier("diagnostic-ip-attributes")
    return vertical([summary, table])
  }

  private func riskContent() -> NSView {
    let categories: [(String, [String])] = [
      (
        context.text("native_diagnostic.34a917cd44"),
        ["proxy", "vpn", "tor"]
      ),
      (
        context.text("native_diagnostic.6cafeab656"),
        ["open_ports", "known_vulnerabilities", "shodan_tags"]
      ),
      (context.text("native_diagnostic.97b31b5d63"), ["datacenter", "mobile"]),
    ]
    let risks = groups["risk"] ?? []
    let assessment = P.riskOverview(risks, context: context)
    let summary = topicSummary(
      assessment.text, tone: assessment.tone,
      metadata: routeSummaryMetadata(intelligenceReport, sourceData: true),
      note: context.text("diagnostic.risk.not_scan"))
    return vertical([summary, riskTable(categories, risks: risks)])
  }

  private func riskTable(_ categories: [(String, [String])], risks: [NetworkDiagnosticFact])
    -> NSView
  {
    var views: [NSView] = []
    for (category, labels) in categories {
      views.append(heading(category))
      for label in labels {
        let fact = risks.first { P.factKind($0) == label }
        let judgment = fact.map { P.riskAssessment($0, context: context) }
        let value =
          judgment?.text
          ?? (report == nil
            ? context.text("native_diagnostic.4d2966c438")
            : context.text("native_diagnostic.2ccd13e2cd"))
        let detail = P.factDetails(fact, report: intelligenceReport, context: context)
        views.append(
          diagnosticRow(
            title: fact.map { P.factLabel($0, context: context) }
              ?? context.text("diagnostic.fact." + label), subtitle: value,
            symbol: "circle.grid.2x2", detail: detail,
            tone: judgment?.tone ?? .pending, basis: fact.map { P.factSource($0, context: context) }
          ))
      }
    }
    let table = diagnosticTable(views)
    table.setAccessibilityIdentifier("diagnostic-risk")
    return table
  }
}
