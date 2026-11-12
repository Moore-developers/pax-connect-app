import Foundation
import PaxClient

/// Plain-language evidence, also used by the frozen environment export.
public enum EnvironmentDiagnosticPresentation {
  public enum Tone: Sendable { case information, matching, difference, failure, pending }
  public struct Row: Sendable {
    public let title, symbol, value, detail: String
    public let tone: Tone
  }
  public static func rows(
    _ sample: EnvironmentDiagnosticReport.Sample?, local: NetworkDiagnosticEnvironment? = nil,
    browser: Bool, waiting: Bool, at: Date? = nil
  ) -> [Row] {
    if sample == nil && local == nil && !waiting {
      let items =
        [("公网 IP", "globe"), ("语言", "character.bubble"), ("时区", "clock"), ("DNS", "server.rack")]
        + (browser ? [("WebRTC", "point.3.connected.trianglepath.dotted")] : [])
      return items.map { Row(title: $0.0, symbol: $0.1, value: "尚未检测", detail: "", tone: .pending) }
    }
    let pending = sample?.complete != true && waiting
    let languages = browser ? sample?.languages ?? [] : local?.languages ?? []
    let region = browser ? sample?.region ?? "" : local?.region ?? ""
    let zone = browser ? sample?.timezone ?? "" : local?.timezone ?? ""
    var rows: [Row] = []
    let ips = [sample?.ipv4 ?? "", sample?.ipv6 ?? ""].filter { !$0.isEmpty }
    rows.append(
      Row(
        title: "公网 IP", symbol: "globe",
        value: ips.isEmpty
          ? (pending
            ? "正在获取公网 IP…" : sampleProblem(sample?.ipError ?? "", fallback: "公网 IP 请求未取得有效地址"))
          : location(sample),
        detail: ips.isEmpty
          ? "本项尚未完成，不能据此判断网络不可用"
          : ips.joined(separator: "\n"),
        tone: ips.isEmpty ? (pending ? .pending : .failure) : .information))
    let locale = Locale(identifier: "zh_CN")
    let language =
      languages.first.map {
        locale.localizedString(
          forLanguageCode: Locale(identifier: $0).language.languageCode?.identifier ?? $0) ?? $0
      } ?? ""
    let code = Locale(identifier: region).region?.identifier ?? ""
    let country = code.isEmpty ? "" : NetworkDiagnosticPresentation.countryName(code)
    rows.append(
      Row(
        title: "语言", symbol: "character.bubble",
        value: language.isEmpty ? (pending ? "等待读取语言…" : "无法读取语言") : language,
        detail: languages.joined(separator: " → ") + (country.isEmpty ? "" : " · 地区设为" + country),
        tone: language.isEmpty ? (pending ? .pending : .failure) : .information))
    rows.append(
      Row(
        title: "时区", symbol: "clock",
        value: zone.isEmpty
          ? (pending ? "等待读取时区…" : "无法读取时区") : timezone(zone, at: local?.at ?? at ?? Date()),
        detail: zone.isEmpty ? "" : zone + " · " + localTime(zone, at: local?.at ?? at ?? Date()),
        tone: zone.isEmpty ? (pending ? .pending : .failure) : .information))
    let resolvers = sample?.resolvers ?? []
    rows.append(
      Row(
        title: "DNS", symbol: "server.rack",
        value: resolvers.isEmpty
          ? (pending
            ? "正在检测 DNS 解析出口…" : sampleProblem(sample?.dnsError ?? "", fallback: "DNS 服务未返回有效解析器"))
          : dnsSummary(resolvers),
        detail: resolvers.isEmpty
          ? (pending ? "" : "未取得有效结果，本项检测未完成")
          : resolvers.prefix(2).map(\.ip).joined(separator: " · ")
            + (resolvers.count > 2 ? "\n其余 \(resolvers.count - 2) 个可在详情中查看" : ""),
        tone: resolvers.isEmpty ? (pending ? .pending : .failure) : .information))
    if browser {
      let addresses = (sample?.rtcAddresses ?? []).filter { isPublicAddress($0) }
      let state = sample?.rtcState ?? ""
      let value: String
      let tone: Tone
      if !addresses.isEmpty {
        let comparable = addresses.allSatisfy { address in
          address.contains(":") ? !(sample?.ipv6.isEmpty ?? true) : !(sample?.ipv4.isEmpty ?? true)
        }
        let same = comparable && addresses.allSatisfy { ips.contains($0) }
        value =
          "检测到 \(addresses.count) 个公网地址：" + addresses.joined(separator: "、") + "；"
          + (!comparable ? "未取得同地址族网页出口供对照" : same ? "与网页出口相同" : "与网页出口不同")
        tone = !comparable ? .information : same ? .matching : .difference
      } else if state == "unsupported" {
        value = "此浏览器不支持 WebRTC"
        tone = .difference
      } else if pending {
        value = "正在检测 WebRTC 地址…"
        tone = .pending
      } else if state == "failed" {
        value = "WebRTC 检测未完成"
        tone = .failure
      } else {
        value =
          "采集到 \(sample?.rtcAddresses.count ?? 0) 个 ICE 候选，0 个公网地址；"
          + ((sample?.rtcAddresses.isEmpty ?? true) ? "原因未确定" : "局域网 / mDNS 候选见详情")
        tone = .difference
      }
      let evidence = (sample?.checks ?? []).filter { $0.kind.hasPrefix("rtc_") }
        .map { $0.target + "：" + checkOutcome($0) }.joined(separator: "；")
      rows.append(
        Row(
          title: "WebRTC", symbol: "point.3.connected.trianglepath.dotted",
          value: value + (evidence.isEmpty ? "" : "；" + evidence),
          detail: addresses.isEmpty
            ? (pending || state == "unsupported" ? "" : "可能已禁用或受到限制；不代表已确认防泄露")
            : addresses.joined(separator: "\n"), tone: tone))
    }
    return rows
  }
  private static func sampleProblem(_ code: String, fallback: String) -> String {
    switch code {
    case "sampling_timeout": "45 秒会话超时，未收到该项结果"
    case "dns_cancelled": "DNS 检测已取消"
    case "dns_unavailable": "DNS 服务未返回有效解析器，具体原因未记录"
    case "ip_unavailable": "公网 IP 请求未取得有效地址，具体原因未记录"
    case "": fallback
    default: checkProblem(code)
    }
  }
  public static func location(_ sample: EnvironmentDiagnosticReport.Sample?) -> String {
    guard let sample, !sample.ipv4.isEmpty || !sample.ipv6.isEmpty else { return "尚未取得公网 IP" }
    if let error = sample.intelligence?.providerError, !error.isEmpty {
      return "公网 IP 已取得，资料查询失败：" + NetworkDiagnosticPresentation.problem(error)
    }
    guard let intel = sample.intelligence, !intel.country.isEmpty else {
      return "公网 IP 已取得，地区资料未取得"
    }
    let city = intel.sources.first { $0.country == intel.country && !$0.city.isEmpty }?.city ?? ""
    return NetworkDiagnosticPresentation.countryName(intel.country)
      + (city.isEmpty ? "" : " · " + city)
  }
  public static func dnsSummary(_ values: [EnvironmentDiagnosticReport.Resolver]) -> String {
    let grouped = Dictionary(grouping: values, by: \.country)
    let countries = grouped.keys.sorted().map {
      NetworkDiagnosticPresentation.countryName($0)
        + (grouped.count > 1 ? " \(grouped[$0]?.count ?? 0) 个" : "")
    }.joined(separator: "、")
    return "发现 \(values.count) 个 DNS 解析出口，" + (grouped.count == 1 ? "均位于" : "分布于") + countries
  }
  public static func timezone(_ identifier: String, at: Date) -> String {
    guard let zone = TimeZone(identifier: identifier) else { return "无法识别时区（\(identifier)）" }
    let countries = (zoneCountries[identifier] ?? []).map {
      NetworkDiagnosticPresentation.countryName($0)
    }.joined(separator: " / ")
    let name = zone.localizedName(for: .generic, locale: Locale(identifier: "zh_CN")) ?? identifier
    return (countries.isEmpty ? "" : countries + " · ") + name + "，"
      + NetworkDiagnosticPresentation.utcOffset(identifier, at: at)
  }
  private static let zoneCountries: [String: [String]] = {
    guard let text = try? String(contentsOfFile: "/usr/share/zoneinfo/zone.tab", encoding: .utf8)
    else { return [:] }
    var result: [String: [String]] = [:]
    for line in text.split(separator: "\n") where !line.hasPrefix("#") {
      let fields = line.split(separator: "\t")
      if fields.count >= 3 {
        result[String(fields[2])] = fields[0].split(separator: ",").map(String.init)
      }
    }
    return result
  }()
  private static func localTime(_ identifier: String, at: Date) -> String {
    let format = DateFormatter()
    format.locale = Locale(identifier: "zh_CN")
    format.timeZone = TimeZone(identifier: identifier)
    format.dateFormat = "MM月dd日 HH:mm"
    return "检测时当地时间 " + format.string(from: at)
  }
  public static func isPublicAddress(_ address: String) -> Bool {
    let lower = address.lowercased()
    if lower.hasSuffix(".local") || lower == "::1" || lower == "::" || lower.hasPrefix("fe80:")
      || lower.hasPrefix("fc") || lower.hasPrefix("fd")
    {
      return false
    }
    if lower.contains(":") { return true }
    let parts = lower.split(separator: ".").compactMap { Int($0) }
    guard parts.count == 4 else { return false }
    return
      !(parts[0] == 0 || parts[0] == 10 || parts[0] == 127 || parts[0] >= 224
      || (parts[0] == 169 && parts[1] == 254) || (parts[0] == 172 && (16...31).contains(parts[1]))
      || (parts[0] == 192 && parts[1] == 168) || (parts[0] == 100 && (64...127).contains(parts[1])))
  }
  public static func differences(
    _ report: EnvironmentDiagnosticReport?, local: NetworkDiagnosticEnvironment?
  ) -> Set<String> {
    guard let report, report.mac.complete, report.browser.complete, !report.browser.browser.isEmpty
    else { return [] }
    var result: Set<String> = []
    for pair in [(report.mac.ipv4, report.browser.ipv4), (report.mac.ipv6, report.browser.ipv6)]
    where !pair.0.isEmpty && !pair.1.isEmpty {
      if pair.0 != pair.1 { result.insert("公网 IP") }
    }
    if let macLanguage = local?.languages.first,
      let browserLanguage = report.browser.languages.first,
      Locale(identifier: macLanguage).identifier != Locale(identifier: browserLanguage).identifier
    {
      result.insert("语言")
    }
    if let macZone = local?.timezone, !report.browser.timezone.isEmpty,
      macZone != report.browser.timezone
    {
      result.insert("时区")
    }
    let macDNS = Set(report.mac.resolvers.map(\.ip))
    let browserDNS = Set(report.browser.resolvers.map(\.ip))
    if !macDNS.isEmpty && !browserDNS.isEmpty && macDNS != browserDNS { result.insert("DNS") }
    return result
  }
  public static func difference(
    _ report: EnvironmentDiagnosticReport?, local: NetworkDiagnosticEnvironment? = nil
  ) -> String {
    guard let report, report.mac.complete, report.browser.complete, !report.browser.browser.isEmpty
    else { return "等待两侧结果" }
    let values = differences(report, local: local)
    if !values.isEmpty { return "发现 \(values.count) 项差异" }
    guard !report.mac.ipv4.isEmpty || !report.mac.ipv6.isEmpty,
      !report.browser.ipv4.isEmpty || !report.browser.ipv6.isEmpty
    else { return "部分项目未完成" }
    return "可对照项一致"
  }
  @MainActor public static func markdown(_ store: EnvironmentDiagnosticStore) -> String {
    var lines = [
      "## 当前环境检测",
      "采集：" + MeasurementTimestamp.text(store.report?.at) + (store.stale ? " · 历史结果" : ""),
    ]
    lines.append("\n### 环境结果")
    lines.append("- 出口判断：" + exitAssessment(store.report?.browser.checks ?? []).text)
    for row in assessedRows(store.report, local: store.local, waiting: store.busy) {
      lines.append("- " + row.title + "：" + row.value + "\n  依据：" + row.detail)
    }
    for browser in [false, true] {
      let sample = browser ? store.report?.browser : store.report?.mac
      lines.append("\n### " + (browser ? "浏览器" : "Mac"))
      for row in rows(
        sample, local: browser ? nil : store.local, browser: browser, waiting: false,
        at: store.report?.at)
      {
        lines.append("- " + row.title + "：" + row.value + "；" + row.detail)
      }
      if let intel = sample?.intelligence {
        lines.append("\n#### IP 资料 · " + NetworkDiagnosticPresentation.acquisitionSummary(intel))
        for provider in intel.providers ?? [] {
          lines.append(NetworkDiagnosticPresentation.providerDetails(intel, id: provider.id))
        }
        for source in intel.sources {
          lines.append(
            "- " + source.name + "：" + source.country + " / " + source.city
              + " · " + source.asn + " · " + source.org + " · "
              + NetworkDiagnosticPresentation.channel(source.providerID)
              + " · 资料时标：" + NetworkDiagnosticPresentation.value(source.providerDate))
        }
        for fact in intel.facts {
          lines.append(
            "- " + fact.label + "：" + fact.value + " · "
              + NetworkDiagnosticPresentation.factState(fact.state) + " · " + fact.source
              + " · "
              + (fact.providerID.map { NetworkDiagnosticPresentation.channel($0) }
                ?? (intel.providers == nil ? "旧记录未记录获取渠道" : "未调用第三方来源")))
        }
      }
      if sample?.checks == nil { lines.append("- 分项诊断：旧记录未采集") }
      for check in sample?.checks ?? [] {
        lines.append("- " + check.kind + " · " + check.target + "：" + checkText(check))
      }
      for resolver in sample?.resolvers ?? [] {
        lines.append("- DNS：\(resolver.ip) · \(resolver.country) · \(resolver.organization)")
      }
    }
    if let previous = store.previousReport {
      lines.append("\n### 上次环境成功读值（历史） · " + MeasurementTimestamp.text(previous.at))
      for (title, sample) in [("Mac", previous.mac), ("浏览器", previous.browser)] {
        lines.append("- " + title + " IPv4：" + sample.ipv4 + "；IPv6：" + sample.ipv6)
        lines.append("- " + title + " DNS：" + sample.resolvers.map(\.ip).joined(separator: "、"))
      }
    }
    var output = lines.joined(separator: "\n")
    let samples = [
      store.report?.mac, store.report?.browser, store.previousReport?.mac,
      store.previousReport?.browser,
    ].compactMap { $0 }
    let addresses = samples.flatMap {
      [$0.ipv4, $0.ipv6] + $0.rtcAddresses + $0.resolvers.map(\.ip)
        + ($0.intelligence?.providers ?? []).map(\.queryIP)
    }
    let echoAddresses = samples.flatMap {
      ($0.checks ?? []).filter { $0.kind.hasPrefix("echo_") }.map {
        $0.value.components(separatedBy: " · ")[0]
      }
    }
    for address in Set(addresses + echoAddresses).filter({ !$0.isEmpty }).sorted(by: {
      $0.count > $1.count
    }) {
      output = output.replacingOccurrences(of: address, with: "[已脱敏]")
    }
    return output
  }
}
