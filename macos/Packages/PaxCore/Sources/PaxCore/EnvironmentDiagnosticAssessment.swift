import Foundation
import PaxClient

extension EnvironmentDiagnosticPresentation {
  /// Compact conclusions preserve full readings in the same row's details.
  public static func assessedRows(
    _ report: EnvironmentDiagnosticReport?, local: NetworkDiagnosticEnvironment?, waiting: Bool,
    includePhysical: Bool = true, context: PresentationContext = .chinese
  ) -> [Row] {
    let raw = mergedRows(report, local: local, waiting: waiting, context: context)
    guard let report else { return raw }
    let mac = report.mac
    let web = report.browser
    return raw.map { row in
      if row.kind == "location" || row.kind == "ipv6" { return row }
      var value = row.value
      var tone = row.tone
      var extra = riskExplanation(for: row.kind, context: context)
      switch row.kind {
      case "public_ip":
        let pairs = [("IPv4", mac.ipv4, web.ipv4), ("IPv6", mac.ipv6, web.ipv6)].filter {
          !$0.1.isEmpty && !$0.2.isEmpty
        }
        if let different = pairs.first(where: { $0.1 != $0.2 }) {
          value = context.text(
            "environment_evidence.8cd21214ae", ["arg0": String(describing: different.0)])
          tone = .difference
        } else if let first = pairs.first {
          value = context.text(
            "environment_evidence.e4e09c688d",
            ["arg0": String(describing: first.0), "arg1": String(describing: first.1)])
          tone = .information
        } else {
          value =
            waiting
            ? context.text("environment_evidence.1ef8818116")
            : context.text("environment_evidence.b1b841c146")
          tone = waiting ? .pending : .failure
        }
      case "language":
        let a = local?.languages.map { Locale(identifier: $0).identifier } ?? []
        let b = web.languages.map { Locale(identifier: $0).identifier }
        if !a.isEmpty && !b.isEmpty {
          let locale = Locale(identifier: context.language == .chinese ? "zh_CN" : "en")
          let names = web.languages.map { locale.localizedString(forIdentifier: $0) ?? $0 }
          value =
            a == b
            ? context.text("environment_evidence.c04b31fba1") + names.joined(separator: " → ")
            : context.text("environment_evidence.45ada6e86e")
          tone = .information
        } else {
          value =
            waiting
            ? context.text("environment_evidence.5e3cf85bdf")
            : context.text("environment_evidence.b34d663bf8")
        }
      case "timezone":
        if let zone = local?.timezone, !zone.isEmpty, !web.timezone.isEmpty,
          let a = TimeZone(identifier: zone), let b = TimeZone(identifier: web.timezone)
        {
          if zone == web.timezone {
            value =
              context.text("environment_evidence.0d78716951")
              + timezone(zone, at: report.at, context: context)
          } else {
            value =
              a.secondsFromGMT(for: report.at) == b.secondsFromGMT(for: report.at)
              ? context.text("environment_evidence.1600075c94")
              : context.text("environment_evidence.7e5b67df49")
          }
          tone = zone == web.timezone ? .information : .difference
        } else {
          value =
            waiting
            ? context.text("environment_evidence.8ea304fc23")
            : context.text("environment_evidence.5cc836cd9c")
        }
      case "dns":
        let verdict = dnsPathAssessment(report, waiting: waiting, context: context)
        value = verdict.text
        tone = verdict.tone
        let a = Set(mac.resolvers.map(\.ip))
        let b = Set(web.resolvers.map(\.ip))
        extra =
          context.text(
            "environment_evidence.3d3e5014d3",
            ["arg0": String(describing: a.count), "arg1": String(describing: b.count)])
          + dnsPathDetails(mac, name: "Mac", context: context) + "\n"
          + dnsPathDetails(
            web, name: context.text("environment_evidence.e19c3b9e26"), context: context)
      case "webrtc":
        let verdict = webRTCVerdict(web, pending: web.complete != true && waiting, context: context)
        value = verdict.summary
        tone = verdict.tone
        if includePhysical, let physical = verdict.physicalDetail { extra = physical }
      default: break
      }
      var detail = row.value + "\n" + row.detail + (extra.isEmpty ? "" : "\n" + extra)
      if row.kind == "dns" {
        for (name, sample) in [
          ("Mac", mac), (context.text("environment_evidence.e19c3b9e26"), web),
        ] {
          detail +=
            "\n" + name + context.text("environment_evidence.aa9c277ffa")
            + sample.resolvers.map {
              $0.ip + " · "
                + NetworkDiagnosticPresentation.countryName($0.country, context: context) + " · "
                + $0.organization
            }.joined(separator: "\n")
        }
      }
      return Row(
        title: row.kind, symbol: row.symbol, value: value, detail: detail, tone: tone,
        context: context)
    }
  }

  /// Shared by structured UI details and the report's evidence text.
  public static func riskExplanation(for title: String, context: PresentationContext = .chinese)
    -> String
  {
    switch title {
    case "public_ip": context.text("environment_evidence.51b757bcd4")
    case "language": context.text("environment_evidence.171bcfd4b6")
    case "timezone": context.text("environment_evidence.0d41ecc17c")
    case "dns": context.text("environment_evidence.9569d25d40")
    default: ""
    }
  }

  public static func exitAssessment(
    _ checks: [EnvironmentDiagnosticReport.Check], context: PresentationContext = .chinese
  )
    -> NetworkDiagnosticPresentation.Assessment
  {
    let a = checks.first { $0.kind == "echo_ipip" && $0.state == "observed" }
      .map { exitParts($0.value).ip }
    let b = checks.first { $0.kind == "echo_ipinfo" && $0.state == "observed" }
      .map { exitParts($0.value).ip }
    guard let a, let b, !a.isEmpty, !b.isEmpty else {
      return .init(text: context.text("environment_evidence.dc0767f559"), tone: .pending)
    }
    guard a.contains(":") == b.contains(":") else {
      return .init(text: context.text("environment_evidence.27c1178bb0"), tone: .information)
    }
    if a == b {
      return .init(text: context.text("environment_evidence.ca00570dd3"), tone: .information)
    }
    return .init(text: context.text("browser.check_exit_mismatch"), tone: .difference)
  }

  public static func exitReading(
    _ check: EnvironmentDiagnosticReport.Check, context: PresentationContext = .chinese
  ) -> String {
    guard check.kind == "echo_ipip" || check.kind == "echo_ipinfo" else { return check.value }
    let (ip, location) = exitParts(check.value)
    guard let location else { return ip + " · " + context.text("browser.check_exit_no_location") }
    if check.kind == "echo_ipip" { return ip + " · " + location }
    let fields = location.components(separatedBy: ", ")
    let country = fields.first ?? ""
    let name =
      country.isEmpty ? "" : NetworkDiagnosticPresentation.countryName(country, context: context)
    return ([ip, name] + fields.dropFirst()).filter { !$0.isEmpty }.joined(separator: " · ")
  }

  private static func exitParts(_ value: String) -> (ip: String, location: String?) {
    let parts = value.components(separatedBy: " · ")
    return (parts[0], parts.dropFirst().first)
  }
}
