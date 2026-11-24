import Foundation
import PaxClient

/// Conclusions describe the observation that supports them, never overall safety.
extension NetworkDiagnosticPresentation {
  public struct Assessment: Sendable {
    public let text: String
    public let tone: EnvironmentDiagnosticPresentation.Tone
  }

  public static func serviceAssessment(
    _ probe: NetworkDiagnosticReport.Probe?, api: Bool, context: PresentationContext = .chinese
  )
    -> Assessment
  {
    guard let probe else {
      return Assessment(text: context.text("diagnostic_evidence.c065199a13"), tone: .pending)
    }
    let result: String
    let tone: EnvironmentDiagnosticPresentation.Tone
    switch probe.httpStatus {
    case 200..<300:
      result =
        api
        ? context.text("diagnostic_evidence.410405faea")
        : context.text("diagnostic_evidence.e7a04dd152")
      tone = .information
    case 300..<400:
      result = context.text("diagnostic_evidence.047618d166")
      tone = .information
    case 401:
      result =
        (api ? "API" : context.text("diagnostic_evidence.57060c88a3"))
        + context.text("diagnostic_evidence.a7e5ab76ff")
      tone = .information
    case 403:
      result = context.text("diagnostic_evidence.0bd47f398b")
      tone = .difference
    case 429:
      result = context.text("diagnostic_evidence.64b9b59de3")
      tone = .difference
    case 500..<600:
      result = context.text("diagnostic_evidence.19bd07e94d")
      tone = .failure
    case 1..<200, 400..<500:
      result = context.text("diagnostic_evidence.eaf4134e5b")
      tone = .difference
    default:
      return Assessment(
        text: problem(probe.errorCode, context: context),
        tone: probe.state == "not_started" ? .pending : .failure)
    }
    return Assessment(text: result + " · \(probe.httpStatus)", tone: tone)
  }

  public static func sourceAssessment(
    _ source: NetworkDiagnosticReport.Source, all: [NetworkDiagnosticReport.Source],
    context: PresentationContext = .chinese
  ) -> Assessment {
    if source.providerID == "ripestat" {
      return Assessment(
        text: context.text("diagnostic.source.bgp_origin") + source.asn, tone: .information)
    }
    let missing = missingSourceFields(source, context: context)
    let geographic = all.filter { $0.providerID != "ripestat" }
    let conflicts = [
      (comparableCountry(source.country), geographic.map { comparableCountry($0.country) }),
      (source.city, geographic.map(\.city)),
      (normalizedASN(source.asn), geographic.map { normalizedASN($0.asn) }),
    ]
    .contains { value, others in !value.isEmpty && others.contains { !$0.isEmpty && $0 != value } }
    let location = [
      source.country.isEmpty ? "" : countryName(source.country, context: context), source.city,
    ].filter {
      !$0.isEmpty
    }.joined(separator: " · ")
    var parts = [location].filter { !$0.isEmpty }
    if conflicts { parts.append(context.text("diagnostic_evidence.1e37daf677")) }
    if !missing.isEmpty {
      parts.append(context.text("diagnostic_evidence.d08d064a3c") + missing.joined(separator: "/"))
    }
    if parts.isEmpty { parts = [context.text("diagnostic_evidence.666622a50f")] }
    return Assessment(
      text: parts.joined(separator: "；"),
      tone: conflicts ? .difference : missing.isEmpty ? .information : .pending)
  }

  public static func sourceOverview(
    _ sources: [NetworkDiagnosticReport.Source], context: PresentationContext = .chinese
  ) -> Assessment {
    guard !sources.isEmpty else {
      return Assessment(text: context.text("diagnostic_evidence.084a9f8fa4"), tone: .pending)
    }
    let geographic = sources.filter { $0.providerID != "ripestat" }
    let groups = [
      (
        context.text("diagnostic_evidence.0cc659a9f5"),
        geographic.map { comparableCountry($0.country) }
      ),
      (context.text("diagnostic_evidence.ed84a1665f"), geographic.map(\.city)),
      ("ASN", geographic.map { normalizedASN($0.asn) }),
    ]
    let conflicts = groups.filter { Set($0.1.filter { !$0.isEmpty }).count > 1 }.map(\.0)
    let incomplete = sources.filter { !missingSourceFields($0, context: context).isEmpty }.count
    let compared = groups.filter { $0.1.filter { !$0.isEmpty }.count >= 2 }.count
    let direct = Set(sources.compactMap(\.providerID).filter { $0 != "ipx.al" }).count
    let fallback = sources.contains { $0.providerID == "ipx.al" }
    var parts = [
      context.text("diagnostic_evidence.a9a2fd9f8e", ["arg0": String(describing: sources.count)])
    ]
    if sources.allSatisfy({ $0.providerID == nil }) {
      parts.append(context.text("diagnostic_evidence.113b6ed5e9"))
    }
    if direct > 0 {
      parts.append(
        context.text("diagnostic_evidence.718a2e9eef", ["arg0": String(describing: direct)]))
    }
    if fallback { parts.append(context.text("diagnostic_evidence.4b7dc00e35")) }
    if !conflicts.isEmpty {
      parts.append(
        conflicts.joined(separator: "/") + context.text("diagnostic_evidence.59004f23b5"))
    } else {
      parts.append(
        compared > 0
          ? context.text("diagnostic_evidence.c31886705d")
          : context.text("diagnostic_evidence.bc4ae3d747"))
    }
    if incomplete > 0 {
      parts.append(
        context.text("diagnostic_evidence.a7e9e6880e", ["arg0": String(describing: incomplete)]))
    }
    return Assessment(
      text: parts.joined(separator: "；"), tone: conflicts.isEmpty ? .information : .difference)
  }

  public static func riskAssessment(_ fact: NetworkDiagnosticFact) -> Assessment {
    riskAssessment(fact, context: .chinese)
  }
  public static func riskAssessment(_ fact: NetworkDiagnosticFact, context: PresentationContext)
    -> Assessment
  {
    let kind = factKind(fact)
    switch fact.state {
    case "hit":
      let key = "diagnostic.risk." + kind
      let known = [
        "open_ports", "known_vulnerabilities", "self_signed_certificate", "low_human_traffic",
        "proxy_risk",
      ]
      return Assessment(
        text: known.contains(kind)
          ? context.text(key)
          : context.text("diagnostic.risk.marked", ["label": factLabel(fact, context: context)]),
        tone: .difference)
    case "clear":
      return Assessment(
        text: context.text(
          ["open_ports", "known_vulnerabilities", "shodan_tags"].contains(kind)
            ? "diagnostic.risk.not_listed" : "diagnostic.risk.not_marked"), tone: .information)
    default:
      // Legacy source text is evidence, never the translated display label.
      return Assessment(
        text: context.text(
          fact.source.contains("未配置")
            ? "diagnostic.risk.unconfigured" : "diagnostic.risk.unknown"), tone: .pending)
    }
  }
  public static func riskOverview(
    _ facts: [NetworkDiagnosticFact], context: PresentationContext = .chinese
  ) -> Assessment {
    guard !facts.isEmpty else {
      return Assessment(text: context.text("diagnostic.risk.missing"), tone: .pending)
    }
    let hits = facts.filter { $0.state == "hit" }.map { factLabel($0, context: context) }
    let unknown = facts.filter { !["hit", "clear", "information"].contains($0.state) }.count
    var parts = [
      hits.isEmpty
        ? context.text(
          unknown == facts.count
            ? "diagnostic.risk.no_judgment" : "diagnostic.risk.no_flags")
        : context.text(
          "diagnostic.risk.marked_list",
          ["labels": hits.joined(separator: context.language == .chinese ? "、" : ", ")])
    ]
    if unknown > 0 { parts.append(context.text("diagnostic.risk.pending", count: unknown)) }
    return Assessment(
      text: parts.joined(separator: " · "),
      tone: hits.isEmpty ? (unknown > 0 ? .pending : .information) : .difference)
  }

  static func normalizedASN(_ value: String) -> String {
    value.uppercased().replacingOccurrences(of: "AS", with: "").trimmingCharacters(in: .whitespaces)
  }
}
