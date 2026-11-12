import Testing

@testable import PaxCore

@Suite("观测缺失的含义") struct FlowPresentationTests {
  @Test("代理地址不能冒充目标地址；IP访问不显示域名识别失败")
  func destinationMeaning() {
    var flow = FlowFact()
    #expect(FlowPresentation.ip(flow) == "历史未采集目标 IP")
    flow.purpose = "access"
    flow.ipSource = "proxy_domain"
    #expect(FlowPresentation.ip(flow) == "代理未提供目标 IP")
    flow.ip = "192.0.2.1"
    #expect(FlowPresentation.domain(flow) == "直接访问 IP")
    flow.ip = "2001:db8::1"
    flow.port = 443
    #expect(FlowPresentation.target(flow) == "[2001:db8::1]:443")
    flow.purpose = "dns"
    #expect(FlowPresentation.ip(flow) == "不适用 · DNS 查询")
  }
  @Test("旧计时、尚无回包及采集缺口分别说明")
  func timingMeaning() {
    var flow = FlowFact()
    #expect(FlowPresentation.latencyReason(flow) == "历史未采集响应计时")
    flow.purpose = "access"
    #expect(FlowPresentation.latencyReason(flow) == "尚无首个回包")
    flow.stateUnknown = true
    #expect(FlowPresentation.latencyReason(flow).contains("缺口"))
    flow.responseKnown = true
    flow.responseMS = 0
    #expect(TrafficFormatting.responseLatency(flow) == 0)
  }
}

extension FlowPresentationTests {
  @Test("IPv6内置阻断主表与详情使用历史真实依据")
  func ipv4OnlyAttribution() {
    for rule in [
      "builtin=IPv6 流量", "{\"Package\":\"Geo 基础包\",\"Method\":\"builtin\",\"Target\":\"IPv6 流量\"}",
      "{\"Package\":\"Pax 内置规则\",\"Method\":\"builtin\",\"Target\":\"IPv6 流量\"}",
    ] {
      for source in ["Pax 包：Geo 基础包", "Pax 包：Pax 内置规则"] {
        var flow = FlowFact()
        flow.direction = "blocked"
        flow.rule = rule
        flow.ruleSource = source
        #expect(FlowPresentation.actualConnection(flow) == "已阻断")
        #expect(TrafficRuleFormatting.package(rule: rule, source: flow.ruleSource) == "Pax 内置规则")
        #expect(
          TrafficRuleFormatting.summaryPackage(rule: rule, source: flow.ruleSource) == "Pax 内置规则")
        var detail = HistoryLogDetail()
        detail.flow = flow
        let rows = LogDetailPresentation(detail: detail, perspective: .application).sections
          .flatMap(
            \.rows)
        #expect(rows.contains { $0 == "规则包" && $1 == "Pax 内置规则" })
        #expect(rows.contains { $0 == "触发条件" && $1 == "IPv6 流量" })
        #expect(rows.contains { $0 == "IPv6 未启用" && $1 == TrafficRuleFormatting.ipv6Limitation })
        #expect(rows.contains { $0 == "原始规则" && $1 == rule })
        #expect(FlowPresentation.recordedStatus(flow, fallback: "已阻断") == "未连接：IPv6 未启用")
      }
    }
    for (rule, source, expected) in [
      ("builtin=IPv6 流量", "Pax 包：自定义包", "自定义包"),
      ("builtin=IPv6 流量", "内核规则", "历史规则（未记录包名）"),
      ("builtin=IPv6 流量扩展", "Pax 包：Geo 基础包", "Geo 基础包"),
      (
        "{\"Package\":\"自定义包\",\"Method\":\"builtin\",\"Target\":\"IPv6 流量\"}", "Pax 包：Geo 基础包",
        "自定义包"
      ),
      ("domain=IPv6 流量", "Pax 包：Geo 基础包", "Geo 基础包"),
      ("ip_is_private", "内核规则", "Geo 基础包"),
      ("source=Google 服务经代理", "Pax 包：geosite-google", "geosite-google"),
    ] {
      var flow = FlowFact()
      flow.direction = "blocked"
      flow.rule = rule
      flow.ruleSource = source
      #expect(FlowPresentation.actualConnection(flow) == "已阻断")
      #expect(
        TrafficRuleFormatting.summaryPackage(rule: rule, source: source)
          == (source == "Pax 包：geosite-google" ? "Geo 基础包" : expected))
    }
  }
}
