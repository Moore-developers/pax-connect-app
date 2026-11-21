import AppKit
import PaxCore
import PaxUI

extension NetworkDiagnosticView {
  typealias E = EnvironmentDiagnosticPresentation

  func showEnvironmentBlock() {
    append(environmentSummary())
    append(mergedEnvironmentCard())
  }

  private func environmentSummary() -> NSView {
    let environment = store.currentEnvironment
    let overview = E.overview(environment?.report, context: context)
    let intel = overview.intelligence
    let address =
      overview.address.isEmpty ? context.text("native_diagnostic.f97f6a88ec") : overview.address
    let ip = text(address)
    ip.font = PaxTheme.Typography.label(
      size: PaxTheme.NetworkDiagnostics.ipFont, weight: .medium)
    ip.isSelectable = true
    ip.maximumNumberOfLines = 1
    ip.lineBreakMode = .byTruncatingTail
    ip.toolTip = address
    let source =
      intel?.sources.first {
        !$0.org.isEmpty && (intel?.country.isEmpty == true || $0.country == intel?.country)
      }
      ?? intel?.sources.first { !$0.org.isEmpty }
    let city = source?.country == intel?.country ? source?.city ?? "" : ""
    let location =
      intel?.country.isEmpty == false
      ? [P.countryName(intel?.country ?? "", context: context), city].filter { !$0.isEmpty }.joined(
        separator: " · ")
      : (overview.address.isEmpty
        ? context.text("native_diagnostic.97840fdd43")
        : context.text("native_diagnostic.98bf0fd777"))
    let asn = source?.asn ?? ""
    let detail = [
      location, asn.isEmpty ? "" : (asn.uppercased().hasPrefix("AS") ? asn : "AS" + asn),
      source?.org ?? "",
    ].filter { !$0.isEmpty }.joined(separator: " · ")
    let counts = P.riskCounts(intel)
    let native = P.nativeAssessment(intel, context: context)
    let risk = blacklistSummary(intel)
    let type =
      intel?.ipType.isEmpty == false
      ? P.typeName(intel?.ipType ?? "", context: context)
      : context.text("native_diagnostic.ae33a0af26")
    let shortType: String
    switch intel?.ipType {
    case "hosting": shortType = context.text("native_diagnostic.3c8bfb2399")
    case "isp": shortType = context.text("native_diagnostic.572b0172bf")
    case "mobile": shortType = context.text("native_diagnostic.fc6bb436b8")
    case "business": shortType = context.text("native_diagnostic.409d071901")
    case "edu": shortType = context.text("native_diagnostic.c883995ff5")
    case nil, "": shortType = context.text("native_diagnostic.e7954a2f3a")
    default: shortType = type
    }
    let cells: [NSView] = [
      vertical(
        [
          text(
            flag(intel?.country ?? "") + context.text("native_diagnostic.4ebb761ac3"),
            secondary: true), ip,
        ],
        spacing: PaxTheme.Spacing.tight),
      metricCard(
        context.text("native_diagnostic.14ace9bd02"),
        value: intel == nil
          ? "—"
          : context.text(
            "native_diagnostic.e77ea3c4c6",
            ["arg0": String(describing: counts.hits), "arg1": String(describing: counts.unknown)]),
        color: summaryColor(counts.hits > 0 ? .difference : .pending),
        splitValue: true,
        detail: P.acquisitionSummary(intel, context: context)
          + context.text("native_diagnostic.3dec23eae6")),
      metricCard(
        context.text("native_diagnostic.33ea5ef367"), value: native.text,
        color: native.tone == .pending
          ? PaxTheme.Color.textSecondary : PaxTheme.NetworkDiagnostics.summaryAccent,
        detail: context.text("native_diagnostic.303b57d760")
          + P.providerDetails(intel, id: "ipx.al", context: context)),
      metricCard(
        context.text("native_diagnostic.ba40014ff4"), value: shortType,
        color: intel?.ipType.isEmpty == false
          ? PaxTheme.NetworkDiagnostics.summaryText : PaxTheme.Color.textSecondary,
        detail: context.text("native_diagnostic.2a6065d3d5")),
      metricCard(
        context.text("native_diagnostic.174ec5a4ab"), value: risk.text,
        color: summaryColor(risk.tone), splitValue: true, detail: risk.detail),
    ]
    var columns: [NSView] = []
    for (index, cell) in cells.enumerated() {
      if index > 0 {
        let line = NSBox()
        line.boxType = .separator
        line.widthAnchor.constraint(equalToConstant: PaxTheme.Layout.cardBorder).isActive = true
        line.heightAnchor.constraint(
          equalToConstant: PaxTheme.NetworkDiagnostics.environmentMetricHeight
        ).isActive = true
        columns.append(line)
      }
      columns.append(cell)
    }
    let row = horizontal(columns, spacing: PaxTheme.Spacing.medium)
    row.alignment = .top
    let baseWidth = cells[2].widthAnchor
    for cell in cells.dropFirst(3) {
      cell.widthAnchor.constraint(equalTo: baseWidth).isActive = true
    }
    let riskWeight: CGFloat = context.language == .english ? 1.25 : 1
    cells[1].widthAnchor.constraint(equalTo: baseWidth, multiplier: riskWeight).isActive = true
    cells[0].widthAnchor.constraint(equalTo: baseWidth, multiplier: 3 - riskWeight).isActive = true
    let result = card(row, summary: true)
    result.toolTip = detail + "\n" + overview.source + " · " + overview.comparison
    return result
  }

  private func metricCard(
    _ title: String, value: String, color: NSColor, splitValue: Bool = false, detail: String
  ) -> NSView {
    let english = context.language == .english
    let parts =
      splitValue && !english
      ? value.split(separator: " ", maxSplits: 1).map(String.init) : [value]
    let caption = text(title, secondary: true)
    let label = text(parts[0])
    label.font = PaxTheme.Typography.label(
      size: english ? PaxTheme.Routes.nameFont : PaxTheme.NetworkDiagnostics.metricFont,
      weight: .medium)
    label.textColor = color
    if english {
      for field in [caption, label] {
        field.maximumNumberOfLines = 1
        field.lineBreakMode = .byTruncatingTail
      }
    }
    let valueRow = horizontal(
      [label] + (parts.count > 1 ? [text(parts[1], secondary: true)] : []) + [NSView()],
      spacing: PaxTheme.Spacing.xs)
    let result = vertical([caption, valueRow], spacing: PaxTheme.Spacing.tight)
    result.toolTip = english ? "\(title) · \(value)\n\(detail)" : detail
    return result
  }

  private func blacklistSummary(_ intel: NetworkDiagnosticReport?) -> (
    text: String, tone: E.Tone, detail: String
  ) {
    let facts = intel?.facts.filter { $0.group == "dnsbl" } ?? []
    let hits = facts.filter { $0.state == "hit" }.count
    let checked = facts.filter { ["hit", "clear"].contains($0.state) }.count
    let unknown = facts.count - checked
    let text =
      hits > 0
      ? context.text("native_diagnostic.a2966368ac", ["arg0": String(describing: hits)])
      : checked > 0
        ? context.text("native_diagnostic.cb5cde9cad", ["arg0": String(describing: checked)])
          + (unknown > 0 ? context.text("native_diagnostic.abc328f9f4") : "")
        : (intel?.providers == nil
          ? context.text("native_diagnostic.e7954a2f3a")
          : context.text("native_diagnostic.3acaa83343"))
    return (
      text, hits > 0 ? .difference : checked > 0 ? .matching : .pending,
      intel?.providers != nil && checked == 0
        ? context.text("native_diagnostic.536dcc7411")
        : context.text("native_diagnostic.a69eb123c4", ["arg0": String(describing: checked)])
    )
  }

  private func mergedEnvironmentCard() -> NSView {
    let environment = store.currentEnvironment
    let rows = E.assessedRows(
      environment?.report, local: environment?.local, waiting: environment?.busy == true,
      context: context)
    let timestamp = text(
      (environment?.stale == true
        ? context.text("native_diagnostic.46acb06da6")
        : context.text("native_diagnostic.d3aababeca"))
        + MeasurementTimestamp.text(environment?.report?.at), secondary: true)
    timestamp.maximumNumberOfLines = 1
    let timestampRow = horizontal([timestamp])
    timestampRow.heightAnchor.constraint(
      equalToConstant:
        PaxTheme.Control.compactHeight + PaxTheme.NetworkDiagnostics.environmentRowInset * 2
    ).isActive = true
    var content: [NSView] = [timestampRow]
    for row in rows {
      if !content.isEmpty { content.append(divider()) }
      content.append(environmentRow(row))
    }
    let result = card(vertical(content, spacing: 0))
    result.setAccessibilityIdentifier("environment-merged-card")
    return result
  }

  private func environmentRow(_ row: E.Row) -> NSView {
    diagnosticRow(
      title: row.title, kind: row.kind, subtitle: row.value, symbol: row.symbol,
      detail: environmentReadingDetails(row), tone: row.tone)
  }

  private func environmentReadingDetails(_ row: E.Row) -> String {
    guard let report = store.currentEnvironment?.report else { return row.detail }
    let local = store.currentEnvironment?.local
    var lines: [String] = []
    for browser in [false, true] {
      if row.title == "WebRTC" && !browser { continue }
      let sample = browser ? report.browser : report.mac
      let name =
        browser
        ? (sample.browser.isEmpty ? context.text("native_diagnostic.e19c3b9e26") : sample.browser)
        : "Mac"
      lines.append(name + " · " + row.title)
      lines.append(
        context.text("native_diagnostic.2f3e61e480") + MeasurementTimestamp.text(report.at))
      switch row.kind {
      case "public_ip":
        lines += [
          "IPv4：" + E.familyValue(sample, v6: false, context: context),
          "IPv6：" + E.familyValue(sample, v6: true, context: context),
          context.text("native_diagnostic.35e12e464c") + E.location(sample, context: context),
          P.acquisitionSummary(sample.intelligence, context: context),
          (sample.intelligence?.providers ?? []).map {
            P.providerDetails(sample.intelligence, id: $0.id, context: context)
          }.joined(separator: "\n\n"),
        ]
      case "language":
        let languages = browser ? sample.languages : local?.languages ?? []
        lines += [
          context.text("native_diagnostic.315e93563e")
            + (languages.isEmpty
              ? context.text("native_diagnostic.e7954a2f3a") : languages.joined(separator: " → ")),
          context.text("native_diagnostic.8f466fd632")
            + (browser
              ? sample.region : local?.region ?? context.text("native_diagnostic.e7954a2f3a")),
        ]
      case "timezone":
        let zone = browser ? sample.timezone : local?.timezone ?? ""
        lines += [
          context.text("native_diagnostic.58600047a4")
            + (zone.isEmpty ? context.text("native_diagnostic.e7954a2f3a") : zone),
          context.text("native_diagnostic.eb22b3bfcb")
            + E.timezone(zone, at: report.at, context: context),
        ]
      case "dns":
        lines += [
          E.dnsPathDetails(
            sample, name: browser ? context.text("native_diagnostic.e19c3b9e26") : "Mac",
            context: context),
          context.text(
            "native_diagnostic.3cc64903bf", ["arg0": String(describing: sample.resolvers.count)]),
          context.text("native_diagnostic.bbb17e8cb8"),
        ]
        if !sample.dnsError.isEmpty {
          lines.append(
            context.text("native_diagnostic.edd04ba74c")
              + E.checkProblem(sample.dnsError, context: context))
        }
        lines += sample.resolvers.map {
          $0.ip + " · " + P.countryName($0.country, context: context) + " · " + $0.organization
        }
      case "webrtc":
        lines.append(
          context.text(
            "native_diagnostic.963e43ae47", ["arg0": String(describing: sample.rtcAddresses.count)])
        )
        if sample.rtcAddresses.isEmpty {
          lines.append(context.text("native_diagnostic.ff9a9d1d55"))
        }
        lines += sample.rtcAddresses
      default: return row.detail
      }
      let checks = (sample.checks ?? []).filter { check in
        switch row.kind {
        case "public_ip": ["ipv4", "ipv6", "ipv6_route", "intelligence"].contains(check.kind)
        case "dns": ["dns_session", "dns_query", "dns_result"].contains(check.kind)
        case "webrtc": check.kind.hasPrefix("rtc_")
        default: false
        }
      }
      if !checks.isEmpty {
        lines.append(context.text("native_diagnostic.d46cb91a2d"))
        lines += checks.map {
          $0.target + "："
            + E.checkText($0, ipv6Off: E.ipv6Exposure(report) == .off, context: context)
        }
      }
    }
    let explanation = E.riskExplanation(for: row.kind, context: context)
    if !explanation.isEmpty { lines.append(explanation) }
    return lines.joined(separator: "\n")
  }

  private func summaryColor(_ tone: E.Tone) -> NSColor {
    switch tone {
    case .information: PaxTheme.NetworkDiagnostics.summaryText
    case .matching: PaxTheme.NetworkDiagnostics.summaryClear
    case .difference: PaxTheme.NetworkDiagnostics.summaryWarning
    case .failure: PaxTheme.Color.error
    case .pending: PaxTheme.Color.textSecondary
    }
  }

  private func flag(_ country: String) -> String {
    let codes = country.uppercased().unicodeScalars
    guard codes.count == 2, codes.allSatisfy({ (65...90).contains($0.value) }) else { return "" }
    return String(String.UnicodeScalarView(codes.compactMap { UnicodeScalar(127397 + $0.value) }))
      + " "
  }
}
