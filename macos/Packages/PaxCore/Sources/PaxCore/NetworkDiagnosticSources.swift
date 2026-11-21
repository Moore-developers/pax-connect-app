import Foundation
import PaxClient

extension NetworkDiagnosticPresentation {
  /// A displayed source may have no returned record; it must never become evidence.
  public struct SourceRow: Sendable {
    public let title, value, detail: String
    public let tone: EnvironmentDiagnosticPresentation.Tone
    let unavailable: Bool
  }

  private static let expectedSources: [(name: String, provider: String)] = [
    ("ipquery", "ipquery"), ("ipwho.is", "ipwho.is"),
    ("Cloudflare 边缘", "ipx.al"), ("MaxMind GeoLite", "ipx.al"),
    ("IPinfo", "ipx.al"), ("IP.SB", "ipx.al"), ("ipapi.is", "ipx.al"),
    ("proxycheck.io", "ipx.al"), ("IPQuery", "ipx.al"), ("IP-API", "ipx.al"),
    ("IPLocate", "ipx.al"), ("ip2region", "ipx.al"),
  ]

  public static func sourceName(_ name: String, providerID: String?, context: PresentationContext)
    -> String
  {
    providerID == "ipx.al" && name == "Cloudflare 边缘"
      ? context.text("diagnostic.source.cloudflare_edge") : name
  }

  /// Fixed baseline plus any additional returned sources. No mutation of stored evidence.
  @MainActor public static func sourceRows(
    _ report: NetworkDiagnosticReport?, onlyDifferences: Bool = false,
    context: PresentationContext = .chinese
  ) -> [SourceRow] {
    let sources = report?.sources ?? []
    var used = Set<Int>()
    var rows: [SourceRow] = []
    for expected in expectedSources {
      if let index = sources.indices.first(where: {
        !used.contains($0) && sources[$0].name == expected.name
          && (sources[$0].providerID == expected.provider || sources[$0].providerID == nil)
      }) {
        used.insert(index)
        if let row = returnedSourceRow(
          sources[index], report: report, onlyDifferences: onlyDifferences, context: context)
        {
          rows.append(row)
        }
      } else {
        rows.append(
          missingSourceRow(
            expected.name, providerID: expected.provider, report: report, context: context))
      }
    }
    for index in sources.indices where !used.contains(index) {
      if let row = returnedSourceRow(
        sources[index], report: report, onlyDifferences: onlyDifferences, context: context)
      {
        rows.append(row)
      }
    }
    for provider in report?.providers ?? []
    where ["ip.sb", "geojs"].contains(provider.id)
      && !sources.contains(where: { $0.providerID == provider.id })
    {
      rows.append(
        missingSourceRow(provider.id, providerID: provider.id, report: report, context: context))
    }
    return rows
  }

  @MainActor public static func sourceListOverview(
    _ report: NetworkDiagnosticReport?, context: PresentationContext = .chinese
  ) -> Assessment {
    let summary = sourceOverview(report?.sources ?? [], context: context)
    let missing = sourceRows(report, context: context).filter(\.unavailable).count
    guard missing > 0 else { return summary }
    return Assessment(
      text: summary.text
        + context.text("diagnostic_evidence.1d8c059331", ["arg0": String(describing: missing)]),
      tone: .pending)
  }

  @MainActor private static func returnedSourceRow(
    _ source: NetworkDiagnosticReport.Source, report: NetworkDiagnosticReport?,
    onlyDifferences: Bool, context: PresentationContext = .chinese
  ) -> SourceRow? {
    let all = report?.sources ?? []
    if onlyDifferences,
      ![source.country, source.city, source.asn].contains(where: \.isEmpty),
      let report, !differences(report, source: source)
    {
      return nil
    }
    let assessment = sourceAssessment(source, all: all, context: context)
    let detail = [
      context.text("diagnostic_evidence.beedab4aae")
        + countryName(source.country, context: context),
      context.text("diagnostic_evidence.485cd74fe4") + value(source.city, context: context),
      "ASN：" + value(source.asn, context: context),
      context.text("diagnostic_evidence.992a5d4d3c") + value(source.org, context: context),
      context.text("diagnostic_evidence.baca4484df") + value(source.providerDate, context: context),
      providerDetails(report, id: source.providerID, context: context),
    ].joined(separator: "\n")
    return SourceRow(
      title: sourceName(source.name, providerID: source.providerID, context: context)
        + (source.providerID == "ipx.al" ? context.text("diagnostic_evidence.aef5cea140") : ""),
      value: assessment.text, detail: detail, tone: assessment.tone, unavailable: false)
  }

  @MainActor private static func missingSourceRow(
    _ name: String, providerID: String, report: NetworkDiagnosticReport?,
    context: PresentationContext = .chinese
  ) -> SourceRow {
    let provider = report?.providers?.first { $0.id == providerID }
    let state: String
    if report == nil {
      state = context.text("diagnostic_evidence.4d2966c438")
    } else if let provider {
      state =
        ["observed", "partial"].contains(provider.state)
        ? context.text("diagnostic_evidence.a73b799126")
        : providerState(provider, context: context) + context.text("diagnostic_evidence.5c90263f62")
    } else {
      state =
        report?.providers == nil
        ? context.text("diagnostic_evidence.921b65493c")
        : context.text("diagnostic_evidence.0ca4fc212d")
    }
    let channelText =
      providerID == "ipx.al"
      ? context.text("diagnostic_evidence.2853d2265f")
      : context.text("diagnostic_evidence.9c783c56dc") + providerID
    return SourceRow(
      title: sourceName(name, providerID: providerID, context: context)
        + (providerID == "ipx.al" ? context.text("diagnostic_evidence.aef5cea140") : ""),
      value: state,
      detail: context.text("diagnostic_evidence.b326e8192b") + channelText + "\n" + state + "\n"
        + providerDetails(report, id: providerID, context: context)
        + context.text("diagnostic_evidence.43d1fc457d"),
      tone: .pending, unavailable: true)
  }
}
