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

  /// Fixed baseline plus any additional returned sources. No mutation of stored evidence.
  @MainActor public static func sourceRows(
    _ report: NetworkDiagnosticReport?, onlyDifferences: Bool = false
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
          sources[index], report: report, onlyDifferences: onlyDifferences)
        {
          rows.append(row)
        }
      } else {
        rows.append(missingSourceRow(expected.name, providerID: expected.provider, report: report))
      }
    }
    for index in sources.indices where !used.contains(index) {
      if let row = returnedSourceRow(
        sources[index], report: report, onlyDifferences: onlyDifferences)
      {
        rows.append(row)
      }
    }
    for provider in report?.providers ?? []
    where ["ip.sb", "geojs"].contains(provider.id)
      && !sources.contains(where: { $0.providerID == provider.id })
    {
      rows.append(missingSourceRow(provider.id, providerID: provider.id, report: report))
    }
    return rows
  }

  @MainActor public static func sourceListOverview(_ report: NetworkDiagnosticReport?) -> Assessment
  {
    let summary = sourceOverview(report?.sources ?? [])
    let missing = sourceRows(report).filter(\.unavailable).count
    guard missing > 0 else { return summary }
    return Assessment(text: summary.text + "；\(missing) 项未返回资料", tone: .pending)
  }

  @MainActor private static func returnedSourceRow(
    _ source: NetworkDiagnosticReport.Source, report: NetworkDiagnosticReport?,
    onlyDifferences: Bool
  ) -> SourceRow? {
    let all = report?.sources ?? []
    if onlyDifferences,
      ![source.country, source.city, source.asn].contains(where: \.isEmpty),
      let report, !differences(report, source: source)
    {
      return nil
    }
    let assessment = sourceAssessment(source, all: all)
    let detail = [
      "国家：" + countryName(source.country), "城市：" + value(source.city),
      "ASN：" + value(source.asn), "组织：" + value(source.org),
      "来源更新时间：" + value(source.providerDate), providerDetails(report, id: source.providerID),
    ].joined(separator: "\n")
    return SourceRow(
      title: source.name + (source.providerID == "ipx.al" ? " · 补充" : ""),
      value: assessment.text, detail: detail, tone: assessment.tone, unavailable: false)
  }

  @MainActor private static func missingSourceRow(
    _ name: String, providerID: String, report: NetworkDiagnosticReport?
  ) -> SourceRow {
    let provider = report?.providers?.first { $0.id == providerID }
    let state: String
    if report == nil {
      state = "尚未检测"
    } else if let provider {
      state =
        ["observed", "partial"].contains(provider.state)
        ? "未返回本来源定位资料" : providerState(provider) + " · 本来源未取得资料"
    } else {
      state = report?.providers == nil ? "旧记录未包含本来源资料" : "本次未请求该来源"
    }
    let channelText = providerID == "ipx.al" ? "补充 · 经 ipx.al" : "直接查询 · " + providerID
    return SourceRow(
      title: name + (providerID == "ipx.al" ? " · 补充" : ""), value: state,
      detail: "获取渠道：" + channelText + "\n" + state + "\n"
        + providerDetails(report, id: providerID) + "\n未取得资料，不参与来源对照与分歧统计。",
      tone: .pending, unavailable: true)
  }
}
