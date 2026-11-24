import Foundation
import PaxClient

/// Evidence-only summaries shared by the native workbench and frozen export.
public enum NetworkDiagnosticPresentation {
  public struct TargetSummary: Equatable, Sendable {
    public let target: String
    public let samples: [NetworkDiagnosticReport.Probe]
    public var successful: [NetworkDiagnosticReport.Probe] { samples.filter(\.expectedResponse) }
    public var firstMS: UInt64? {
      samples.first(where: { $0.attempt == 1 && $0.expectedResponse })?.durationMS
    }
    public var median: UInt64? {
      let values = successful.filter { $0.attempt > 1 }.map(\.durationMS).sorted()
      guard !values.isEmpty else { return nil }
      let middle = values.count / 2
      return values.count.isMultiple(of: 2)
        ? (values[middle - 1] + values[middle]) / 2 : values[middle]
    }
    public var range: String {
      let values = successful.filter { $0.attempt > 1 }.map(\.durationMS)
      guard values.count >= 3, let low = values.min(), let high = values.max() else {
        return "样本不足"
      }
      return "\(low)–\(high) ms"
    }
    public var count: String { "\(successful.count)/\(samples.count) 请求成功" }
  }
  public static func targets(_ report: NetworkDiagnosticReport?) -> [TargetSummary] {
    guard let report else { return [] }
    let targets = report.probes.reduce(into: [String]()) {
      if !$0.contains($1.target) { $0.append($1.target) }
    }
    return targets.map { target in
      TargetSummary(target: target, samples: report.probes.filter { $0.target == target })
    }
  }
  public static func typeName(_ value: String) -> String { typeName(value, context: .chinese) }
  public static func typeName(_ value: String, context: PresentationContext) -> String {
    switch value {
    case "hosting": context.text("diagnostic_basic.type.hosting")
    case "isp": context.text("diagnostic_basic.type.isp")
    case "mobile": context.text("diagnostic_basic.type.mobile")
    case "business": context.text("diagnostic_basic.type.business")
    case "edu": context.text("diagnostic_basic.type.edu")
    case "pseudo_residential", "pseudo": context.text("diagnostic_basic.type.pseudo")
    case "native": context.text("diagnostic_basic.type.native")
    case "broadcast": context.text("diagnostic_basic.type.broadcast")
    case "": context.text("diagnostic_basic.missing")
    default: value
    }
  }
  public static func value(_ value: String, context: PresentationContext = .chinese) -> String {
    value.isEmpty ? context.text("diagnostic_basic.missing") : value
  }
  public static func factState(_ state: String, context: PresentationContext = .chinese) -> String {
    switch state {
    case "hit": context.text("diagnostic_basic.state.hit")
    case "clear": context.text("diagnostic_basic.state.clear")
    case "information": context.text("diagnostic_basic.state.information")
    default: context.text("diagnostic_basic.state.unknown")
    }
  }
  public static func countrySummary(
    _ report: NetworkDiagnosticReport?, context: PresentationContext = .chinese
  ) -> String {
    guard let report else { return context.text("diagnostic_evidence.1261a36c69") }
    let counts = Dictionary(grouping: report.sources.filter { !$0.country.isEmpty }, by: \.country)
    guard !counts.isEmpty else { return context.text("diagnostic_evidence.2e2b7ac785") }
    return counts.keys.sorted().map {
      context.text(
        "diagnostic_evidence.afce46e960",
        [
          "arg0": String(describing: countryName($0, context: context)),
          "arg1": String(describing: counts[$0]?.count ?? 0),
        ])
    }.joined(
      separator: " · ")
  }
  public static func organization(
    _ report: NetworkDiagnosticReport?, context: PresentationContext = .chinese
  ) -> String {
    guard let report else { return context.text("diagnostic_evidence.5a7aa2274e") }
    if let isp = report.exit?.isp, !isp.isEmpty { return isp }
    let expected = (report.exit?.asn ?? "").replacingOccurrences(of: "AS", with: "")
    let names = Set(
      report.sources.filter {
        expected.isEmpty || $0.asn.replacingOccurrences(of: "AS", with: "") == expected
      }.map(\.org).filter { !$0.isEmpty })
    if names.count == 1 { return names.first ?? context.text("diagnostic_evidence.5a7aa2274e") }
    return names.isEmpty
      ? context.text("diagnostic_evidence.5a7aa2274e")
      : context.text("diagnostic_evidence.3d27d00a9b")
  }
  public static func countryName(_ code: String, context: PresentationContext = .chinese) -> String
  {
    guard !code.isEmpty else { return context.text("diagnostic_evidence.756762e293") }
    return Locale(identifier: context.language == .chinese ? "zh_CN" : "en").localizedString(
      forRegionCode: code) ?? code
  }
  public static func utcOffset(_ zone: String, at: Date, context: PresentationContext = .chinese)
    -> String
  {
    guard let zone = TimeZone(identifier: zone) else {
      return context.text("diagnostic_evidence.3d2a7f060d")
    }
    let seconds = zone.secondsFromGMT(for: at)
    return String(
      format: "UTC%@%02d:%02d", seconds < 0 ? "−" : "+", abs(seconds) / 3600,
      abs(seconds) % 3600 / 60)
  }
  public static func languageName(_ value: String) -> String {
    let locale = Locale(identifier: "zh_CN")
    return (locale.localizedString(forIdentifier: value) ?? value) + "（" + value + "）"
  }
  public static func downloadEnd(_ code: String, context: PresentationContext = .chinese) -> String
  {
    switch code {
    case "byte_cap": context.text("diagnostic_evidence.c95195e78a")
    case "sample_window": context.text("diagnostic_evidence.621ad125dc")
    case "budget_window", "total_timeout": context.text("diagnostic_evidence.e28c490f7a")
    case "sample_short": context.text("diagnostic_evidence.42bb592d33")
    case "body_short": context.text("diagnostic_evidence.f4a6b51ae2")
    case "cancelled": context.text("diagnostic_evidence.6ddf680b84")
    case "body_interrupted", "transfer_failed": context.text("diagnostic_evidence.236ff1b3fb")
    case "headers_failed": context.text("diagnostic_evidence.2d2f6fd386")
    default: context.text("diagnostic_evidence.49d78058cb")
    }
  }
  public static func problem(_ code: String, context: PresentationContext = .chinese) -> String {
    if code.isEmpty { return context.text("diagnostic_evidence.756762e293") }
    if code.hasPrefix("provider_http_") {
      return context.text("diagnostic_evidence.5e812cf45d") + code.dropFirst("provider_http_".count)
    }
    if code.hasPrefix("target_http_") {
      return "HTTP " + code.dropFirst("target_http_".count)
        + context.text("diagnostic_evidence.ba12f16899")
    }
    switch code {
    case "provider_ip_mismatch": return context.text("diagnostic_evidence.9df4cef2a0")
    case "intelligence_unavailable": return context.text("diagnostic_evidence.86464e1ad4")
    case "provider_timeout": return context.text("diagnostic_evidence.9df6b0bf7c")
    case "provider_fields_missing": return context.text("diagnostic_evidence.9e927a668d")
    case "provider_family_unsupported": return context.text("diagnostic_evidence.06965b33b8")
    case "exit_services_unavailable": return context.text("diagnostic_evidence.24d352781e")
    case "operation_cancelled": return context.text("diagnostic_evidence.cada50959f")
    case "provider_response_invalid": return context.text("diagnostic_evidence.ce18450db7")
    case "exit_unavailable": return context.text("diagnostic_evidence.e727e25758")
    case "target_connection_timeout": return context.text("diagnostic_evidence.0c3dc437d9")
    case "target_tls_failed", "tls_certificate_invalid":
      return context.text("diagnostic_evidence.453dc16fde")
    case "target_connection_refused": return context.text("diagnostic_evidence.3334a2763d")
    case "diagnostic_budget_exhausted": return context.text("diagnostic_evidence.9d6f7b5f52")
    case "diagnostic_cancelled": return context.text("diagnostic_evidence.d313044b1b")
    case "target_dns_failed": return context.text("diagnostic_evidence.d5771b9733")
    case "target_network_unreachable": return context.text("diagnostic_evidence.8d069ce371")
    case "target_connection_reset": return context.text("diagnostic_evidence.5350a10b9a")
    case "target_connection_closed": return context.text("diagnostic_evidence.6cdaeb973b")
    case "target_connection_failed": return context.text("diagnostic_evidence.0a920129cf")
    case "node_connection_timeout": return context.text("diagnostic_evidence.809cfb6b18")
    default:
      return context.text("diagnostic_evidence.52d9d6a8fc", ["arg0": String(describing: code)])
    }
  }
  public static func probeResult(
    _ sample: NetworkDiagnosticReport.Probe, context: PresentationContext = .chinese
  ) -> String {
    if sample.httpStatus == 0 {
      return problem(sample.errorCode, context: context)
        + (sample.stage.map { context.text("diagnostic_evidence.b55b1b06e9") + $0 } ?? "")
        + (sample.state == "not_started" ? "" : " · \(sample.durationMS) ms")
    }
    let meaning: String
    switch sample.httpStatus {
    case 401: meaning = context.text("diagnostic_evidence.cc614c9254")
    case 403: meaning = context.text("diagnostic_evidence.e8082597ce")
    case 429: meaning = context.text("diagnostic_evidence.c5cf8ac4c6")
    case 300..<400:
      meaning =
        context.text("diagnostic_evidence.f421626bbe")
        + (sample.redirectHost?.isEmpty == false ? " → " + (sample.redirectHost ?? "") : "")
    default: meaning = context.text("diagnostic_evidence.ecc24c7e17")
    }
    return "HTTP \(sample.httpStatus) · " + meaning + " · \(sample.durationMS) ms"
  }
}
