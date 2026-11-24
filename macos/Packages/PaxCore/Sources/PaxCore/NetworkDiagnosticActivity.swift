import Foundation
import PaxClient

public enum NetworkDiagnosticRunState: Sendable { case idle, running, finished, stopped, failed }

/// Live foreground activity is separate from retained diagnostic evidence.
public struct NetworkDiagnosticActivity {
  public enum State { case idle, running, finished, partial, stopped, failed }
  public let name, detail: String
  public let state: State
  public var settled: Bool { state != .idle && state != .running }
}

extension NetworkDiagnosticStore {
  public var activities: [NetworkDiagnosticActivity] { activities(context: .chinese) }
  public func activities(context: PresentationContext) -> [NetworkDiagnosticActivity] {
    var values = [routeActivity(context: context)]
    if let currentEnvironment {
      values.append(currentEnvironment.activity(browser: false, context: context))
      values.append(currentEnvironment.activity(browser: true, context: context))
    }
    return values
  }

  private func routeActivity(context: PresentationContext) -> NetworkDiagnosticActivity {
    let name = context.text("diagnostic_activity.38c1a91549")
    switch runState {
    case .idle:
      return .init(name: name, detail: context.text("diagnostic_activity.541e655330"), state: .idle)
    case .running:
      var parts = [String]()
      if let progress {
        if progress.probesTotal > 0 {
          parts.append(
            context.text(
              "diagnostic_activity.57517eb59f",
              [
                "arg0": String(describing: progress.probesCompleted),
                "arg1": String(describing: progress.probesTotal),
              ]))
        }
        parts.append(
          Self.phase(
            progress.exitState, title: context.text("diagnostic_activity.7016090059"),
            context: context))
        parts.append(
          Self.phase(
            progress.sourcesState, title: context.text("diagnostic_activity.df08456568"),
            context: context))
      } else {
        parts.append(context.text("diagnostic_activity.e8c43fc5e1"))
      }
      if let progressError = progressError(context: context) { parts.append(progressError) }
      return .init(name: name, detail: parts.joined(separator: " · "), state: .running)
    case .finished:
      let issues = Self.routeIssues(report, context: context)
      let completed = context.text(
        "diagnostic_activity.02126cbf79", ["arg0": String(describing: report?.probes.count ?? 0)])
      return .init(
        name: name, detail: ([completed] + issues).joined(separator: " · "),
        state: issues.isEmpty ? .finished : .partial)
    case .stopped:
      return .init(
        name: name, detail: context.text("diagnostic_activity.a639236da2"), state: .stopped)
    case .failed: return .init(name: name, detail: message(context: context), state: .failed)
    }
  }

  static func routeIssues(_ report: NetworkDiagnosticReport?, context: PresentationContext)
    -> [String]
  {
    guard let report else { return [] }
    var issues: [String] = []
    for probe in report.probes where !probe.errorCode.isEmpty {
      guard let host = URL(string: probe.target)?.host else { continue }
      if probe.httpStatus == 401 && ["api.openai.com", "api.anthropic.com"].contains(host) {
        continue
      }
      let reason =
        probe.httpStatus > 0
        ? "HTTP \(probe.httpStatus)"
        : NetworkDiagnosticPresentation.problem(probe.errorCode, context: context)
      let issue = host + " · " + reason
      if !issues.contains(issue) { issues.append(issue) }
    }
    for provider in report.providers ?? []
    where !["observed", "not_indexed", "unsupported"].contains(provider.state) {
      issues.append(
        provider.id + " · "
          + NetworkDiagnosticPresentation.providerState(provider, context: context))
    }
    if !report.exitError.isEmpty {
      issues.append(
        context.text("diagnostic_activity.7016090059") + " · "
          + NetworkDiagnosticPresentation.problem(report.exitError, context: context))
    }
    if !report.providerError.isEmpty && report.providers?.isEmpty != false
      && report.exitError.isEmpty
    {
      issues.append(
        context.text("diagnostic_activity.df08456568") + " · "
          + NetworkDiagnosticPresentation.problem(report.providerError, context: context))
    }
    return issues
  }

  private static func phase(_ state: String, title: String, context: PresentationContext) -> String
  {
    switch state {
    case "running": return title + context.text("diagnostic_activity.65f9322365")
    case "complete": return title + context.text("diagnostic_activity.4be334f6b7")
    case "failed": return title + context.text("diagnostic_activity.bf4cedf76a")
    case "skipped": return title + context.text("diagnostic_activity.231fe22042")
    default:
      return context.text("diagnostic_activity.251bc40e70") + title
        + context.text("diagnostic_activity.bcd6771e08")
    }
  }
}

extension EnvironmentDiagnosticStore {
  public func activity(browser: Bool, context: PresentationContext = .chinese)
    -> NetworkDiagnosticActivity
  {
    let name =
      browser
      ? context.text("diagnostic_activity.3a6c1f7cd5")
      : context.text("diagnostic_activity.1482453530")
    let sample = browser ? progressReport?.browser : progressReport?.mac
    if let sample, sample.complete {
      let ipv6Off = EnvironmentDiagnosticPresentation.ipv6Exposure(progressReport) == .off
      let issues = Self.sampleIssues(sample, ipv6Off: ipv6Off, context: context)
      return .init(
        name: name,
        detail: issues.isEmpty
          ? context.text("diagnostic_activity.b1e78c6c65") : issues.joined(separator: " / "),
        state: issues.isEmpty ? .finished : .partial)
    }
    switch runState {
    case .idle:
      return .init(name: name, detail: context.text("diagnostic_activity.541e655330"), state: .idle)
    case .running:
      let sampled = !(sample?.checks?.isEmpty ?? true)
      return .init(
        name: name,
        detail: sampled
          ? context.text("diagnostic_activity.94b236857e")
          : browser
            ? context.text("diagnostic_activity.d2c1cac7f4")
            : context.text("diagnostic_activity.df30b8fed4"),
        state: .running)
    case .stopped:
      return .init(
        name: name, detail: context.text("diagnostic_activity.a639236da2"), state: .stopped)
    case .finished, .failed:
      return .init(
        name: name,
        detail: message(context: context),
        state: .failed
      )
    }
  }

  static func sampleIssues(
    _ sample: EnvironmentDiagnosticReport.Sample, ipv6Off: Bool, context: PresentationContext
  ) -> [String] {
    var issues: [String] = []
    let checks = sample.checks ?? []
    let exit = EnvironmentDiagnosticPresentation.exitAssessment(checks, context: context)
    if exit.tone == .difference { issues.append(exit.text) }
    let failures = checks.filter {
      ["failed", "timeout", "unsupported"].contains($0.state) && !(ipv6Off && $0.kind == "ipv6")
    }
    for check in failures {
      let name: String
      switch check.kind {
      case "ipv4": name = "IPv4"
      case "ipv6": name = "IPv6"
      case "dns_session", "dns_query", "dns_result": name = "DNS"
      case "rtc_cf", "rtc_google", "rtc_stun": name = "WebRTC"
      case "intelligence": name = context.text("diagnostic_activity.df08456568")
      default:
        name =
          EnvironmentDiagnosticPresentation.ClaudeCheck(rawValue: check.kind).map {
            context.text($0.titleKey)
          } ?? check.target
      }
      let reason: String
      switch check.state {
      case "timeout":
        reason = context.text(
          name == "WebRTC" ? "browser.check_rtc_timeout" : "browser.check_timeout")
      case "unsupported":
        reason = context.text("browser.check_unsupported")
      default:
        reason = EnvironmentDiagnosticPresentation.checkProblem(check.errorCode, context: context)
      }
      let issue = name + " · " + reason
      if !issues.contains(issue) { issues.append(issue) }
    }
    if !sample.ipError.isEmpty
      && !failures.contains(where: { $0.kind == "ipv4" || $0.kind == "ipv6" })
    {
      issues.append(context.text("browser.check_ip") + " · " + context.text("browser.check_failed"))
    }
    if !sample.dnsError.isEmpty && !failures.contains(where: { $0.kind.hasPrefix("dns_") }) {
      issues.append(
        "DNS · " + EnvironmentDiagnosticPresentation.checkProblem(sample.dnsError, context: context)
      )
    }
    for status in checks
    where ["claude_status", "claude_api_status"].contains(status.kind) && status.state == "observed"
      && status.value != "operational"
    {
      if let kind = EnvironmentDiagnosticPresentation.ClaudeCheck(rawValue: status.kind) {
        issues.append(
          context.text(kind.titleKey) + " · "
            + context.text("browser.claude_status_" + status.value))
      }
    }
    return issues
  }

}
