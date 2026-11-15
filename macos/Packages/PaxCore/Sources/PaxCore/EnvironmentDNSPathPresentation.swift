import Foundation
import PaxClient

extension EnvironmentDiagnosticPresentation {
  private struct DNSCoverage: Decodable {
    let expected, observed, unverified: Int
  }
  private struct DNSUpstream: Decodable {
    let server, outbound, pool: String
    let route: String?
  }

  private static func dnsPathState(_ sample: EnvironmentDiagnosticReport.Sample) -> String {
    guard let check = sample.checks?.first(where: { $0.kind == "dns_path" }),
      let coverage = try? JSONDecoder().decode(DNSCoverage.self, from: Data(check.value.utf8)),
      coverage.expected == 12, (0...12).contains(coverage.observed), coverage.unverified >= 0
    else { return "unknown" }
    if check.state == "bypass" { return "bypass" }
    guard coverage.observed == 12, coverage.unverified == 0,
      ["proxy", "policy_direct", "mixed"].contains(check.state)
    else { return "unknown" }
    return check.state
  }

  /// Only exact-query transport observations can confirm a path; countries cannot.
  public static func dnsPathAssessment(_ report: EnvironmentDiagnosticReport, waiting: Bool)
    -> NetworkDiagnosticPresentation.Assessment
  {
    let mac = dnsPathState(report.mac)
    let browser = dnsPathState(report.browser)
    if [mac, browser].contains("bypass") {
      return .init(text: "发现绕过代理 · 本轮海外 DNS 请求直接出站", tone: .difference)
    }
    if mac == "proxy" && browser == "proxy" {
      return .init(text: "本次 DNS 请求已确认经代理 · Mac 与浏览器均经代理", tone: .matching)
    }
    if mac != "unknown" && browser != "unknown" {
      return .init(
        text: mac == "policy_direct" && browser == "policy_direct"
          ? "按 DNS 分流规则直连 · 本轮使用国内加密 DNS"
          : "请求路径已确认 · 本轮包含代理与按规则直连的 DNS", tone: .information)
    }
    if mac == "proxy" || browser == "proxy" {
      return .init(
        text: "部分已确认经代理 · " + (mac == "proxy" ? "浏览器" : "Mac") + "请求路径未确认",
        tone: .pending)
    }
    return .init(
      text: waiting ? "正在核对 DNS 请求路径" : "请求路径未确认 · 尚无完整的本轮 DNS 传输证据",
      tone: .pending)
  }

  public static func dnsPathDetails(_ sample: EnvironmentDiagnosticReport.Sample, name: String)
    -> String
  {
    var lines = [name + " DNS 请求路径"]
    if let check = sample.checks?.first(where: { $0.kind == "dns_path" }),
      let coverage = try? JSONDecoder().decode(DNSCoverage.self, from: Data(check.value.utf8)),
      coverage.expected == 12, (0...12).contains(coverage.observed), coverage.unverified >= 0
    {
      let state: String
      switch dnsPathState(sample) {
      case "proxy": state = "本轮解析已确认经代理路径处理"
      case "policy_direct": state = "按 DNS 分流规则直连"
      case "mixed": state = "经代理与按规则直连"
      case "bypass": state = "海外 DNS 请求绕过代理"
      default: state = "请求路径未确认"
      }
      lines.append(state + " · 已关联 \(coverage.observed)/12 个测试域名")
      if coverage.unverified > 0 { lines.append("另有 \(coverage.unverified) 条查询缺少上游响应证据") }
      if check.state == "unavailable" { lines.append("Pax 未接管或本轮路径观测不可用") }
    } else {
      lines.append("旧记录未采集或本轮尚无路径证据")
    }
    for check in sample.checks ?? [] where check.kind == "dns_upstream" {
      guard let upstream = try? JSONDecoder().decode(DNSUpstream.self, from: Data(check.value.utf8))
      else { continue }
      let direction: String
      switch check.state {
      case "proxy": direction = "经代理"
      case "remote_proxy":
        let route = upstream.route.flatMap { $0.isEmpty ? nil : $0 } ?? "代理线路"
        lines.append("域名已交由「" + route + "」解析 · 已收到代理连接响应；具体解析器由代理端选择")
        continue
      case "policy_direct": direction = "按规则直连"
      case "bypass": direction = "绕过代理"
      default: continue
      }
      lines.append(
        direction + " · " + (upstream.pool == "domestic" ? "国内" : "海外") + "加密 DNS · "
          + upstream.server + " · 出站 " + upstream.outbound)
    }
    lines.append("仅对应本轮测试域名；本机 DNS 上游响应与交由代理解析分别记录，未关联到的请求仍保留未知。")
    return lines.joined(separator: "\n")
  }
}
