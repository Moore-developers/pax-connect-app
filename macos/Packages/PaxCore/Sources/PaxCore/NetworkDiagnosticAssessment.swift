import Foundation
import PaxClient

/// Conclusions describe the observation that supports them, never overall safety.
extension NetworkDiagnosticPresentation {
  public struct Assessment: Sendable {
    public let text: String
    public let tone: EnvironmentDiagnosticPresentation.Tone
  }

  public static func serviceAssessment(_ probe: NetworkDiagnosticReport.Probe?, api: Bool)
    -> Assessment
  {
    guard let probe else { return Assessment(text: "尚无检测结果", tone: .pending) }
    let result: String
    let tone: EnvironmentDiagnosticPresentation.Tone
    switch probe.httpStatus {
    case 200..<300:
      result = api ? "API 已响应，权限未验" : "网页已响应，账号未验"
      tone = .information
    case 300..<400:
      result = "目标要求跳转"
      tone = .information
    case 401:
      result = (api ? "API" : "目标") + "已响应，需认证"
      tone = .information
    case 403:
      result = "本次请求被拒绝"
      tone = .difference
    case 429:
      result = "本次请求被限流"
      tone = .difference
    case 500..<600:
      result = "目标服务器报错"
      tone = .failure
    case 1..<200, 400..<500:
      result = "目标返回非预期响应"
      tone = .difference
    default:
      return Assessment(
        text: problem(probe.errorCode), tone: probe.state == "not_started" ? .pending : .failure)
    }
    return Assessment(text: result + " · \(probe.httpStatus)", tone: tone)
  }

  public static func serviceOverview(_ probes: [NetworkDiagnosticReport.Probe]) -> Assessment {
    guard !probes.isEmpty else { return Assessment(text: "尚无服务端点结果", tone: .pending) }
    let total = max(12, probes.count)
    let responded = probes.filter { $0.httpStatus > 0 }.count
    let refused = probes.filter { $0.httpStatus == 403 }.count
    let auth = probes.filter { $0.httpStatus == 401 }.count
    let limited = probes.filter { $0.httpStatus == 429 }.count
    let server = probes.filter { $0.httpStatus >= 500 }.count
    let missing = total - responded
    var parts = ["\(responded)/\(total) 项已响应"]
    for (count, label) in [
      (auth, "需认证"), (refused, "拒绝请求"), (limited, "限流"), (server, "服务端报错"), (missing, "未取得响应"),
    ] where count > 0 {
      parts.append("\(count) 项" + label)
    }
    return Assessment(
      text: parts.joined(separator: "；"),
      tone: missing > 0 || server > 0
        ? .failure : refused > 0 || limited > 0 ? .difference : .information)
  }

  public static func sourceAssessment(
    _ source: NetworkDiagnosticReport.Source, all: [NetworkDiagnosticReport.Source]
  ) -> Assessment {
    let missing = [("国家", source.country), ("城市", source.city), ("ASN", source.asn)].filter {
      $0.1.isEmpty
    }.map(\.0)
    let conflicts = [
      (source.country, all.map(\.country)), (source.city, all.map(\.city)),
      (normalizedASN(source.asn), all.map { normalizedASN($0.asn) }),
    ]
    .contains { value, others in !value.isEmpty && others.contains { !$0.isEmpty && $0 != value } }
    let location = [source.country.isEmpty ? "" : countryName(source.country), source.city].filter {
      !$0.isEmpty
    }.joined(separator: " · ")
    var parts = [location].filter { !$0.isEmpty }
    if conflicts { parts.append("与其他来源有分歧") }
    if !missing.isEmpty { parts.append("缺" + missing.joined(separator: "/")) }
    if parts.isEmpty { parts = ["来源未提供定位字段"] }
    return Assessment(
      text: parts.joined(separator: "；"),
      tone: conflicts ? .difference : missing.isEmpty ? .information : .pending)
  }

  public static func sourceOverview(_ sources: [NetworkDiagnosticReport.Source]) -> Assessment {
    guard !sources.isEmpty else { return Assessment(text: "尚无定位资料，无法交叉核对", tone: .pending) }
    let groups = [
      ("国家", sources.map(\.country)), ("城市", sources.map(\.city)),
      ("ASN", sources.map { normalizedASN($0.asn) }),
    ]
    let conflicts = groups.filter { Set($0.1.filter { !$0.isEmpty }).count > 1 }.map(\.0)
    let incomplete = sources.filter { $0.country.isEmpty || $0.city.isEmpty || $0.asn.isEmpty }
      .count
    let compared = groups.filter { $0.1.filter { !$0.isEmpty }.count >= 2 }.count
    let direct = Set(sources.compactMap(\.providerID).filter { $0 != "ipx.al" }).count
    let fallback = sources.contains { $0.providerID == "ipx.al" }
    var parts = ["\(sources.count) 条来源资料"]
    if sources.allSatisfy({ $0.providerID == nil }) { parts.append("旧记录未记录获取渠道") }
    if direct > 0 { parts.append("\(direct) 家直接查询") }
    if fallback { parts.append("含 ipx.al 补充转交资料") }
    if !conflicts.isEmpty {
      parts.append(conflicts.joined(separator: "/") + "存在分歧")
    } else {
      parts.append(compared > 0 ? "可对照字段未见分歧" : "资料不足，无法交叉核对")
    }
    if incomplete > 0 { parts.append("\(incomplete) 源字段不全") }
    return Assessment(
      text: parts.joined(separator: "；"), tone: conflicts.isEmpty ? .information : .difference)
  }

  public static func riskAssessment(_ fact: NetworkDiagnosticFact) -> Assessment {
    let label = fact.label
    switch fact.state {
    case "hit":
      let text: String
      switch label {
      case "开放端口": text = "公开资料记录开放端口"
      case "已知漏洞": text = "公开资料关联漏洞，尚未实测验证"
      case "自签证书": text = "公开资料记录自签证书"
      case "人类流量 <50%": text = "来源估计人类流量低于 50%"
      case "代理风险 ≥67": text = "来源风险值达到 67"
      default: text = "来源标记为" + label
      }
      return Assessment(text: text, tone: .difference)
    case "clear":
      return Assessment(
        text: ["开放端口", "已知漏洞", "Shodan 标签"].contains(label)
          ? "该源已收录资料中未列出此项" : "来源未标记此项", tone: .information)
    default:
      return Assessment(
        text: fact.source.contains("未配置") ? "来源未配置，未检测" : "独立资料不足，未作判断", tone: .pending)
    }
  }

  public static func riskOverview(_ facts: [NetworkDiagnosticFact]) -> Assessment {
    guard !facts.isEmpty else { return Assessment(text: "缺少风险资料，不能判断", tone: .pending) }
    let hits = facts.filter { $0.state == "hit" }.map(\.label)
    let unknown = facts.filter { !["hit", "clear", "information"].contains($0.state) }.count
    var parts = [
      hits.isEmpty
        ? (unknown == facts.count ? "来源尚无有效判断" : "已返回判断中未见标记")
        : "来源标记：" + hits.joined(separator: "、")
    ]
    if unknown > 0 { parts.append("\(unknown) 项待确认") }
    parts.append("非实时扫描或账号风控结论")
    return Assessment(
      text: parts.joined(separator: "；"), tone: hits.isEmpty ? .information : .difference)
  }

  static func normalizedASN(_ value: String) -> String {
    value.uppercased().replacingOccurrences(of: "AS", with: "").trimmingCharacters(in: .whitespaces)
  }
}
