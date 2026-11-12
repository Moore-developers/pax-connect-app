import Foundation
import PaxClient

/// Evidence-only summaries shared by the native workbench and frozen export.
public enum NetworkDiagnosticPresentation {
  public struct TargetSummary: Equatable, Sendable {
    public let target: String
    public let samples: [NetworkDiagnosticReport.Probe]
    public var successful: [NetworkDiagnosticReport.Probe] { samples.filter(\.expectedResponse) }
    public var firstMS: UInt64? {
      samples.first(where: { $0.attempt == 1 && $0.expectedResponse })?.durationMS
    }
    public var median: UInt64? {
      let values = successful.filter { $0.attempt > 1 }.map(\.durationMS).sorted()
      guard !values.isEmpty else { return nil }
      let middle = values.count / 2
      return values.count.isMultiple(of: 2)
        ? (values[middle - 1] + values[middle]) / 2 : values[middle]
    }
    public var range: String {
      let values = successful.filter { $0.attempt > 1 }.map(\.durationMS)
      guard values.count >= 3, let low = values.min(), let high = values.max() else {
        return "样本不足"
      }
      return "\(low)–\(high) ms"
    }
    public var count: String { "\(successful.count)/\(samples.count) 请求成功" }
  }
  public static func targets(_ report: NetworkDiagnosticReport?) -> [TargetSummary] {
    guard let report else { return [] }
    let targets = report.probes.reduce(into: [String]()) {
      if !$0.contains($1.target) { $0.append($1.target) }
    }
    return targets.map { target in
      TargetSummary(target: target, samples: report.probes.filter { $0.target == target })
    }
  }
  public static func typeName(_ value: String) -> String {
    switch value {
    case "hosting": "机房 / 数据中心"
    case "isp": "住宅网络"
    case "mobile": "移动网络"
    case "business": "企业网络"
    case "edu": "教育网络"
    case "pseudo_residential", "pseudo": "疑似伪家宽"
    case "native": "注册国与推定使用国一致（供应方判断）"
    case "broadcast": "注册国与推定使用国不同（供应方判断）"
    case "": "未提供"
    default: value
    }
  }
  public static func value(_ value: String) -> String { value.isEmpty ? "未提供" : value }
  public static func factState(_ state: String) -> String {
    switch state {
    case "hit": "命中"
    case "clear": "未命中"
    case "information": "资料"
    default: "未知"
    }
  }
  public static func countrySummary(_ report: NetworkDiagnosticReport?) -> String {
    guard let report else { return "尚未取得定位资料" }
    let counts = Dictionary(grouping: report.sources.filter { !$0.country.isEmpty }, by: \.country)
    guard !counts.isEmpty else { return "没有返回可对照的国家资料" }
    return counts.keys.sorted().map { "\(countryName($0)) \(counts[$0]?.count ?? 0) 源" }.joined(
      separator: " · ")
  }
  public static func organization(_ report: NetworkDiagnosticReport?) -> String {
    guard let report else { return "组织未提供" }
    if let isp = report.exit?.isp, !isp.isEmpty { return isp }
    let expected = (report.exit?.asn ?? "").replacingOccurrences(of: "AS", with: "")
    let names = Set(
      report.sources.filter {
        expected.isEmpty || $0.asn.replacingOccurrences(of: "AS", with: "") == expected
      }.map(\.org).filter { !$0.isEmpty })
    if names.count == 1 { return names.first ?? "组织未提供" }
    return names.isEmpty ? "组织未提供" : "组织资料存在分歧"
  }
  public static func countryName(_ code: String) -> String {
    guard !code.isEmpty else { return "未提供" }
    return Locale(identifier: "zh_CN").localizedString(forRegionCode: code) ?? code
  }
  public static func utcOffset(_ zone: String, at: Date) -> String {
    guard let zone = TimeZone(identifier: zone) else { return "UTC 偏移未知" }
    let seconds = zone.secondsFromGMT(for: at)
    return String(
      format: "UTC%@%02d:%02d", seconds < 0 ? "−" : "+", abs(seconds) / 3600,
      abs(seconds) % 3600 / 60)
  }
  public static func languageName(_ value: String) -> String {
    let locale = Locale(identifier: "zh_CN")
    return (locale.localizedString(forIdentifier: value) ?? value) + "（" + value + "）"
  }
  public static func differences(
    _ report: NetworkDiagnosticReport, source: NetworkDiagnosticReport.Source
  ) -> Bool {
    report.sources.contains { other in
      [
        (source.country, other.country), (source.city, other.city),
        (normalizedASN(source.asn), normalizedASN(other.asn)),
      ].contains { a, b in !a.isEmpty && !b.isEmpty && a != b }
    }
  }

  public static func downloadEnd(_ code: String) -> String {
    switch code {
    case "byte_cap": "达到流量上限"
    case "sample_window": "达到采样时间"
    case "budget_window", "total_timeout": "达到时间预算"
    case "sample_short": "采样时间过短"
    case "body_short": "正文不足"
    case "cancelled": "用户停止"
    case "body_interrupted", "transfer_failed": "正文传输中断"
    case "headers_failed": "未取得响应头"
    default: "结束原因未提供"
    }
  }
  public static func problem(_ code: String) -> String {
    if code.isEmpty { return "未提供" }
    if code.hasPrefix("provider_http_") {
      return "资料接口 HTTP " + code.dropFirst("provider_http_".count)
    }
    if code.hasPrefix("target_http_") {
      return "HTTP " + code.dropFirst("target_http_".count) + " · 已收到响应"
    }
    switch code {
    case "provider_ip_mismatch": return "资料返回的 IP 与本次出口不符，已丢弃"
    case "intelligence_unavailable": return "独立来源及备用均未取得有效资料"
    case "provider_timeout": return "资料请求超时"
    case "provider_fields_missing": return "来源未提供所需字段"
    case "provider_family_unsupported": return "此来源的该地址族尚未支持"
    case "exit_services_unavailable": return "来源冷却或额度暂不可用"
    case "operation_cancelled": return "检测已取消"
    case "provider_response_invalid": return "资料格式无效或超出限额"
    case "exit_unavailable": return "未取得本次出口，未查询信誉资料"
    case "target_connection_timeout": return "等待目标响应超时"
    case "target_tls_failed", "tls_certificate_invalid": return "TLS 验证失败"
    case "target_connection_refused": return "目标拒绝连接"
    case "diagnostic_budget_exhausted": return "本轮时间预算耗尽，未发起请求"
    case "diagnostic_cancelled": return "用户取消检测"
    case "target_dns_failed": return "目标域名解析失败"
    case "target_network_unreachable": return "目标网络不可达"
    case "target_connection_reset": return "连接被重置"
    case "target_connection_closed": return "目标关闭连接"
    case "target_connection_failed": return "连接失败，具体原因未记录"
    case "node_connection_timeout": return "建立线路连接超时"
    default: return "检测未完成（\(code)）"
    }
  }
  public static func probeResult(_ sample: NetworkDiagnosticReport.Probe) -> String {
    if sample.httpStatus == 0 {
      return problem(sample.errorCode) + (sample.stage.map { " · 阶段 " + $0 } ?? "")
        + (sample.state == "not_started" ? "" : " · \(sample.durationMS) ms")
    }
    let meaning: String
    switch sample.httpStatus {
    case 401: meaning = "需要认证"
    case 403: meaning = "服务器拒绝请求"
    case 429: meaning = "服务器限流"
    case 300..<400:
      meaning =
        "重定向" + (sample.redirectHost?.isEmpty == false ? " → " + (sample.redirectHost ?? "") : "")
    default: meaning = "收到响应"
    }
    return "HTTP \(sample.httpStatus) · " + meaning + " · \(sample.durationMS) ms"
  }
  @MainActor public static func markdown(
    _ report: NetworkDiagnosticReport?, services: NetworkDiagnosticReport?,
    environment: NetworkDiagnosticEnvironment?, stale: Bool
  ) -> String {
    guard let report else { return "# 网络性能诊断\n\n尚未检测。" }
    var lines = [
      "# 网络性能诊断", "", "时间：\(MeasurementTimestamp.secondsText(report.at))",
      "目标：线路（已匿名）", "出口：已脱敏", "有效性：\(stale || report.changedDuringCapture ? "历史 / 失效" : "本轮观测")",
      "", "## IP 画像",
      "类型：\(typeName(report.ipType))",
      "风险记录：\(riskCounts(report).hits) 项命中 / \(riskCounts(report).unknown) 项未知",
      "来源错误：\(report.providerError.isEmpty ? "无" : problem(report.providerError))", "", "## 多源对比",
    ]
    if let score = report.score {
      lines.append("ipx.al 供应方评分：\(score) · " + report.scoreModel)
    }
    var latest: [String: NetworkDiagnosticReport.Probe] = [:]
    for probe in report.probes + (services?.probes ?? []) {
      guard let host = URL(string: probe.target)?.host,
        !["www.google.com", "www.cloudflare.com"].contains(host)
      else { continue }
      if let old = latest[host], (old.at ?? report.at) > (probe.at ?? report.at) { continue }
      latest[host] = probe
    }
    lines.insert(
      contentsOf: [
        "", "## 摘要判断",
        "- 定位：" + sourceOverview(report.sources).text,
        "- 服务：" + serviceOverview(Array(latest.values)).text,
        "- 风险：" + riskOverview(report.facts.filter { $0.group == "risk" }).text,
      ], at: 7)
    for s in report.sources {
      lines.append(
        "- \(s.name)：\(s.country) / \(s.city) · ASN \(s.asn) · 来源时间标记 \(value(s.providerDate)) · \(channel(s.providerID))"
      )
    }
    lines += ["", "## 资料获取", acquisitionSummary(report)]
    for provider in report.providers ?? [] {
      lines += ["", "### " + provider.id, providerDetails(report, id: provider.id)]
    }
    lines += ["", "## 供应方资料与风险"]
    for f in report.facts {
      let content = f.group == "routing" ? "已保存候选证据；自定义规则/线路名称默认隐藏，在 App 中查看完整去向" : value(f.value)
      lines.append(
        "- \(f.label)：\(content) · \(factState(f.state)) · \(f.source) · \(f.providerID.map { channel($0) } ?? (report.providers == nil ? channel(nil) : "未调用第三方来源"))"
      )
    }
    lines += ["", "## 请求样本"]
    for probe in report.probes + (services?.probes ?? []) {
      lines.append(
        "- \(probe.target) #\(probe.attempt)：" + probeResult(probe) + " · "
          + MeasurementTimestamp.secondsText(probe.at ?? report.at)
      )
    }
    if let local = environment {
      lines += [
        "", "## 本机系统环境", "采集：\(MeasurementTimestamp.secondsText(local.at))",
        "时区：\(local.timezone)；IP 推定时区：\(value(report.timezone))",
        local.timezoneComparison(report.timezone), "语言：\(local.languages.joined(separator: "、"))",
        "地区：\(local.region)",
      ]
    }
    lines += [
      "", "本段为指定线路原生探测；浏览器核心环境见独立环境章节，Claude Code 进程环境未测。公开端点响应不代表账号、模型或支付权限。",
      "报告在本机生成；目标与资料供应方可见出口或被查 IP；本机环境未上传。",
    ]
    var text = lines.joined(separator: "\n")
    // Third-party labels can repeat identity values. Redact those too.
    for identity in [
      report.exit?.ip ?? "", report.targetRouteID, report.targetRouteName, report.runningRouteID,
      report.runningRouteName,
    ] + (report.providers ?? []).map(\.queryIP)
      + report.facts.filter({ $0.group == "network" && $0.label == "宣告前缀" }).map(\.value)
    where !identity.isEmpty {
      text = text.replacingOccurrences(of: identity, with: "[已脱敏]")
    }
    return text
  }
}
