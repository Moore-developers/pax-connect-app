import Foundation
import Network
import PaxClient

extension EnvironmentDiagnosticPresentation {
  public enum ClaudeCheck: String, CaseIterable {
    case exit = "echo_claude"
    case status = "claude_status"
    case apiExit = "echo_claude_api"
    case apiStatus = "claude_api_status"
    case edge = "claude_edge"
    case connection = "claude_protocol"

    public var titleKey: String {
      switch self {
      case .exit: "diagnostic.claude.exit"
      case .status: "diagnostic.claude.status"
      case .apiExit: "diagnostic.claude.api_exit"
      case .apiStatus: "diagnostic.claude.api_status"
      case .edge: "diagnostic.claude.edge"
      case .connection: "diagnostic.claude.protocol"
      }
    }
  }

  @MainActor public static func claudeReading(
    _ kind: ClaudeCheck, sample: EnvironmentDiagnosticReport.Sample?,
    context: PresentationContext = .chinese
  ) -> (text: String, detail: String, tone: Tone) {
    let check = sample?.checks?.first { $0.kind == kind.rawValue }
    guard let check, check.state == "observed" else {
      return unavailableClaudeCheck(check, context)
    }
    switch kind {
    case .exit, .apiExit:
      return claudeExit(check, sample: sample, context: context)
    case .status, .apiStatus:
      let status = context.text("browser.claude_status_" + check.value)
      return (
        status, status + " · " + MeasurementTimestamp.text(check.at),
        check.value == "operational" ? .matching : .difference
      )
    case .edge, .connection:
      let scope = context.text(
        kind == .edge ? "diagnostic.claude.edge_scope" : "diagnostic.claude.protocol_scope")
      let legacyTLS =
        kind == .connection
        && (check.value.hasSuffix("TLS 1.0") || check.value.hasSuffix("TLS 1.1"))
      let label = context.text(
        legacyTLS ? "diagnostic.claude.legacy_tls" : "diagnostic.claude.normal")
      return (
        label + " · " + check.value, checkText(check, context: context) + "\n" + scope,
        legacyTLS ? .difference : .matching
      )
    }
  }

  @MainActor public static func claudeKeychainReading(
    _ reading: ClaudeKeychainReading?, checking: Bool, context: PresentationContext = .chinese
  ) -> (text: String, detail: String, tone: Tone) {
    guard let reading, !checking else {
      return (
        context.text(
          checking ? "browser.check_checking" : "diagnostic.claude.keychain_not_checked"),
        "", .pending
      )
    }
    var details = [
      context.text("diagnostic.claude.keychain_scope"),
      MeasurementTimestamp.text(reading.at),
    ]
    let summary: String
    let tone: Tone
    switch reading.outcome {
    case .checked(let doctorProbeCount, let defaultCount, let otherCount):
      summary = context.text(
        "diagnostic.claude.doctor_count", ["count": String(doctorProbeCount)])
      details.insert("Claude Code-doctor-probe · " + summary, at: 0)
      details.insert(
        context.text(
          "diagnostic.claude.keychain_counts",
          ["standard": String(defaultCount), "other": String(otherCount)]), at: 1)
      tone = doctorProbeCount == 0 ? .matching : .failure
    case .restricted:
      summary = context.text("diagnostic.claude.keychain_restricted")
      tone = .pending
    case .unavailable:
      summary = context.text("diagnostic.claude.keychain_unavailable")
      tone = .pending
    }
    return (summary, details.joined(separator: "\n"), tone)
  }

  @MainActor private static func claudeExit(
    _ check: EnvironmentDiagnosticReport.Check, sample: EnvironmentDiagnosticReport.Sample?,
    context: PresentationContext
  ) -> (text: String, detail: String, tone: Tone) {
    let parts = check.value.components(separatedBy: " · ")
    let address = parts[0]
    let browserAddress: String?
    let same: Bool
    if let ip = IPv4Address(address), let browser = sample.flatMap({ IPv4Address($0.ipv4) }) {
      browserAddress = browser.debugDescription
      same = ip == browser
    } else if let ip = IPv6Address(address), let browser = sample.flatMap({ IPv6Address($0.ipv6) })
    {
      browserAddress = browser.debugDescription
      same = ip == browser
    } else {
      browserAddress = nil
      same = false
    }
    let comparison = browserAddress == nil ? "uncompared" : same ? "same" : "different"
    let country = parts.dropFirst().first.map {
      NetworkDiagnosticPresentation.countryName($0, context: context)
    }
    let text =
      (country.map { $0 + " · " } ?? "") + address
      + " · " + context.text("diagnostic.claude." + comparison)
    let detail = [
      checkText(check, context: context),
      browserAddress.map { context.text("browser.check_ip") + " · " + $0 },
    ].compactMap { $0 }.joined(separator: "\n")
    return (text, detail, browserAddress == nil ? .pending : same ? .matching : .difference)
  }

  @MainActor private static func unavailableClaudeCheck(
    _ check: EnvironmentDiagnosticReport.Check?, _ context: PresentationContext
  ) -> (text: String, detail: String, tone: Tone) {
    let key: String
    if check?.state == "timeout" {
      key = "browser.check_timeout"
    } else if check?.errorCode == "probe_response_invalid", check?.httpStatus == 200,
      ["claude_edge", "claude_protocol"].contains(check?.kind ?? "")
    {
      key = "diagnostic.claude.field_unconfirmed"
    } else {
      key = "browser.check_unavailable"
    }
    return (context.text(key), check.map { checkText($0, context: context) } ?? "", .pending)
  }
}
