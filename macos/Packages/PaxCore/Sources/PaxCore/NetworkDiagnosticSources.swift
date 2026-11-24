import Foundation
import PaxClient

extension NetworkDiagnosticPresentation {
  /// A displayed source may have no returned record; it must never become evidence.
  public struct SourceRow: Sendable {
    public let title, value, detail: String
    public let tone: EnvironmentDiagnosticPresentation.Tone
    public let providerID: String?
    public let unavailable: Bool
  }

  private static let directSources: [(name: String, provider: String)] = [
    ("ipquery", "ipquery"), ("ipwho.is", "ipwho.is"),
    ("db-ip", "db-ip"), ("ip2location.io", "ip2location.io"), ("ipapi.is", "ipapi.is"),
    ("proxycheck.io", "proxycheck.io"), ("IPLocate", "iplocate"), ("RIPEstat", "ripestat"),
    ("ip.sb", "ip.sb"), ("geojs", "geojs"),
  ]

  static func missingSourceFields(
    _ source: NetworkDiagnosticReport.Source, context: PresentationContext
  ) -> [String] {
    let fields = [
      (
        context.text("diagnostic_evidence.0cc659a9f5"), source.country,
        source.providerID != "ripestat"
      ),
      (
        context.text("diagnostic_evidence.ed84a1665f"), source.city, source.providerID != "ripestat"
      ),
      ("ASN", source.asn, source.providerID != "db-ip"),
    ]
    return fields.filter { $0.2 && $0.1.isEmpty }.map(\.0)
  }

  static func comparableCountry(_ country: String) -> String {
    let name =
      country.utf8.count == 2
      ? Locale(identifier: "en").localizedString(forRegionCode: country) ?? country : country
    return name.lowercased()
  }

  private static func sourceName(_ name: String, providerID: String?) -> String {
    switch providerID {
    case "db-ip": "DB-IP"
    case "ip2location.io": "IP2Location.io"
    default: name
    }
  }

  /// One row for each requested direct source. Historical transfers stay out of the comparison.
  @MainActor public static func sourceRows(
    _ report: NetworkDiagnosticReport?, context: PresentationContext = .chinese
  ) -> [SourceRow] {
    let sources = comparableSources(report)
    let queried = Set(report?.providers?.map(\.id) ?? [])
    let ordered = directSources.filter { item in
      queried.contains(item.provider) || sources.contains { $0.providerID == item.provider }
    }
    let rows: [SourceRow] = ordered.compactMap { item in
      if let source = sources.first(where: { $0.providerID == item.provider }) {
        return returnedSourceRow(source, all: sources, report: report, context: context)
      }
      guard let provider = report?.providers?.first(where: { $0.id == item.provider }) else {
        return nil
      }
      return missingSourceRow(item.name, provider: provider, report: report, context: context)
    }
    return rows.filter { $0.tone == .difference } + rows.filter { $0.tone == .pending }
      + rows.filter { $0.tone != .difference && $0.tone != .pending }
  }

  public static func sourceListOverview(
    _ rows: [SourceRow], context: PresentationContext = .chinese
  ) -> Assessment {
    guard !rows.isEmpty else {
      return Assessment(text: context.text("diagnostic.overview.no_sources"), tone: .pending)
    }
    let differing = rows.filter { $0.tone == .difference }.count
    let missing = rows.filter { $0.tone == .pending }.count
    var parts: [String] = []
    if differing > 0 {
      parts.append(context.text("diagnostic.overview.sources_differing", count: differing))
    }
    if missing > 0 {
      parts.append(context.text("diagnostic.overview.sources_missing", count: missing))
    }
    parts.append(context.text("diagnostic.overview.sources_count", count: rows.count))
    return Assessment(
      text: parts.joined(separator: " · "),
      tone: differing > 0 || missing > 0 ? .difference : .information)
  }

  private static func comparableSources(_ report: NetworkDiagnosticReport?)
    -> [NetworkDiagnosticReport.Source]
  {
    let sources = report?.sources ?? []
    return directSources.compactMap { item in sources.first { $0.providerID == item.provider } }
  }

  @MainActor private static func returnedSourceRow(
    _ source: NetworkDiagnosticReport.Source, all: [NetworkDiagnosticReport.Source],
    report: NetworkDiagnosticReport?, context: PresentationContext
  ) -> SourceRow {
    let assessment = sourceAssessment(source, all: all, context: context)
    var fields = [
      context.text("diagnostic_evidence.beedab4aae")
        + countryName(source.country, context: context),
      context.text("diagnostic_evidence.485cd74fe4") + value(source.city, context: context),
      "ASN：" + value(source.asn, context: context),
      context.text("diagnostic_evidence.992a5d4d3c") + value(source.org, context: context),
      context.text("diagnostic_evidence.baca4484df") + value(source.providerDate, context: context),
      providerDetails(report, id: source.providerID, context: context),
    ]
    if source.providerID == "ripestat" {
      fields.removeFirst(2)
      fields[0] = context.text("diagnostic.source.bgp_origin") + source.asn
    }
    fields += sourceDetails(report, id: source.providerID, context: context)
    return SourceRow(
      title: sourceName(source.name, providerID: source.providerID),
      value: assessment.text, detail: fields.joined(separator: "\n"), tone: assessment.tone,
      providerID: source.providerID, unavailable: false)
  }

  private static func sourceDetails(
    _ report: NetworkDiagnosticReport?, id: String?, context: PresentationContext
  ) -> [String] {
    guard let id else { return [] }
    return (report?.facts ?? []).filter { $0.providerID == id }.compactMap { fact in
      switch fact.group {
      case "source_network":
        return context.text("diagnostic.source.bgp_prefix") + fact.value
      case "source_risk":
        let state =
          fact.state == "hit"
          ? context.text("diagnostic.source.flagged") : context.text("diagnostic.risk.not_marked")
        return factLabel(fact, context: context) + " · " + state
      default:
        return nil
      }
    }
  }

  @MainActor private static func missingSourceRow(
    _ name: String, provider: NetworkDiagnosticReport.Provider, report: NetworkDiagnosticReport?,
    context: PresentationContext
  ) -> SourceRow {
    let providerID = provider.id
    let state =
      ["observed", "partial"].contains(provider.state)
      ? context.text("diagnostic_evidence.a73b799126")
      : providerState(provider, context: context) + context.text("diagnostic_evidence.5c90263f62")
    let channelText = channel(providerID, context: context)
    return SourceRow(
      title: sourceName(name, providerID: providerID),
      value: state,
      detail: context.text("diagnostic_evidence.b326e8192b") + channelText + "\n" + state + "\n"
        + ([providerDetails(report, id: providerID, context: context)]
        + sourceDetails(report, id: providerID, context: context)).joined(separator: "\n")
        + context.text("diagnostic_evidence.43d1fc457d"),
      tone: .pending, providerID: providerID, unavailable: true)
  }
}
