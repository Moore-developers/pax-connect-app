import Foundation

/// Recorded absence has different meanings for access, DNS and older history.
public enum FlowPresentation {
  public static func target(_ flow: FlowFact) -> String { target(flow, context: .chinese) }
  public static func failure(_ code: String) -> String { failure(code, context: .chinese) }
  public static func application(_ flow: FlowFact) -> String {
    application(flow, context: .chinese)
  }
  public static func ip(_ flow: FlowFact) -> String { ip(flow, context: .chinese) }
  public static func domain(_ flow: FlowFact) -> String { domain(flow, context: .chinese) }
  public static func connection(_ flow: FlowFact) -> String { connection(flow, context: .chinese) }
  public static func actualConnection(_ flow: FlowFact) -> String {
    actualConnection(flow, context: .chinese)
  }
  public static func failure(_ flow: FlowFact) -> String? { failure(flow, context: .chinese) }
  public static func recordedStatus(_ flow: FlowFact?, fallback: String) -> String {
    recordedStatus(flow, fallback: fallback, context: .chinese)
  }
  public static func latencyReason(_ flow: FlowFact) -> String {
    latencyReason(flow, context: .chinese)
  }
  public static func target(_ flow: FlowFact, context: PresentationContext) -> String {
    if !flow.domain.isEmpty { return flow.domain }
    guard !flow.ip.isEmpty else { return context.text("flow.target_unobserved") }
    let address = flow.ip.contains(":") ? "[" + flow.ip + "]" : flow.ip
    return address + (flow.port > 0 ? ":\(flow.port)" : "")
  }
  public static func failure(_ code: String, context: PresentationContext) -> String {
    switch code {
    case "dns_failed", "dns_resolution_failed": return context.text("flow.dns_failed")
    case "dns_not_found": return context.text("flow.domain_missing")
    case "dns_server_failure": return context.text("flow.dns_server_failed")
    case "connection_timeout": return context.text("flow.connection_timeout")
    case "connection_refused": return context.text("flow.connection_refused")
    case "certificate_invalid", "tls_certificate_invalid":
      return context.text("flow.certificate_failed")
    case "network_unreachable": return context.text("flow.network_unreachable")
    case "rule_rejected": return context.text("flow.rule_blocked")
    default: return context.text("flow.failure_unknown")
    }
  }
  public static func application(_ flow: FlowFact, context: PresentationContext) -> String {
    if !flow.application.isEmpty { return flow.application }
    if flow.purpose == "dns" { return context.text("flow.dns_source_missing") }
    switch flow.processStatus {
    case "not_found": return context.text("flow.application_not_found")
    case "path_unavailable": return context.text("flow.process_unavailable")
    case "lookup_failed": return context.text("flow.application_lookup_failed")
    case "non_local": return context.text("flow.other_device")
    case "disabled": return context.text("flow.identification_disabled")
    default:
      return flow.purpose.isEmpty
        ? context.text("flow.legacy_application_missing") : context.text("flow.application_missing")
    }
  }

  public static func ip(_ flow: FlowFact, context: PresentationContext) -> String {
    if flow.purpose == "dns" { return context.text("flow.dns_not_applicable") }
    if !flow.ip.isEmpty { return flow.ip }
    if flow.ipSource == "proxy_domain" { return context.text("flow.proxy_ip_missing") }
    return flow.purpose.isEmpty
      ? context.text("flow.legacy_ip_missing") : context.text("flow.ip_missing")
  }

  public static func domain(_ flow: FlowFact, context: PresentationContext) -> String {
    if !flow.domain.isEmpty { return flow.domain }
    if flow.purpose == "dns" { return context.text("flow.legacy_query_missing") }
    return flow.ip.isEmpty
      ? (flow.purpose.isEmpty
        ? context.text("flow.legacy_domain_missing") : context.text("flow.domain_missing_capture"))
      : context.text("flow.direct_ip")
  }

  public static func connection(_ flow: FlowFact, context: PresentationContext) -> String {
    switch flow.direction {
    case "dns": return context.text("flow.pax_dns")
    case "direct": return context.text("flow.direct")
    case "proxy":
      if flow.routeName.hasPrefix("pax-node-") {
        return TrafficFormatting.actualConnection(
          direction: "proxy", route: flow.routeName, context: context)
      }
      return flow.routeName.isEmpty
        ? context.text("flow.proxy_legacy_name")
        : context.text("flow.proxy_prefix") + flow.routeName
    case "blocked": return context.text("flow.blocked")
    default:
      return flow.purpose == "dns"
        ? context.text("flow.dns_takeover")
        : flow.purpose.isEmpty
          ? context.text("flow.legacy_direction_missing")
          : context.text("flow.selection_incomplete")
    }
  }

  public static func actualConnection(_ flow: FlowFact, context: PresentationContext) -> String {
    if flow.purpose == "dns" { return "Pax DNS" }
    let rule =
      flow.rule == "final" || flow.rule.isEmpty
      ? "" : TrafficRuleFormatting.package(rule: flow.rule, source: flow.ruleSource)
    let suffix = rule.isEmpty ? "" : "：" + rule
    switch flow.direction {
    case "direct": return context.text("flow.direct") + suffix
    case "proxy":
      let ready =
        flow.responseKnown || flow.establishmentState == "ready"
        || flow.establishmentState == "response_ready"
      let name = TrafficFormatting.actualConnection(
        direction: "proxy", route: flow.routeName, context: context)
      return (ready ? "" : context.text("flow.selected_prefix")) + name + suffix
    case "blocked": return context.text("flow.blocked")
    default:
      return flow.purpose.isEmpty
        ? context.text("flow.legacy_connection_missing") : context.text("flow.route_unselected")
    }
  }
  public static func isIPv6Limited(_ flow: FlowFact) -> Bool {
    flow.direction == "blocked"
      && TrafficRuleFormatting.isIPv4OnlyRule(rule: flow.rule, source: flow.ruleSource)
  }
  private static func rejectionReason(_ flow: FlowFact, context: PresentationContext) -> String? {
    if isIPv6Limited(flow) { return context.text("flow.ipv6_not_connected") }
    guard flow.direction == "blocked", flow.failure == "rule_rejected",
      let route = TrafficRuleFormatting.unavailableRouteName(flow.rule)
    else { return nil }
    return context.text("flow.route_unavailable", ["route": route])
  }
  public static func failure(_ flow: FlowFact, context: PresentationContext) -> String? {
    rejectionReason(flow, context: context)
      ?? (flow.failure.isEmpty ? nil : failure(flow.failure, context: context))
  }
  public static func recordedStatus(
    _ flow: FlowFact?, fallback: String, context: PresentationContext
  ) -> String {
    if let flow, let rejection = rejectionReason(flow, context: context) { return rejection }
    guard context.language == .english, flow != nil, let key = recordedStatusKeys[fallback]
    else { return fallback }
    return context.text(key)
  }

  // The daemon supplies these fixed LogStatus labels in both current and legacy reads.
  private static let recordedStatusKeys: [String: String] = Dictionary(
    uniqueKeysWithValues: PresentationContext.chinese.messages.compactMap { key, message in
      guard key.hasPrefix("flow.status."), case .text(let title) = message else { return nil }
      return (title, key)
    })

  public static func latencyReason(_ flow: FlowFact, context: PresentationContext) -> String {
    if TrafficFormatting.responseLatency(flow) != nil {
      return flow.purpose == "dns"
        ? context.text("flow.query_timing") : context.text("flow.connection_timing")
    }
    if flow.stateUnknown { return context.text("flow.timing_gap") }
    if flow.purpose.isEmpty { return context.text("flow.legacy_timing_missing") }
    if !flow.failure.isEmpty { return context.text("flow.failed_without_response") }
    return flow.closedAt == nil
      ? context.text("flow.response_pending") : context.text("flow.closed_without_response")
  }
}
