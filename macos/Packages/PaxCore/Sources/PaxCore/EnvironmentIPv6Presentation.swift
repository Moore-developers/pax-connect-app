import Foundation
import PaxClient

extension EnvironmentDiagnosticPresentation {
  public static let ipv6Title = "IPv6"

  public enum IPv6Exposure: Sendable { case on, off, unreadable, old }

  /// Judged only from the Mac's existing `ipv6_route` evidence; the browser has no switch of its own.
  public static func ipv6Exposure(_ report: EnvironmentDiagnosticReport?) -> IPv6Exposure? {
    guard let checks = report?.mac.checks else { return report == nil ? nil : .old }
    switch checks.first(where: { $0.kind == "ipv6_route" })?.state {
    case "observed": return .on
    case "absent": return .off
    default: return .unreadable
    }
  }

  private static func ipv6Path(context: PresentationContext) -> String {
    context.text("environment_evidence.832ecc9f29")
  }
  private static func ipv6Boundary(context: PresentationContext) -> String {
    context.text("environment_evidence.e21b2c8e18")
  }

  /// One row after WebRTC. Report text carries states only, never addresses.
  static func ipv6Row(
    _ report: EnvironmentDiagnosticReport?, waiting: Bool, context: PresentationContext = .chinese
  ) -> Row {
    func row(_ value: String, _ detail: String, _ tone: Tone) -> Row {
      Row(
        title: ipv6Title, symbol: "network", value: value, detail: detail, tone: tone,
        context: context)
    }
    guard let report else {
      return row(
        waiting
          ? context.text("environment_evidence.e8b970bc3e")
          : context.text("environment_evidence.4d2966c438"), "", .pending)
    }
    switch ipv6Exposure(report) {
    case .on:
      let interface = report.mac.checks?.first { $0.kind == "ipv6_route" }?.value ?? ""
      var lines = [
        context.text("environment_evidence.5de1039bfd")
          + (interface.isEmpty ? "" : "（" + interface + "）")
      ]
      for (name, sample) in [
        ("Mac", report.mac), (context.text("environment_evidence.e19c3b9e26"), report.browser),
      ] {
        let check = sample.checks?.first { $0.kind == "ipv6" && $0.state == "observed" }
        lines.append(
          name + context.text("environment_evidence.395529f1a4")
            + (check.map {
              $0.value
                + ($0.at.map { context.text("environment_evidence.e026a44250") + timeText($0) }
                  ?? "")
            } ?? context.text("environment_evidence.e7954a2f3a")))
      }
      lines += [
        context.text("environment_evidence.d9f30e0981") + ipv6Path(context: context),
        ipv6Boundary(context: context),
      ]
      return row(
        context.text("environment_evidence.369dc78176"), lines.joined(separator: "\n"), .difference)
    case .off:
      return row(
        context.text("environment_evidence.294c303e43"),
        context.text("environment_evidence.c284e62bda"), .information)
    case .old:
      return row(context.text("environment_evidence.883b0ebcba"), "", .pending)
    default:
      if !report.mac.complete && waiting {
        return row(context.text("environment_evidence.e8b970bc3e"), "", .pending)
      }
      return row(
        context.text("environment_evidence.eaa4783b5c"),
        context.text("environment_evidence.ef3af05793"), .pending)
    }
  }

  /// Report variant of the row's 依据.
  static func ipv6ReportDetail(
    _ report: EnvironmentDiagnosticReport?, context: PresentationContext = .chinese
  ) -> String {
    switch ipv6Exposure(report) {
    case .on: context.text("environment_evidence.c17cf86492") + ipv6Boundary(context: context)
    case .off: context.text("environment_evidence.1a9b96b560")
    case .old: context.text("environment_evidence.883b0ebcba")
    case .unreadable: context.text("environment_evidence.069afa0808")
    case nil: context.text("environment_evidence.4d2966c438")
    }
  }
}
