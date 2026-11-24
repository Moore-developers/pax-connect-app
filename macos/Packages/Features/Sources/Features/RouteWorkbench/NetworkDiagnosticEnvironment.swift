import AppKit
import PaxCore
import PaxUI

extension NetworkDiagnosticView {
  typealias E = EnvironmentDiagnosticPresentation

  func showEnvironmentBlock() {
    append(environmentSummary())
    let environment = store.currentEnvironment
    let title = text(context.text("diagnostic.overview.environment"))
    title.font = PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyTitle, weight: .semibold)
    let stamp =
      context.text(
        environment?.stale == true ? "native_diagnostic.46acb06da6" : "native_diagnostic.d3aababeca"
      )
      + MeasurementTimestamp.text(environment?.report?.at)
    append(
      horizontal([
        title, text("Mac · " + browserName, secondary: true), NSView(),
        text(stamp, secondary: true),
      ]))
    append(mergedEnvironmentCard())
  }

  var browserName: String {
    let name = store.currentEnvironment?.report?.browser.browser ?? ""
    return name.isEmpty ? context.text("diagnostic.overview.default_browser") : name
  }

  private func environmentSummary() -> NSView {
    let environment = store.currentEnvironment
    let overview = E.overview(environment?.report, context: context)
    let intel = overview.intelligence?.excludingProviderEvidence("ipx.al")
    let address =
      overview.address.isEmpty ? context.text("native_diagnostic.f97f6a88ec") : overview.address
    let ip = text(address)
    ip.font = PaxTheme.Typography.label(
      size: PaxTheme.NetworkDiagnostics.environmentIPFont, weight: .medium)
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
    let browser = environment?.report?.browser
    let hasBrowserIP = [browser?.ipv4, browser?.ipv6].contains { $0?.isEmpty == false }
    let tagText =
      overview.address.isEmpty ? context.text("common.unknown") : hasBrowserIP ? browserName : "Mac"
    let tag = PaxTag(tagText, style: .currentRoute)
    tag.setAccessibilityLabel(tagText)
    tag.setContentHuggingPriority(.required, for: .horizontal)
    let title = cardHeading("diagnostic.overview.exit", symbol: "globe")
    let risk =
      intel?.facts.contains(where: { $0.group == "risk" }) == true
      ? context.text(
        "native_diagnostic.e77ea3c4c6",
        ["arg0": String(counts.hits), "arg1": String(counts.unknown)])
      : context.text("common.unknown")
    let providers =
      intel?.providers?.filter { ["observed", "partial", "not_indexed"].contains($0.state) }
      .map(\.id) ?? []
    let names = providers.isEmpty ? (intel?.sources.map(\.name) ?? []) : providers
    let sources =
      names.isEmpty ? context.text("native_diagnostic.e7954a2f3a") : names.joined(separator: " · ")
    func metric(_ key: String, _ value: String) -> NSView {
      let caption = text(context.text(key), secondary: true)
      let field = text(value)
      field.maximumNumberOfLines = 1
      field.lineBreakMode = .byTruncatingTail
      field.alignment = .right
      field.toolTip = value
      caption.setContentHuggingPriority(.required, for: .horizontal)
      return horizontal([caption, field])
    }
    ip.textColor = PaxTheme.CurrentRoute.ink
    let region = text(location)
    region.textColor = PaxTheme.CurrentRoute.ink
    let addressGroup = vertical([ip, region], spacing: PaxTheme.Spacing.tight)
    let metrics = vertical([
      metric("native_diagnostic.14ace9bd02", risk),
      metric("diagnostic.overview.exit_type", shortType),
      metric("diagnostic.overview.providers", sources),
    ])
    let rule = NSBox()
    rule.boxType = .separator
    rule.widthAnchor.constraint(equalToConstant: PaxTheme.Layout.cardBorder).isActive = true
    let reading = horizontal([addressGroup, rule, metrics], spacing: PaxTheme.Spacing.page)
    rule.heightAnchor.constraint(equalTo: metrics.heightAnchor).isActive = true
    addressGroup.widthAnchor.constraint(
      equalTo: metrics.widthAnchor, multiplier: PaxTheme.NetworkDiagnostics.heroExitRatio
    ).isActive = true
    let result = card(
      vertical([horizontal([title, tag]), divider(), reading], spacing: PaxTheme.Spacing.medium),
      style: .currentRoute)
    result.setAccessibilityIdentifier("diagnostic-environment-overview")
    result.toolTip = [
      detail, overview.source, overview.comparison, P.acquisitionSummary(intel, context: context),
    ].joined(separator: "\n")
    return result
  }

  private func mergedEnvironmentCard() -> NSView {
    let environment = store.currentEnvironment
    let rows = E.assessedRows(
      environment?.report, local: environment?.local, waiting: environment?.busy == true,
      context: context)
    let order = ["public_ip", "dns", "webrtc", "ipv6", "language", "timezone", "location"]
    let ordered = rows.sorted {
      (order.firstIndex(of: $0.kind) ?? order.count)
        < (order.firstIndex(of: $1.kind) ?? order.count)
    }
    let table = diagnosticTable(
      ordered.map { row in
        diagnosticRow(
          title: row.title, kind: row.kind, subtitle: row.value, symbol: row.symbol,
          detail: environmentReadingDetails(row), tone: row.tone)
      })
    let result = card(
      vertical(
        [
          cardHeading("diagnostic.overview.checks", symbol: "laptopcomputer", count: rows.count),
          table,
        ],
        spacing: PaxTheme.Spacing.medium))
    result.setAccessibilityIdentifier("environment-merged-card")
    return result
  }

  private func environmentReadingDetails(_ row: E.Row) -> String {
    guard let report = store.currentEnvironment?.report else { return row.detail }
    let local = store.currentEnvironment?.local
    var lines: [String] = []
    for browser in [false, true] {
      if row.kind == "webrtc" && !browser { continue }
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
        let verdict = E.webRTCVerdict(
          sample, pending: !sample.complete && store.currentEnvironment?.busy == true,
          context: context)
        lines += [verdict.text] + verdict.routeDetails
        if let physical = verdict.physicalDetail { lines.append(physical) }
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

}
