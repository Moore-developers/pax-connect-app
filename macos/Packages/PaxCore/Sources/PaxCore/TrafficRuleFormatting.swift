import Foundation

/// Describes recorded matches. It never consults today's rules to reinterpret old flows.
public enum TrafficRuleFormatting {
  private static func evidence(_ rule: String) -> [String: String]? {
    guard rule.utf8.count <= 4096, let raw = rule.data(using: .utf8),
      let values = try? JSONDecoder().decode([String: String].self, from: raw),
      values["Package"] != nil
    else { return nil }
    return values
  }
  /// The old compiler labeled this built-in rule as Geo. Only exact recorded evidence qualifies.
  public static func isIPv4OnlyRule(rule: String, source: String) -> Bool {
    if let values = evidence(rule) {
      return values["Method"] == "builtin" && values["Target"] == "IPv6 流量"
        && ["Geo 基础包", "Pax 内置规则"].contains(values["Package"] ?? "")
    }
    return rule == "builtin=IPv6 流量"
      && ["Pax 包：Geo 基础包", "Pax 包：Pax 内置规则"].contains(source)
  }
  public static let ipv6Limitation =
    "这次尝试使用 IPv6，但该次 Pax 连接仅使用 IPv4，因此未建立连接。其它 IPv4 记录单独统计，不代表应用整体被封禁。"

  /// Only the frozen match supplies identity; neither row names nor the current app catalog do.
  public static func latestTitle(_ match: TrafficRoutingMatch?) -> String {
    guard let match else { return "最近命中未记录" }
    let rule = match.rule
    let source = match.source
    if isIPv4OnlyRule(rule: rule, source: source) { return "IPv6 未启用" }
    if rule == "final" { return "默认线路" }
    let values = evidence(rule)
    let method = values?["Method"]
    let target = values?["Target"] ?? ""
    let package = values?["Package"] ?? self.package(rule: rule, source: source)
    if package == "优先规则" {
      let applicationID =
        method == "application"
        ? target
        : rule.hasPrefix("application=") ? String(rule.dropFirst("application=".count)) : ""
      if !applicationID.isEmpty {
        return "优先规则："
          + (applicationID == match.applicationID && !match.application.isEmpty
            ? match.application : applicationID)
      }
      return "优先规则：" + condition(rule: rule, source: source)
    }
    let normalized = rule.replacingOccurrences(of: "geosite_cn", with: "geosite-cn")
      .replacingOccurrences(of: "geoip_cn", with: "geoip-cn")
    let recordedPackage =
      values?["Package"]
      ?? (source.hasPrefix("Pax 包：") ? String(source.dropFirst("Pax 包：".count)) : "")
    let legacyBuiltin = values == nil && ["", "内核规则"].contains(source)
    let chinaSource =
      ["Geo 基础包", "geosite-cn", "geoip-cn"].contains(recordedPackage) || legacyBuiltin
    let privateSource = recordedPackage == "Geo 基础包" || legacyBuiltin
    let googleSource = ["Geo 基础包", "geosite-google"].contains(recordedPackage) || legacyBuiltin
    if chinaSource
      && ([
        "rule_set=geosite-cn", "rule_set=[geosite-cn]", "rule_set=geoip-cn", "rule_set=[geoip-cn]",
      ].contains(normalized)
        || (["source", "builtin"].contains(method ?? "") && target == "中国域名 / IP 规则")
        || ["source=中国域名 / IP 规则", "builtin=中国域名 / IP 规则"].contains(rule))
    {
      return "中国域名 / IP 规则"
    }
    if privateSource
      && (["ip_is_private", "ip_is_private=true", "builtin=局域网与私有网络地址"].contains(rule)
        || (method == "builtin" && target == "局域网与私有网络地址"))
    {
      return "局域网规则"
    }
    if googleSource
      && ([
        "rule_set=geosite-google", "rule_set=[geosite-google]", "source=Google 服务经代理",
        "builtin=Google 服务经代理",
      ].contains(rule)
        || (["builtin", "source"].contains(method ?? "") && target == "Google 服务经代理"))
    {
      return "Google 服务规则"
    }
    guard !rule.isEmpty else { return "最近命中未记录" }
    return (recordedPackage.isEmpty ? "" : recordedPackage + "：")
      + condition(rule: rule, source: source)
  }
  public static func latestEvidence(_ match: TrafficRoutingMatch?) -> String {
    let scope = "所选时间范围最近一次线路选择依据，不代表本行全部累计流量"
    guard let match else { return scope + "\n最近命中未记录" }
    let time = TrafficFormatting.dateAndClock(match.at)
    return [
      scope, time.date + " " + time.clock, latestTitle(match),
      "触发条件：" + condition(rule: match.rule, source: match.source),
      "记录的规则：" + match.rule, "记录的来源：" + match.source,
    ].joined(separator: "\n")
  }
  public static func package(rule: String, source: String) -> String {
    if isIPv4OnlyRule(rule: rule, source: source) { return "Pax 内置规则" }
    if let values = evidence(rule), let name = values["Package"] { return name }
    let value = rule.trimmingCharacters(in: .whitespacesAndNewlines).replacingOccurrences(
      of: "geosite_cn", with: "geosite-cn"
    ).replacingOccurrences(of: "geoip_cn", with: "geoip-cn")
    if rule.isEmpty { return "未采集规则" }
    if rule == "final" { return "无" }
    if !source.hasPrefix("Pax 包："),
      ["rule_set=geosite-cn", "rule_set=[geosite-cn]"].contains(value)
    {
      return "geosite-cn"
    }
    if !source.hasPrefix("Pax 包："), ["rule_set=geoip-cn", "rule_set=[geoip-cn]"].contains(value) {
      return "geoip-cn"
    }
    if !source.hasPrefix("Pax 包："), ["ip_is_private", "ip_is_private=true"].contains(value) {
      return "Geo 基础包"
    }
    if source.hasPrefix("Pax 包：") { return String(source.dropFirst("Pax 包：".count)) }
    return source.isEmpty || source == "内核规则" ? "历史规则（未记录包名）" : source
  }
  public static func summaryPackage(rule: String, source: String) -> String {
    if isIPv4OnlyRule(rule: rule, source: source) {
      return package(rule: rule, source: source)
    }
    if let values = evidence(rule), values["Method"] == "builtin", values["Target"] == "IPv6 流量" {
      return package(rule: rule, source: source)
    }
    let builtin = [
      "rule_set=geosite-google", "rule_set=[geosite-google]", "ip_is_private", "ip_is_private=true",
      "rule_set=geosite-cn", "rule_set=[geosite-cn]",
      "rule_set=geoip-cn", "rule_set=[geoip-cn]",
    ]
    if source == "Pax 包：Geo 基础包"
      || (["Pax 包：geosite-cn", "Pax 包：geoip-cn", "Pax 包：geosite-google"].contains(source)
        && (rule.hasPrefix("source=") || rule.hasPrefix("builtin=")))
      || (["", "内核规则"].contains(source) && builtin.contains(rule))
    {
      return "Geo 基础包"
    }
    return package(rule: rule, source: source)
  }
  public static func condition(rule: String, source: String = "") -> String {
    if let values = evidence(rule) {
      let target = values["Target"] ?? "条件未记录"
      switch values["Method"] {
      case "domain": return "精确域名 " + target
      case "domain_suffix": return "域名及子域 " + target
      case "ip", "cidr": return "IP / 网段 " + target
      case "application": return "应用身份 " + target
      default: return target
      }
    }
    let value = rule.trimmingCharacters(in: .whitespacesAndNewlines).replacingOccurrences(
      of: "geosite_cn", with: "geosite-cn"
    ).replacingOccurrences(of: "geoip_cn", with: "geoip-cn")
    if rule.isEmpty { return "历史没有保存命中依据" }
    if rule == "final" { return "未命中任何规则，走当前线路" }
    if !source.hasPrefix("Pax 包："),
      ["rule_set=geosite-cn", "rule_set=[geosite-cn]"].contains(value)
    {
      return "中国域名"
    }
    if !source.hasPrefix("Pax 包："), ["rule_set=geoip-cn", "rule_set=[geoip-cn]"].contains(value) {
      return "中国 IP"
    }
    if !source.hasPrefix("Pax 包："), ["ip_is_private", "ip_is_private=true"].contains(value) {
      return "局域网及私有地址"
    }
    for prefix in ["builtin=", "source="] {
      if rule.hasPrefix(prefix) { return String(rule.dropFirst(prefix.count)) }
    }
    if rule.hasPrefix("application=") {
      return "应用身份 " + String(rule.dropFirst("application=".count))
    }
    var result = rule
    for (key, title) in [
      ("domain_suffix=", "域名及子域 "), ("domain=", "域名 "), ("ip_cidr=", "IP / 网段 "),
      ("process_path_regex=", "应用路径匹配 "), ("process_path=", "应用路径 "), ("process_name=", "进程 "),
    ] { result = result.replacingOccurrences(of: key, with: title) }
    return result
  }
}
