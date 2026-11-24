import Foundation
import PaxClient

extension NetworkDiagnosticPresentation {
  public static func channel(_ id: String?, context: PresentationContext = .chinese) -> String {
    guard let id, !id.isEmpty else { return context.text("diagnostic_evidence.dac5117cf5") }
    return id == "ipx.al"
      ? context.text("diagnostic_evidence.2853d2265f")
      : context.text("diagnostic_evidence.9c783c56dc") + id
  }

  public static func capabilityName(_ value: String, context: PresentationContext = .chinese)
    -> String
  {
    [
      "geo.country": context.text("diagnostic_evidence.0cc659a9f5"), "geo.asn": "ASN",
      "geo.city": context.text("diagnostic_evidence.ed84a1665f"),
      "bgp.prefix": context.text("diagnostic.source.bgp_prefix").trimmingCharacters(
        in: .punctuationCharacters),
      "risk.proxy": context.text("diagnostic_evidence.5e84ea61e8"), "risk.vpn": "VPN",
      "risk.tor": "Tor", "risk.datacenter": context.text("diagnostic_evidence.37727441a2"),
      "risk.mobile": context.text("diagnostic_evidence.a5c27195f9"),
      "exposure.ports": context.text("diagnostic_evidence.b4de56b75b"),
      "exposure.vulns": context.text("diagnostic_evidence.c2771a9f31"),
      "exposure.tags": context.text("diagnostic_evidence.fbfc455154"),
      "risk.relay": context.text("diagnostic_evidence.8f827a1a74"),
      "risk.anonymous": context.text("diagnostic_evidence.47858034c7"),
      "risk.abuse": context.text("diagnostic_evidence.7ae2f9ea88"),
      "risk.proxy_score": context.text("diagnostic_evidence.7b4fc92bc7"),
      "risk.crawler": context.text("diagnostic_evidence.4c480479f7"),
      "risk.threat": context.text("diagnostic_evidence.89748218e6"),
      "risk.scanner": context.text("diagnostic_evidence.8b1a92ecd3"),
      "risk.dnsbl": context.text("diagnostic_evidence.2588a90b8a"),
      "exposure.certificate": context.text("diagnostic_evidence.84c85aec1b"),
      "risk.residential": context.text("diagnostic_evidence.8ad0c5026e"),
      "risk.human": context.text("diagnostic_evidence.db382141d6"),
      "risk.broadcast": context.text("diagnostic_evidence.4fae07c642"),
    ][value] ?? value
  }

  public static func providerState(
    _ provider: NetworkDiagnosticReport.Provider, context: PresentationContext = .chinese
  ) -> String {
    switch provider.state {
    case "observed": return context.text("diagnostic_evidence.388de336c0")
    case "partial": return context.text("diagnostic_evidence.69400e86b5")
    case "not_indexed": return context.text("diagnostic_evidence.fc0ba14b5e")
    case "unsupported": return context.text("diagnostic_evidence.41ade02db3")
    case "not_configured": return context.text("diagnostic_evidence.d2cdeb54eb")
    default: return problem(provider.errorCode, context: context)
    }
  }

  @MainActor public static func providerDetails(
    _ report: NetworkDiagnosticReport?, id: String?, context: PresentationContext = .chinese
  ) -> String {
    guard let provider = report?.providers?.first(where: { $0.id == id }) else {
      return id == nil
        ? context.text("diagnostic_evidence.c6836a4151")
        : context.text("diagnostic_evidence.0737a77900")
    }
    var lines = [
      context.text("diagnostic_evidence.b326e8192b") + channel(provider.id, context: context),
      context.text("diagnostic_evidence.dc4a9daa70") + provider.queryIP + " · " + provider.family,
      context.text("diagnostic_evidence.732f16436c")
        + (provider.at.map { MeasurementTimestamp.secondsText($0) }
          ?? context.text("diagnostic_evidence.7450cc5b16")),
      context.text("diagnostic_evidence.e25c88e513") + providerState(provider, context: context)
        + " · \(provider.durationMS) ms",
    ]
    if provider.httpStatus > 0 { lines.append("HTTP：\(provider.httpStatus)") }
    if !provider.capabilities.isEmpty {
      lines.append(
        context.text("diagnostic_evidence.de7fb4b798")
          + provider.capabilities.map { capabilityName($0, context: context) }.joined(
            separator: "、"))
    }
    if !provider.fallbackFor.isEmpty {
      lines.append(
        provider.fallbackReason == "independent_coverage_missing"
          ? context.text("diagnostic_evidence.e0cca5c68a")
          : context.text("diagnostic_evidence.f698d2d2ba"))
      lines.append(
        context.text("diagnostic_evidence.03134622e3")
          + provider.fallbackFor.map { capabilityName($0, context: context) }.joined(separator: "、")
      )
    }
    return lines.joined(separator: "\n")
  }

  public static func acquisitionSummary(
    _ report: NetworkDiagnosticReport?, context: PresentationContext = .chinese
  ) -> String {
    guard let providers = report?.providers else {
      return context.text("diagnostic_evidence.9325ec09e6")
    }
    let direct = providers.filter { $0.id != "ipx.al" }
    let answered = direct.filter { ["observed", "partial", "not_indexed"].contains($0.state) }.count
    if let fallback = providers.first(where: { $0.id == "ipx.al" }) {
      return context.text(
        "diagnostic_evidence.6c686d7b7c",
        ["arg0": String(describing: answered), "arg1": String(describing: direct.count)])
        + providerState(fallback, context: context)
    }
    return context.text(
      "diagnostic_evidence.dedda3b38d",
      ["arg0": String(describing: answered), "arg1": String(describing: direct.count)])
  }

  public static func nativeAssessment(
    _ report: NetworkDiagnosticReport?, context: PresentationContext = .chinese
  ) -> Assessment {
    let fact = report?.facts.first { $0.group == "registration" && $0.label == "原生 / 广播" }
    let name: String
    switch fact?.value {
    case "native": name = context.text("diagnostic_evidence.d8750d60af")
    case "broadcast": name = context.text("diagnostic_evidence.4fae07c642")
    default: return Assessment(text: context.text("diagnostic_evidence.c098e854e6"), tone: .pending)
    }
    return Assessment(text: name, tone: .information)
  }

  public static func riskCounts(_ report: NetworkDiagnosticReport?) -> (hits: Int, unknown: Int) {
    let groups = Dictionary(
      grouping: report?.facts.filter { $0.group == "risk" } ?? [], by: \.label)
    return (
      groups.values.filter { $0.contains { $0.state == "hit" } }.count,
      groups.values.filter { !$0.contains { ["hit", "clear", "information"].contains($0.state) } }
        .count
    )
  }

  @MainActor public static func factDetails(
    _ fact: NetworkDiagnosticFact?, report: NetworkDiagnosticReport?,
    context: PresentationContext = .chinese
  )
    -> String
  {
    guard let fact else { return context.text("diagnostic_evidence.adae7def03") }
    if fact.providerID == nil, report?.providers != nil {
      return context.text("diagnostic_evidence.17e6171763") + fact.source
        + context.text("diagnostic_evidence.7c4fe31b9d")
    }
    let records =
      report?.facts.filter {
        $0.group == "exposure_detail" && $0.label == fact.label && $0.providerID == fact.providerID
      }.map(\.value) ?? []
    let lists =
      fact.label == "邮件 DNSBL"
      ? (report?.facts.filter { $0.group == "dnsbl" && $0.providerID == fact.providerID }
        .map { $0.label + "：" + factState($0.state, context: context) } ?? []) : []
    let numbers =
      report?.facts.filter {
        $0.group == "raw_risk" && $0.providerID == fact.providerID
      }.map { $0.label + "：" + $0.value + context.text("diagnostic_evidence.cfd800000c") } ?? []
    return
      ([
        context.text("diagnostic_evidence.17e6171763") + fact.source,
        context.text("diagnostic_evidence.0fc5a5c889") + factState(fact.state, context: context),
        providerDetails(report, id: fact.providerID, context: context),
      ] + records + lists + numbers + [
        fact.providerID == "internetdb" ? context.text("diagnostic_evidence.d55371c8c4") : "",
        context.text("diagnostic_evidence.8fe100bd59"),
      ])
      .joined(separator: "\n")
  }
}
