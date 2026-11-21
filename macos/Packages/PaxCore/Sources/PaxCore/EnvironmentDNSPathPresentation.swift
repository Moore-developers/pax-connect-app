import Foundation
import PaxClient

extension EnvironmentDiagnosticPresentation {
  private struct DNSCoverage: Decodable {
    let expected, observed, unverified: Int
  }
  private struct DNSUpstream: Decodable {
    let server, outbound, pool: String
    let route: String?
  }

  private static func dnsPathState(_ sample: EnvironmentDiagnosticReport.Sample) -> String {
    guard let check = sample.checks?.first(where: { $0.kind == "dns_path" }),
      let coverage = try? JSONDecoder().decode(DNSCoverage.self, from: Data(check.value.utf8)),
      coverage.expected == 12, (0...12).contains(coverage.observed), coverage.unverified >= 0
    else { return "unknown" }
    if check.state == "bypass" { return "bypass" }
    guard coverage.observed == 12, coverage.unverified == 0,
      ["proxy", "policy_direct", "mixed"].contains(check.state)
    else { return "unknown" }
    return check.state
  }

  /// Only exact-query transport observations can confirm a path; countries cannot.
  public static func dnsPathAssessment(
    _ report: EnvironmentDiagnosticReport, waiting: Bool, context: PresentationContext = .chinese
  )
    -> NetworkDiagnosticPresentation.Assessment
  {
    let mac = dnsPathState(report.mac)
    let browser = dnsPathState(report.browser)
    if [mac, browser].contains("bypass") {
      return .init(text: context.text("environment_evidence.fb1a0abba6"), tone: .difference)
    }
    if mac == "proxy" && browser == "proxy" {
      return .init(text: context.text("environment_evidence.1493c24466"), tone: .matching)
    }
    if mac != "unknown" && browser != "unknown" {
      return .init(
        text: mac == "policy_direct" && browser == "policy_direct"
          ? context.text("environment_evidence.aa49876090")
          : context.text("environment_evidence.fd2c877e91"), tone: .information)
    }
    if mac == "proxy" || browser == "proxy" {
      return .init(
        text: context.text("environment_evidence.eae88c7670")
          + (mac == "proxy" ? context.text("environment_evidence.e19c3b9e26") : "Mac")
          + context.text("environment_evidence.8e50c04ce9"),
        tone: .pending)
    }
    return .init(
      text: waiting
        ? context.text("environment_evidence.0d092c6a3d")
        : context.text("environment_evidence.19c14c203e"),
      tone: .pending)
  }

  public static func dnsPathDetails(
    _ sample: EnvironmentDiagnosticReport.Sample, name: String,
    context: PresentationContext = .chinese
  )
    -> String
  {
    var lines = [name + context.text("environment_evidence.c1cd782403")]
    if let check = sample.checks?.first(where: { $0.kind == "dns_path" }),
      let coverage = try? JSONDecoder().decode(DNSCoverage.self, from: Data(check.value.utf8)),
      coverage.expected == 12, (0...12).contains(coverage.observed), coverage.unverified >= 0
    {
      let state: String
      switch dnsPathState(sample) {
      case "proxy": state = context.text("environment_evidence.ac8b76972a")
      case "policy_direct": state = context.text("environment_evidence.baba0195ed")
      case "mixed": state = context.text("environment_evidence.4c1fd53512")
      case "bypass": state = context.text("environment_evidence.e8d903bc08")
      default: state = context.text("environment_evidence.8e50c04ce9")
      }
      lines.append(
        state
          + context.text(
            "environment_evidence.ea8ebc8528", ["arg0": String(describing: coverage.observed)]))
      if coverage.unverified > 0 {
        lines.append(
          context.text(
            "environment_evidence.1c4ab9aa3c", ["arg0": String(describing: coverage.unverified)]))
      }
      if check.state == "unavailable" {
        lines.append(context.text("environment_evidence.453ef833f2"))
      }
    } else {
      lines.append(context.text("environment_evidence.e68b61a00f"))
    }
    for check in sample.checks ?? [] where check.kind == "dns_upstream" {
      guard let upstream = try? JSONDecoder().decode(DNSUpstream.self, from: Data(check.value.utf8))
      else { continue }
      let direction: String
      switch check.state {
      case "proxy": direction = context.text("environment_evidence.c49b87cc96")
      case "remote_proxy":
        let route =
          upstream.route.flatMap { $0.isEmpty ? nil : $0 }
          ?? context.text("environment_evidence.3306bcb123")
        lines.append(
          context.text("environment_evidence.ae619ec022") + route
            + context.text("environment_evidence.e259aa6639"))
        continue
      case "policy_direct": direction = context.text("environment_evidence.7108b256f4")
      case "bypass": direction = context.text("environment_evidence.52e1d924db")
      default: continue
      }
      lines.append(
        direction + " · "
          + (upstream.pool == "domestic"
            ? context.text("environment_evidence.cec69ce036")
            : context.text("environment_evidence.0f5759bd0e"))
          + context.text("environment_evidence.c091798197")
          + upstream.server + context.text("environment_evidence.0868a21e0e") + upstream.outbound)
    }
    lines.append(context.text("environment_evidence.640c70550a"))
    return lines.joined(separator: "\n")
  }
}
