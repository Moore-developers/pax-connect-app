import AppKit
import PaxCore
import PaxUI

extension NetworkDiagnosticView {
  static let diagnosticServiceTargets = [
    ("ChatGPT", "chatgpt.com"), ("OpenAI API", "api.openai.com"),
    ("Claude", "claude.ai"), ("Anthropic API", "api.anthropic.com"),
    ("Grok", "grok.com"), ("Perplexity", "www.perplexity.ai"),
    ("Gemini", "gemini.google.com"), ("TikTok", "www.tiktok.com"),
    ("Facebook", "www.facebook.com"), ("X", "x.com"),
    ("Steam 商店", "store.steampowered.com"), ("GitHub", "github.com"),
  ]

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
    let run = action("重测公开端点") { [weak self] in self?.store.start(mode: "services") }
    run.isEnabled = !store.busy && store.currentEnvironment?.busy != true
    run.setAccessibilityIdentifier("diagnostic-services-run")
    let targets = Self.diagnosticServiceTargets
    var rows: [NSView] = []
    for (name, host) in targets {
      if !rows.isEmpty { rows.append(divider()) }
      let sample = serviceSample(host)
      let judgment = P.serviceAssessment(sample?.0, api: host.hasPrefix("api."))
      var detail = "目标：" + host + "\n地区、账号、模型权限未验证。"
      if let (probe, owner) = sample {
        detail +=
          "\n" + P.probeResult(probe) + "\n检测于 " + MeasurementTimestamp.text(probe.at ?? owner.at)
        if let previous = store.previousReport,
          NetworkDiagnosticStore.sameBoundary(owner, previous),
          let ip = owner.exit?.ip, !ip.isEmpty, ip == previous.exit?.ip, probe.httpStatus == 0,
          let old = previous.probes.last(where: {
            URL(string: $0.target)?.host == host && $0.httpStatus > 0
          })
        {
          detail += "\n上次响应：" + P.probeResult(old) + " · " + MeasurementTimestamp.text(old.at)
        }
      }
      rows.append(
        diagnosticRow(
          title: name,
          subtitle: judgment.text.replacingOccurrences(of: "，账号未验", with: "").replacingOccurrences(
            of: "，权限未验", with: ""),
          symbol: "network", detail: detail, tone: judgment.tone))
    }
    let list = vertical(rows, spacing: 0)
    list.setAccessibilityIdentifier("diagnostic-services")
    let samples = targets.compactMap { serviceSample($0.1)?.0 }
    let overview = P.serviceOverview(samples)
    return vertical([
      horizontal([heading("适合做什么", cardTitle: true), NSView(), run]),
      text(serviceContext(), secondary: true),
      text("匿名端点检测；账号、地区及模型权限未验证。", secondary: true),
      assessmentText(overview), list,
    ])
  }
  private func serviceContext() -> String {
    let results = Self.diagnosticServiceTargets.compactMap { serviceSample($0.1) }
    let times = results.compactMap { $0.0.at ?? $0.1.at }
    guard let first = times.min(), let last = times.max() else { return "尚无检测记录" }
    let history =
      store.stale || results.contains { $0.1.at != report?.at || $0.1.mode == "sources" }
    return (history ? "含上次结果 · " : "检测于 ") + MeasurementTimestamp.text(last)
      + (last.timeIntervalSince(first) > 60 ? "；各项时间见详情" : "")
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
        value = "出口 " + check.value
      } else {
        value = check.map { E.checkProblem($0.errorCode) } ?? "未采集，无法判断出口"
      }
      rows.append(
        diagnosticRow(
          title: title, subtitle: value, symbol: "globe",
          detail: check.map(E.checkText) ?? "旧记录未采集",
          tone: check?.state == "observed" ? .information : .pending))
    }
    let list = vertical(rows, spacing: 0)
    list.setAccessibilityIdentifier("diagnostic-browser-exits")
    return vertical([
      heading("浏览器与出口", cardTitle: true),
      text(
        (environment?.stale == true ? "上次检测 · " : "")
          + MeasurementTimestamp.text(environment?.report?.at) + "；仅代表这些目标，不代表全部流量。",
        secondary: true),
      assessmentText(E.exitAssessment(checks)), list,
    ])
  }
}
