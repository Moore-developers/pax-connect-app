import Foundation
import Testing

@testable import PaxCore

@Suite("最近命中与连接限制") struct TrafficLatestMatchFormattingTests {
  private func match(
    _ rule: String, _ source: String = "Pax 包：Geo 基础包",
    applicationID: String = "", application: String = ""
  ) -> TrafficRoutingMatch {
    .init(
      at: Date(timeIntervalSince1970: 1_792_350_001), rule: rule, source: source,
      applicationID: applicationID, application: application, identity: "flow/2")
  }
  private func basis(_ rule: String, _ source: String, count: UInt64 = 0) -> TrafficBasis {
    var value = TrafficBasis()
    value.rule = rule
    value.source = source
    value.connections = count
    return value
  }
  @Test("最近命中只用冻结证据，旧历史即使有basis也不推断")
  func latestEvidence() {
    #expect(TrafficRuleFormatting.latestTitle(nil) == "最近命中未记录")
    let app = match(
      "application=com.google.Chrome", "Pax 包：优先规则",
      applicationID: "com.google.Chrome", application: "Google Chrome")
    #expect(TrafficRuleFormatting.latestTitle(app) == "优先规则：Google Chrome")
    let mismatch = match(
      "application=com.google.Chrome", "Pax 包：优先规则",
      applicationID: "com.tencent.xinWeChat", application: "WeChat")
    #expect(TrafficRuleFormatting.latestTitle(mismatch) == "优先规则：com.google.Chrome")
    for (rule, expected) in [
      ("source=中国域名 / IP 规则", "中国域名 / IP 规则"),
      ("rule_set=geoip-cn", "中国域名 / IP 规则"),
      ("builtin=局域网与私有网络地址", "局域网规则"),
      ("source=Google 服务经代理", "Google 服务规则"),
      ("final", "默认线路"),
    ] { #expect(TrafficRuleFormatting.latestTitle(match(rule)) == expected) }
    let structured = match(
      "{\"Package\":\"优先规则\",\"Method\":\"application\",\"Target\":\"com.google.Chrome\"}",
      applicationID: "com.google.Chrome", application: "Google Chrome")
    #expect(TrafficRuleFormatting.latestTitle(structured) == "优先规则：Google Chrome")
    #expect(
      TrafficRuleFormatting.latestTitle(match("builtin=局域网与私有网络地址", "Pax 包：自定义包"))
        == "自定义包：局域网与私有网络地址")
    let hover = TrafficRuleFormatting.latestEvidence(app)
    #expect(hover.contains("所选时间范围最近一次线路选择依据，不代表本行全部累计流量"))
    #expect(hover.contains(TrafficFormatting.dateAndClock(app.at).clock))
    #expect(hover.contains("application=com.google.Chrome") && hover.contains("Pax 包：优先规则"))
  }
  @Test("IPv6限制不能把用户主动阻断或混合路径解释成整应用未启用")
  func restrictionScope() {
    let rule = "builtin=IPv6 流量"
    let source = "Pax 包：Geo 基础包"
    var row = TrafficRow()
    row.direction = "blocked"
    row.bases = [basis(rule, source)]
    row.failedCount = 410
    #expect(TrafficFormatting.actualConnections(row) == "已阻断")
    #expect(TrafficRuleFormatting.latestTitle(match(rule)) == "IPv6 未启用")
    row.bases.append(basis("domain=example.com", "Pax 包：用户规则", count: 1))
    #expect(TrafficFormatting.actualConnections(row) == "已阻断")
    row.bases = [basis(rule, "Pax 包：自定义包", count: 1)]
    #expect(TrafficFormatting.actualConnections(row) == "已阻断")
  }
}
