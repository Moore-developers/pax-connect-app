import Foundation
import PaxCore

/// Historical evidence only; no current configuration or inferred expected route.
extension WorkbenchTrafficRecord {
  var recordedRuleMissing: Bool {
    hit.condition.isEmpty || hit.condition == "未记录"
  }
  var ipv6Limited: Bool {
    ["blocked", "已阻断", "规则阻断", "IPv6 未启用"].contains(direction)
      && TrafficRuleFormatting.isIPv4OnlyRule(rule: hit.condition, source: hit.source)
  }
  var recordedRuleTitle: String {
    if ipv6Limited { return "IPv6 未启用" }
    if recordedRuleMissing { return "规则未记录" }
    let name = TrafficRuleFormatting.package(rule: hit.condition, source: hit.source)
    if ["geosite-google", "geosite-cn", "geoip-cn", "Geo 基础包"].contains(name) {
      return TrafficRuleFormatting.condition(rule: hit.condition, source: hit.source)
    }
    return name
  }
  var connectionRuleTitle: String {
    recordedRuleTitle.replacingOccurrences(of: "规则", with: "").trimmingCharacters(
      in: .whitespacesAndNewlines)
  }
  var actualConnection: String {
    if ipv6Limited { return "已阻断" }
    return TrafficFormatting.actualConnection(direction: direction, route: route)
  }
  var connectionAddress: String {
    guard let port else { return ip }
    let host = ip.contains(":") && !ip.hasPrefix("[") ? "[" + ip + "]" : ip
    return host + ":\(port)"
  }
  var connectionTime: (date: String, clock: String) {
    TrafficFormatting.dateAndClock(started)
  }
  var connectionResult: String {
    if let failure { return failure }
    return stateUnknown ? "状态未知" : ended ? "已结束" : "活动中"
  }
}
