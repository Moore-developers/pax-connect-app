import Foundation
import PaxClient

extension NetworkDiagnosticPresentation {
  public static func channel(_ id: String?) -> String {
    guard let id, !id.isEmpty else { return "旧记录 · 获取渠道未记录" }
    return id == "ipx.al" ? "补充 · 经 ipx.al" : "直接查询 · " + id
  }

  public static func capabilityName(_ value: String) -> String {
    [
      "geo.country": "国家", "geo.asn": "ASN", "risk.proxy": "代理", "risk.vpn": "VPN",
      "risk.tor": "Tor", "risk.datacenter": "机房 IP", "risk.mobile": "移动网络",
      "exposure.ports": "开放端口", "exposure.vulns": "已知漏洞", "exposure.tags": "Shodan 标签",
      "risk.relay": "iCloud 中继", "risk.anonymous": "匿名出口", "risk.abuse": "滥用标记",
      "risk.proxy_score": "代理风险（ipx.al阈值）", "risk.crawler": "爬虫", "risk.threat": "威胁情报名单",
      "risk.scanner": "恶意扫描", "risk.dnsbl": "邮件 DNSBL", "exposure.certificate": "自签证书",
      "risk.residential": "伪家宽", "risk.human": "人类流量比例", "risk.broadcast": "广播 IP",
    ][value] ?? value
  }

  public static func providerState(_ provider: NetworkDiagnosticReport.Provider) -> String {
    switch provider.state {
    case "observed": return "已返回资料"
    case "partial": return "部分字段未提供"
    case "not_indexed": return "该源未收录，不代表没有暴露记录"
    case "unsupported": return "该来源本次地址族未支持"
    case "not_configured": return "未配置来源"
    default: return problem(provider.errorCode)
    }
  }

  @MainActor public static func providerDetails(
    _ report: NetworkDiagnosticReport?, id: String?
  ) -> String {
    guard let provider = report?.providers?.first(where: { $0.id == id }) else {
      return id == nil ? "未记录逐源请求证据；未知项目不作安全判断" : "尚无该来源请求证据"
    }
    var lines = [
      "获取渠道：" + channel(provider.id),
      "查询地址：" + provider.queryIP + " · " + provider.family,
      "查询时间：" + (provider.at.map { MeasurementTimestamp.secondsText($0) } ?? "未记录"),
      "请求结果：" + providerState(provider) + " · \(provider.durationMS) ms",
    ]
    if provider.httpStatus > 0 { lines.append("HTTP：\(provider.httpStatus)") }
    if !provider.capabilities.isEmpty {
      lines.append("已返回能力：" + provider.capabilities.map(capabilityName).joined(separator: "、"))
    }
    if !provider.fallbackFor.isEmpty {
      lines.append(
        provider.fallbackReason == "independent_coverage_missing"
          ? "补充原因：独立来源未覆盖或未取得该项资料"
          : "备用原因：对应能力的独立来源失败或缺有效字段")
      lines.append("仅补缺：" + provider.fallbackFor.map(capabilityName).joined(separator: "、"))
    }
    return lines.joined(separator: "\n")
  }

  public static func acquisitionSummary(_ report: NetworkDiagnosticReport?) -> String {
    guard let providers = report?.providers else { return "旧记录未采集逐源请求；请按需重新检测" }
    let direct = providers.filter { $0.id != "ipx.al" }
    let answered = direct.filter { ["observed", "partial", "not_indexed"].contains($0.state) }.count
    if let fallback = providers.first(where: { $0.id == "ipx.al" }) {
      return "独立来源 \(answered)/\(direct.count) 返回；ipx.al 补充：" + providerState(fallback)
    }
    return "独立来源 \(answered)/\(direct.count) 返回；未请求 ipx.al"
  }

  public static func nativeAssessment(_ report: NetworkDiagnosticReport?) -> Assessment {
    let fact = report?.facts.first { $0.group == "registration" && $0.label == "原生 / 广播" }
    let name: String
    switch fact?.value {
    case "native": name = "原生 IP"
    case "broadcast": name = "广播 IP"
    default: return Assessment(text: "未确认", tone: .pending)
    }
    return Assessment(text: name, tone: .information)
  }

  public static func riskCounts(_ report: NetworkDiagnosticReport?) -> (hits: Int, unknown: Int) {
    let groups = Dictionary(
      grouping: report?.facts.filter { $0.group == "risk" } ?? [], by: \.label)
    return (
      groups.values.filter { $0.contains { $0.state == "hit" } }.count,
      groups.values.filter { !$0.contains { ["hit", "clear", "information"].contains($0.state) } }
        .count
    )
  }

  @MainActor public static func factDetails(
    _ fact: NetworkDiagnosticFact?, report: NetworkDiagnosticReport?
  )
    -> String
  {
    guard let fact else { return "本阶段未取得独立来源资料，未作判断" }
    if fact.providerID == nil, report?.providers != nil {
      return "资料来源：" + fact.source + "\n本阶段未取得该项有效证据；以各来源实际响应为准。"
    }
    let records =
      report?.facts.filter {
        $0.group == "exposure_detail" && $0.label == fact.label && $0.providerID == fact.providerID
      }.map(\.value) ?? []
    let lists =
      fact.label == "邮件 DNSBL"
      ? (report?.facts.filter { $0.group == "dnsbl" && $0.providerID == fact.providerID }
        .map { $0.label + "：" + factState($0.state) } ?? []) : []
    let numbers =
      report?.facts.filter {
        $0.group == "raw_risk" && $0.providerID == fact.providerID
      }.map { $0.label + "：" + $0.value + "（供应方策略值，不是概率或总体安全分）" } ?? []
    return
      ([
        "资料来源：" + fact.source, "供应方判断：" + factState(fact.state),
        providerDetails(report, id: fact.providerID),
      ] + records + lists + numbers + [
        fact.providerID == "internetdb" ? "InternetDB 每周更新；未提供本条扫描时间。" : "",
        "供应方资料，不是本机实时扫描；未标记不等于安全。",
      ])
      .joined(separator: "\n")
  }
}
