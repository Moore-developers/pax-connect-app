import Foundation

/// Recorded absence has different meanings for access, DNS and older history.
public enum FlowPresentation {
  public static func target(_ flow: FlowFact) -> String {
    if !flow.domain.isEmpty { return flow.domain }
    guard !flow.ip.isEmpty else { return "目标未采集" }
    let address = flow.ip.contains(":") ? "[" + flow.ip + "]" : flow.ip
    return address + (flow.port > 0 ? ":\(flow.port)" : "")
  }
  public static func failure(_ code: String) -> String {
    switch code {
    case "dns_failed", "dns_resolution_failed": return "域名解析失败"
    case "dns_not_found": return "域名不存在"
    case "dns_server_failure": return "DNS 服务返回失败"
    case "connection_timeout": return "连接超时"
    case "connection_refused": return "目标拒绝连接"
    case "certificate_invalid", "tls_certificate_invalid": return "证书校验失败"
    case "network_unreachable": return "网络不可达"
    case "rule_rejected": return "被规则阻断"
    default: return "连接失败，具体原因未确认"
    }
  }
  public static func application(_ flow: FlowFact) -> String {
    if !flow.application.isEmpty { return flow.application }
    if flow.purpose == "dns" { return "DNS 查询 · 来源应用未采集" }
    switch flow.processStatus {
    case "not_found": return "未找到所属应用"
    case "path_unavailable": return "进程路径不可用"
    case "lookup_failed": return "应用归属查询失败"
    case "non_local": return "其它设备流量"
    case "disabled": return "未启用应用识别"
    default: return flow.purpose.isEmpty ? "历史未采集应用" : "来源应用未采集"
    }
  }

  public static func ip(_ flow: FlowFact) -> String {
    if flow.purpose == "dns" { return "不适用 · DNS 查询" }
    if !flow.ip.isEmpty { return flow.ip }
    if flow.ipSource == "proxy_domain" { return "代理未提供目标 IP" }
    return flow.purpose.isEmpty ? "历史未采集目标 IP" : "未采集目标 IP"
  }

  public static func domain(_ flow: FlowFact) -> String {
    if !flow.domain.isEmpty { return flow.domain }
    if flow.purpose == "dns" { return "历史未采集查询域名" }
    return flow.ip.isEmpty ? (flow.purpose.isEmpty ? "历史未采集域名" : "未采集域名") : "直接访问 IP"
  }

  public static func connection(_ flow: FlowFact) -> String {
    switch flow.direction {
    case "dns": return "Pax DNS 解析"
    case "direct": return "直连"
    case "proxy": return flow.routeName.isEmpty ? "代理 · 历史未采集线路名称" : "代理 · " + flow.routeName
    case "blocked": return "已阻断"
    default: return flow.purpose == "dns" ? "DNS 接管" : flow.purpose.isEmpty ? "历史未记录去向" : "未完成线路选择"
    }
  }

  public static func actualConnection(_ flow: FlowFact) -> String {
    if flow.purpose == "dns" { return "Pax DNS" }
    let rule =
      flow.rule == "final" || flow.rule.isEmpty
      ? "" : TrafficRuleFormatting.package(rule: flow.rule, source: flow.ruleSource)
    let suffix = rule.isEmpty ? "" : "：" + rule
    switch flow.direction {
    case "direct": return "直连" + suffix
    case "proxy":
      let ready =
        flow.responseKnown || flow.establishmentState == "ready"
        || flow.establishmentState == "response_ready"
      let name = flow.routeName.isEmpty ? "历史未记录线路" : flow.routeName
      return (ready ? "" : "已选") + name + suffix
    case "blocked": return "已阻断"
    default: return flow.purpose.isEmpty ? "历史未记录连接方式" : "尚未选定线路"
    }
  }
  public static func isIPv6Limited(_ flow: FlowFact) -> Bool {
    flow.direction == "blocked"
      && TrafficRuleFormatting.isIPv4OnlyRule(rule: flow.rule, source: flow.ruleSource)
  }
  public static func recordedStatus(_ flow: FlowFact?, fallback: String) -> String {
    flow.map(isIPv6Limited) == true ? "未连接：IPv6 未启用" : fallback
  }
  public static func latencyReason(_ flow: FlowFact) -> String {
    if TrafficFormatting.responseLatency(flow) != nil {
      return flow.purpose == "dns" ? "查询开始至解析结果" : "开始拨号至首个回包，非 HTTP 请求耗时"
    }
    if flow.stateUnknown { return "采集有缺口，未保存完整响应计时" }
    if flow.purpose.isEmpty { return "历史未采集响应计时" }
    if !flow.failure.isEmpty { return "连接失败，未观测到有效响应" }
    return flow.closedAt == nil ? "尚无首个回包" : "关闭前未观测到回包"
  }
}
