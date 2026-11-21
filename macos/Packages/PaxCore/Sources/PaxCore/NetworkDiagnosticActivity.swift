import Foundation

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
      let partial =
        report?.probes.contains { !$0.errorCode.isEmpty } == true
        || report?.providers?.contains {
          !["observed", "not_indexed", "unsupported"].contains($0.state)
        } == true
        || !(report?.providerError.isEmpty ?? true)
      return .init(
        name: name,
        detail: context.text(
          "diagnostic_activity.02126cbf79", ["arg0": String(describing: report?.probes.count ?? 0)])
          + (partial ? context.text("diagnostic_activity.ed079e5a98") : ""),
        state: partial ? .partial : .finished)
    case .stopped:
      return .init(
        name: name, detail: context.text("diagnostic_activity.a639236da2"), state: .stopped)
    case .failed: return .init(name: name, detail: message(context: context), state: .failed)
    }
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
      let partial =
        !sample.dnsError.isEmpty || !sample.ipError.isEmpty
        || sample.checks?.contains {
          ["failed", "timeout", "unsupported"].contains($0.state)
            && !(ipv6Off && $0.kind == "ipv6")
        } == true
      return .init(
        name: name,
        detail: partial
          ? context.text("diagnostic_activity.0f9c97f0f0")
          : context.text("diagnostic_activity.b1e78c6c65"),
        state: partial ? .partial : .finished)
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
        detail: context.text("diagnostic_activity.af9e03e4b4") + message(context: context),
        state: .failed
      )
    }
  }
}
