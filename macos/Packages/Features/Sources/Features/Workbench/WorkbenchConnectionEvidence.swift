import Foundation
import PaxCore

/// Historical evidence only; no current configuration or inferred expected route.
extension WorkbenchTrafficRecord {
  var exitIdentity: String {
    actualConnectionKey.isEmpty ? direction + "\t" + routeID + "\t" + route : actualConnectionKey
  }
  var ruleIdentity: String {
    hit.sourceKey ?? String(hit.source.utf8.count) + ":" + hit.source + hit.condition
  }
  var recordedRuleMissing: Bool {
    hit.condition.isEmpty || hit.condition == "未记录"
  }
  var ipv6Limited: Bool {
    (directionCode == "blocked" || ["blocked", "已阻断", "规则阻断", "IPv6 未启用"].contains(direction))
      && TrafficRuleFormatting.isIPv4OnlyRule(rule: hit.condition, source: hit.source)
  }
  var recordedRuleTitle: String { recordedRuleTitle(context: .chinese) }
  func recordedRuleTitle(context: PresentationContext) -> String {
    if ipv6Limited { return context.text("traffic_series.96d388c1b2") }
    if recordedRuleMissing { return context.text("traffic_series.340e2ffc30") }
    let name = TrafficRuleFormatting.package(
      rule: hit.condition, source: hit.source, context: context)
    if ["geosite-google", "geosite-cn", "geoip-cn", "Geo 基础包"].contains(name) {
      return TrafficRuleFormatting.condition(
        rule: hit.condition, source: hit.source, context: context)
    }
    return name
  }
  var connectionRuleTitle: String { connectionRuleTitle(context: .chinese) }
  func connectionRuleTitle(context: PresentationContext) -> String {
    recordedRuleTitle(context: context)
  }
  var actualConnection: String { actualConnection(context: .chinese) }
  func actualConnection(context: PresentationContext) -> String {
    if ipv6Limited { return context.text("traffic_series.858717dc95") }
    return TrafficFormatting.actualConnection(direction: direction, route: route, context: context)
  }
  var connectionAddress: String {
    guard let port else { return ip }
    let host = ip.contains(":") && !ip.hasPrefix("[") ? "[" + ip + "]" : ip
    return host + ":\(port)"
  }
  var connectionTime: (date: String, clock: String) {
    TrafficFormatting.dateAndClock(started)
  }
  var connectionResult: String { connectionResult(context: .chinese) }
  func connectionResult(context: PresentationContext) -> String {
    if let failure { return failure }
    return stateUnknown
      ? context.text("traffic_series.93e474c68e")
      : ended
        ? context.text("traffic_series.d79b1d0e5c") : context.text("traffic_series.3c01089ea0")
  }
}
