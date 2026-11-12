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
    let overview = E.overview(environment?.report)
    let intel = overview.intelligence
    let address = overview.address.isEmpty ? "等待检测" : overview.address
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
      ? [P.countryName(intel?.country ?? ""), city].filter { !$0.isEmpty }.joined(
        separator: " · ")
      : (overview.address.isEmpty ? "尚未取得出口" : "地区未取得")
    let asn = source?.asn ?? ""
    let detail = [
      location, asn.isEmpty ? "" : (asn.uppercased().hasPrefix("AS") ? asn : "AS" + asn),
      source?.org ?? "",
    ].filter { !$0.isEmpty }.joined(separator: " · ")
    let counts = P.riskCounts(intel)
    let native = P.nativeAssessment(intel)
    let risk = blacklistSummary(intel)
    let type = intel?.ipType.isEmpty == false ? P.typeName(intel?.ipType ?? "") : "类型未取得"
    let shortType: String
    switch intel?.ipType {
    case "hosting": shortType = "机房"
    case "isp": shortType = "家宽"
    case "mobile": shortType = "移动"
    case "business": shortType = "企业"
    case "edu": shortType = "教育"
    case nil, "": shortType = "未取得"
    default: shortType = type
    }
    let cells: [NSView] = [
      vertical(
        [
          text(flag(intel?.country ?? "") + "出口 IP", secondary: true), ip,
        ],
        spacing: PaxTheme.Spacing.tight),
      metricCard(
        "风险记录", value: intel == nil ? "—" : "\(counts.hits) 命中 / \(counts.unknown) 未知",
        tone: counts.hits > 0 ? .difference : .pending,
        detail: P.acquisitionSummary(intel) + "；按检查项统计，不是安全分"),
      metricCard(
        "原生性 · 来源判断", value: native.text, tone: native.tone,
        detail: "ipx.al 根据登记国和推定使用国判断；不证明原生家宽。\n" + P.providerDetails(intel, id: "ipx.al")),
      metricCard(
        "类型", value: shortType, tone: intel?.ipType.isEmpty == false ? .information : .pending,
        detail: "资料供应方分类；未标记机房不代表住宅网络"),
      metricCard("黑名单", value: risk.text, tone: risk.tone, detail: risk.detail),
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
    for cell in cells.dropFirst() {
      cell.widthAnchor.constraint(equalTo: cells[1].widthAnchor).isActive = true
    }
    cells[0].widthAnchor.constraint(equalTo: cells[1].widthAnchor, multiplier: 2).isActive = true
    let result = card(row, summary: true)
    result.toolTip = detail + "\n" + overview.source + " · " + overview.comparison
    return result
  }

  private func metricCard(_ title: String, value: String, tone: E.Tone, detail: String) -> NSView {
    let parts =
      ["黑名单", "风险记录"].contains(title)
      ? value.split(separator: " ", maxSplits: 1).map(String.init) : [value]
    let label = text(parts[0])
    label.font = PaxTheme.Typography.label(
      size: PaxTheme.NetworkDiagnostics.metricFont, weight: .medium)
    label.textColor = environmentColor(tone)
    let valueRow = horizontal(
      [label] + (parts.count > 1 ? [text(parts[1], secondary: true)] : []) + [NSView()],
      spacing: PaxTheme.Spacing.xs)
    let result = vertical(
      [text(title, secondary: true), valueRow], spacing: PaxTheme.Spacing.tight)
    result.toolTip = detail
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
      ? "\(hits) 项命中"
      : checked > 0
        ? "\(checked) 项未命中" + (unknown > 0 ? " · 未全测" : "")
        : (intel?.providers == nil ? "未取得" : "未检测")
    return (
      text, hits > 0 ? .difference : checked > 0 ? .matching : .pending,
      intel?.providers != nil && checked == 0
        ? "未取得有效 DNSBL 结果；独立与补充来源缺失均不视为安全。"
        : "DNSBL：已取得 \(checked) 项结果，经 ipx.al 获取；缺失项目不视为安全。"
    )
  }

  private func mergedEnvironmentCard() -> NSView {
    let environment = store.currentEnvironment
    let rows = E.assessedRows(
      environment?.report, local: environment?.local, waiting: environment?.busy == true)
    let timestamp = text(
      (environment?.stale == true ? "上次环境检测 · " : "环境检测 · ")
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
      title: row.title, subtitle: row.value, symbol: row.symbol,
      detail: environmentReadingDetails(row), tone: row.tone)
  }

  private func environmentReadingDetails(_ row: E.Row) -> String {
    guard let report = store.currentEnvironment?.report else { return row.detail }
    let local = store.currentEnvironment?.local
    var lines: [String] = []
    for browser in [false, true] {
      if row.title == "WebRTC" && !browser { continue }
      let sample = browser ? report.browser : report.mac
      let name = browser ? (sample.browser.isEmpty ? "浏览器" : sample.browser) : "Mac"
      lines.append(name + " · " + row.title)
      lines.append("检测时间：" + MeasurementTimestamp.text(report.at))
      switch row.title {
      case "公网 IP":
        lines += [
          "IPv4：" + E.familyValue(sample, v6: false), "IPv6：" + E.familyValue(sample, v6: true),
          "归属资料：" + E.location(sample), P.acquisitionSummary(sample.intelligence),
          (sample.intelligence?.providers ?? []).map {
            P.providerDetails(sample.intelligence, id: $0.id)
          }.joined(separator: "\n\n"),
        ]
      case "语言":
        let languages = browser ? sample.languages : local?.languages ?? []
        lines += [
          "语言顺序：" + (languages.isEmpty ? "未取得" : languages.joined(separator: " → ")),
          "地区设置：" + (browser ? sample.region : local?.region ?? "未取得"),
        ]
      case "时区":
        let zone = browser ? sample.timezone : local?.timezone ?? ""
        lines += [
          "时区标识：" + (zone.isEmpty ? "未取得" : zone),
          "时区说明：" + E.timezone(zone, at: report.at),
        ]
      case "DNS":
        lines += ["解析器数量：\(sample.resolvers.count)", "检测来源：bash.ws · 本轮随机域名采样"]
        if !sample.dnsError.isEmpty { lines.append("未完成原因：" + E.checkProblem(sample.dnsError)) }
        lines += sample.resolvers.map {
          $0.ip + " · " + P.countryName($0.country) + " · " + $0.organization
        }
      case "WebRTC":
        lines.append("候选数量：\(sample.rtcAddresses.count)")
        if sample.rtcAddresses.isEmpty { lines.append("采样结果：未取得候选，不能据此确认防泄露") }
        lines += sample.rtcAddresses
      default: return row.detail
      }
      let checks = (sample.checks ?? []).filter { check in
        switch row.title {
        case "公网 IP": ["ipv4", "ipv6", "ipv6_route", "intelligence"].contains(check.kind)
        case "DNS": check.kind.hasPrefix("dns_")
        case "WebRTC": check.kind.hasPrefix("rtc_")
        default: false
        }
      }
      if !checks.isEmpty {
        lines.append("采样证据")
        lines += checks.map { $0.target + "：" + E.checkText($0) }
      }
    }
    return lines.joined(separator: "\n")
  }

  private func environmentColor(_ tone: E.Tone) -> NSColor {
    switch tone {
    case .information: PaxTheme.Color.accent
    case .matching: PaxTheme.Color.healthy
    case .difference: PaxTheme.Color.amberLabel
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
