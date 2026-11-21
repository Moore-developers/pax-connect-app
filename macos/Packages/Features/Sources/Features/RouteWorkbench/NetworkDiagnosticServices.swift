import AppKit
import PaxCore
import PaxUI

extension NetworkDiagnosticView {
  func diagnosticServiceTargets() -> [(String, String)] {
    [
      ("ChatGPT", "chatgpt.com"), ("OpenAI API", "api.openai.com"),
      ("Claude", "claude.ai"), ("Anthropic API", "api.anthropic.com"),
      ("Grok", "grok.com"), ("Perplexity", "www.perplexity.ai"),
      ("Gemini", "gemini.google.com"), ("TikTok", "www.tiktok.com"),
      ("Facebook", "www.facebook.com"), ("X", "x.com"),
      (context.text("native_diagnostic.ea1fd2165c"), "store.steampowered.com"),
      ("GitHub", "github.com"),
    ]
  }

  func serviceSample(_ host: String) -> (NetworkDiagnosticReport.Probe, NetworkDiagnosticReport)? {
    let reports = [report, store.services].compactMap { $0 }.filter { candidate in
      report == nil
        || report.map { $0 == candidate || NetworkDiagnosticStore.sameBoundary($0, candidate) }
          == true
    }
    return reports.flatMap { candidate in
      candidate.probes.filter { URL(string: $0.target)?.host == host }.map { ($0, candidate) }
    }.max(by: { ($0.0.at ?? $0.1.at) < ($1.0.at ?? $1.1.at) })
  }
  func servicesContent() -> NSView {
    let run = action(context.text("native_diagnostic.935d29270f")) { [weak self] in
      self?.store.start(mode: "services")
    }
    run.isEnabled = !store.busy && store.currentEnvironment?.busy != true
    run.setAccessibilityIdentifier("diagnostic-services-run")
    let targets = diagnosticServiceTargets()
    var rows: [NSView] = []
    for (name, host) in targets {
      if !rows.isEmpty { rows.append(divider()) }
      let sample = serviceSample(host)
      let judgment = P.serviceAssessment(sample?.0, api: host.hasPrefix("api."), context: context)
      var detail =
        context.text("native_diagnostic.4d2d25a288") + host
        + context.text("native_diagnostic.eaaab2ef23")
      if let (probe, owner) = sample {
        detail +=
          "\n" + P.probeResult(probe, context: context)
          + context.text("native_diagnostic.a3d09d2e9e")
          + MeasurementTimestamp.text(probe.at ?? owner.at)
        if let previous = store.previousReport,
          NetworkDiagnosticStore.sameBoundary(owner, previous),
          let ip = owner.exit?.ip, !ip.isEmpty, ip == previous.exit?.ip, probe.httpStatus == 0,
          let old = previous.probes.last(where: {
            URL(string: $0.target)?.host == host && $0.httpStatus > 0
          })
        {
          detail +=
            context.text("native_diagnostic.322828a78a") + P.probeResult(old, context: context)
            + " · " + MeasurementTimestamp.text(old.at)
        }
      }
      rows.append(
        diagnosticRow(
          title: name,
          subtitle: judgment.text.replacingOccurrences(
            of: context.text("native_diagnostic.577e0915d9"), with: ""
          ).replacingOccurrences(
            of: context.text("native_diagnostic.beeec8a46a"), with: ""),
          symbol: "network", detail: detail, tone: judgment.tone))
    }
    let list = vertical(rows, spacing: 0)
    list.setAccessibilityIdentifier("diagnostic-services")
    let samples = targets.compactMap { serviceSample($0.1)?.0 }
    let overview = P.serviceOverview(samples, context: context)
    return vertical([
      horizontal([
        heading(context.text("native_diagnostic.8cc881f033"), cardTitle: true), NSView(), run,
      ]),
      text(serviceContext(), secondary: true),
      text(context.text("native_diagnostic.513b5a115e"), secondary: true),
      assessmentText(overview), list,
    ])
  }
  private func serviceContext() -> String {
    let results = diagnosticServiceTargets().compactMap { serviceSample($0.1) }
    let times = results.compactMap { $0.0.at ?? $0.1.at }
    guard let first = times.min(), let last = times.max() else {
      return context.text("native_diagnostic.214836ed73")
    }
    let history =
      store.stale || results.contains { $0.1.at != report?.at || $0.1.mode == "sources" }
    return
      (history
      ? context.text("native_diagnostic.b2d9af2723") : context.text("native_diagnostic.ecc1fb185b"))
      + MeasurementTimestamp.text(last)
      + (last.timeIntervalSince(first) > 60 ? context.text("native_diagnostic.1e817d7cd8") : "")
  }

  func pathsContent() -> NSView {
    let environment = store.currentEnvironment
    let checks = environment?.report?.browser.checks ?? []
    var rows: [NSView] = []
    for (title, kind) in [
      ("IPIP", "echo_ipip"), ("IPinfo", "echo_ipinfo"), ("Cloudflare", "echo_cf"),
    ] {
      if !rows.isEmpty { rows.append(divider()) }
      let check = checks.first { $0.kind == kind }
      let value: String
      if let check, check.state == "observed" {
        value = context.text("native_diagnostic.769aebde14") + check.value
      } else {
        value =
          check.map { E.checkProblem($0.errorCode, context: context) }
          ?? context.text("native_diagnostic.bec9167b59")
      }
      rows.append(
        diagnosticRow(
          title: title, subtitle: value, symbol: "globe",
          detail: check.map { E.checkText($0) } ?? context.text("native_diagnostic.883b0ebcba"),
          tone: check?.state == "observed" ? .information : .pending))
    }
    let list = vertical(rows, spacing: 0)
    list.setAccessibilityIdentifier("diagnostic-browser-exits")
    return vertical([
      heading(context.text("native_diagnostic.ee42d43fd2"), cardTitle: true),
      text(
        (environment?.stale == true ? context.text("native_diagnostic.55509b5207") : "")
          + MeasurementTimestamp.text(environment?.report?.at)
          + context.text("native_diagnostic.8adf0da57a"),
        secondary: true),
      assessmentText(E.exitAssessment(checks, context: context)), list,
    ])
  }
}
