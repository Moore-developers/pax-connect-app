import AppKit
import PaxCore
import PaxUI

extension NetworkDiagnosticView {
  func claudeContent() -> NSView {
    let environment = store.currentEnvironment
    let sample = environment?.report?.browser
    let checking =
      environment?.busy == true && (environment?.stale == true || sample?.complete != true)
    let region = action(context.text("diagnostic.claude.regions")) {
      if let url = URL(string: "https://www.anthropic.com/legal/supported-regions") {
        NSWorkspace.shared.open(url)
      }
    }
    region.setAccessibilityIdentifier("diagnostic-claude-regions")
    var rows: [NSView] = [
      heading(
        context.text(
          "diagnostic.overview.browser_group", ["count": String(E.ClaudeCheck.allCases.count)]))
    ]
    for check in E.ClaudeCheck.allCases {
      let reading = E.claudeReading(check, sample: checking ? nil : sample, context: context)
      let observed =
        checking
        ? nil : sample?.checks?.first { $0.kind == check.rawValue && $0.state == "observed" }
      let parts = reading.text.components(separatedBy: " · ")
      let isExit = check == .exit || check == .apiExit
      let exitVerdict = isExit && observed != nil ? parts.last : nil
      let evidence: String?
      if isExit, observed != nil {
        evidence = parts.dropLast().joined(separator: " · ")
      } else if check == .status || check == .apiStatus {
        evidence = observed.map { $0.target + " · " + $0.value }
      } else {
        evidence = nil
      }
      rows.append(
        diagnosticRow(
          title: context.text(check.titleKey), kind: check.rawValue,
          subtitle: checking ? context.text("browser.check_checking") : reading.text,
          symbol: symbol(for: check),
          detail: reading.detail, tone: reading.tone,
          verdict: exitVerdict, basis: evidence, detailAction: check == .exit ? region : nil))
    }
    let keychain = E.claudeKeychainReading(
      environment?.claudeKeychain, checking: environment?.checkingClaudeKeychain == true,
      context: context)
    rows.append(heading(context.text("diagnostic.overview.local_group")))
    rows.append(
      diagnosticRow(
        title: context.text("diagnostic.claude.keychain"), kind: "claude-keychain",
        subtitle: keychain.text, symbol: "key", detail: keychain.detail, tone: keychain.tone,
        basis: environment?.claudeKeychain.map { _ in
          context.text("diagnostic.overview.keychain_source")
        }))
    let list = diagnosticTable(rows)
    list.setAccessibilityIdentifier("diagnostic-claude")
    return vertical([
      cardHeading(
        "diagnostic.claude.title", symbol: "network", count: E.ClaudeCheck.allCases.count + 1),
      list,
    ])
  }

  private func symbol(for check: E.ClaudeCheck) -> String {
    switch check {
    case .exit, .apiExit: "globe"
    case .status, .apiStatus: "waveform.path.ecg"
    case .edge: "mappin.and.ellipse"
    case .connection: "lock"
    }
  }

  func pathsContent() -> NSView {
    let environment = store.currentEnvironment
    let checks = environment?.report?.browser.checks ?? []
    let assessment = E.exitAssessment(checks, context: context)
    var rows: [NSView] = []
    var unavailable: [String] = []
    for (title, kind) in [
      ("IPIP", "echo_ipip"), ("IPinfo", "echo_ipinfo"), ("Cloudflare", "echo_cf"),
    ] {
      let check = checks.first { $0.kind == kind }
      let mismatch = assessment.tone == .difference && kind != "echo_cf"
      let value: String
      if let check, check.state == "observed" {
        value =
          context.text("native_diagnostic.769aebde14") + E.exitReading(check, context: context)
      } else {
        if check != nil { unavailable.append(title) }
        value =
          check.map { E.checkProblem($0.errorCode, context: context) }
          ?? context.text("native_diagnostic.bec9167b59")
      }
      rows.append(
        diagnosticRow(
          title: title, kind: kind, subtitle: value, symbol: "globe",
          detail: check.map { E.checkText($0, context: context) }
            ?? context.text("native_diagnostic.883b0ebcba"),
          tone: mismatch ? .difference : check?.state == "observed" ? .information : .pending,
          verdict: context.text(
            mismatch
              ? "browser.check_exit_abnormal"
              : check?.state == "observed"
                ? "browser.check_observed" : "diagnostic_evidence.c098e854e6"), basis: value))
    }
    let list = diagnosticTable(rows)
    list.setAccessibilityIdentifier("diagnostic-browser-exits")
    let metadata =
      environment?.report.map {
        [
          browserName,
          environment?.stale == true ? context.text("native_diagnostic.f48ea0733c") : "",
          MeasurementTimestamp.text($0.at),
        ].filter { !$0.isEmpty }.joined(separator: " · ")
      } ?? ""
    var finding = assessment.text
    if !unavailable.isEmpty {
      let failure = context.text(
        "diagnostic.overview.browser_unavailable", ["targets": unavailable.joined(separator: ", ")])
      finding = assessment.tone == .difference ? finding + " · " + failure : failure
    }
    let summary = topicSummary(
      finding, tone: unavailable.isEmpty ? assessment.tone : .difference, metadata: metadata,
      note: context.text("diagnostic.overview.browser_scope"))
    return vertical([summary, list])
  }
}
