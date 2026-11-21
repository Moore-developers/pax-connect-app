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
  static func unavailableRouteName(_ rule: String) -> String? {
    guard let name = evidence(rule)?["UnavailableRoute"], !name.isEmpty else { return nil }
    return name
  }
  public static func recordedRuleText(_ rule: String) -> String {
    guard rule.hasPrefix("{"), let values = evidence(rule), values["UnavailableRoute"] != nil,
      let method = values["Method"], let target = values["Target"]
    else { return rule }
    return method + "=" + target
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
  public static var ipv6Limitation: String { ipv6Limitation(context: .chinese) }
  public static func ipv6Limitation(context: PresentationContext) -> String {
    context.text("traffic_format.b25d11dbd2")
  }

  /// Only the frozen match supplies identity; neither row names nor the current app catalog do.
  public static func latestTitle(
    _ match: TrafficRoutingMatch?, context: PresentationContext = .chinese
  ) -> String {
    guard let match else { return context.text("traffic_format.b0c3b0dd64") }
    let rule = match.rule
    let source = match.source
    if isIPv4OnlyRule(rule: rule, source: source) {
      return context.text("traffic_format.96d388c1b2")
    }
    if rule == "final" { return context.text("traffic_format.6b1918f8e3") }
    let values = evidence(rule)
    let method = values?["Method"]
    let target = values?["Target"] ?? ""
    let package =
      values?["Package"]
      ?? (source.hasPrefix("Pax 包：") ? String(source.dropFirst("Pax 包：".count)) : source)
    if package == "优先规则" {
      let applicationID =
        method == "application"
        ? target
        : rule.hasPrefix("application=") ? String(rule.dropFirst("application=".count)) : ""
      if !applicationID.isEmpty {
        return context.text("traffic_format.b9a2180514")
          + (applicationID == match.applicationID && !match.application.isEmpty
            ? match.application : applicationID)
      }
      return context.text("traffic_format.b9a2180514")
        + condition(rule: rule, source: source, context: context)
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
      return context.text("traffic_format.1096e4f64f")
    }
    if privateSource
      && (["ip_is_private", "ip_is_private=true", "builtin=局域网与私有网络地址"].contains(rule)
        || (method == "builtin" && target == "局域网与私有网络地址"))
    {
      return context.text("traffic_format.ade12b8aa7")
    }
    if googleSource
      && ([
        "rule_set=geosite-google", "rule_set=[geosite-google]", "source=Google 服务经代理",
        "builtin=Google 服务经代理",
      ].contains(rule)
        || (["builtin", "source"].contains(method ?? "") && target == "Google 服务经代理"))
    {
      return context.text("traffic_format.0fb42c8009")
    }
    guard !rule.isEmpty else { return context.text("traffic_format.b0c3b0dd64") }
    return (recordedPackage.isEmpty ? "" : recordedPackage + "：")
      + condition(rule: rule, source: source, context: context)
  }
  public static func latestEvidence(
    _ match: TrafficRoutingMatch?, context: PresentationContext = .chinese
  ) -> String {
    let scope = context.text("traffic_format.f304c496af")
    guard let match else { return scope + context.text("traffic_format.ba747ac648") }
    let time = TrafficFormatting.dateAndClock(match.at)
    return [
      scope, time.date + " " + time.clock, latestTitle(match, context: context),
      context.text("traffic_format.2f43305a0c")
        + condition(rule: match.rule, source: match.source, context: context),
      context.text("traffic_format.ea33c4dd49") + recordedRuleText(match.rule),
      context.text("traffic_format.2023d979df") + match.source,
    ].joined(separator: "\n")
  }
  public static func package(rule: String, source: String, context: PresentationContext = .chinese)
    -> String
  {
    if isIPv4OnlyRule(rule: rule, source: source) {
      return context.text("traffic_format.4599a6baf8")
    }
    if let values = evidence(rule), let name = values["Package"] { return name }
    let value = rule.trimmingCharacters(in: .whitespacesAndNewlines).replacingOccurrences(
      of: "geosite_cn", with: "geosite-cn"
    ).replacingOccurrences(of: "geoip_cn", with: "geoip-cn")
    if rule.isEmpty { return context.text("traffic_format.9896c2fedc") }
    if rule == "final" { return context.text("traffic_format.484d556139") }
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
    return source.isEmpty || source == "内核规则" ? context.text("traffic_format.079266bdfd") : source
  }
  public static func summaryPackage(
    rule: String, source: String, context: PresentationContext = .chinese
  ) -> String {
    if isIPv4OnlyRule(rule: rule, source: source) {
      return package(rule: rule, source: source, context: context)
    }
    if let values = evidence(rule), values["Method"] == "builtin", values["Target"] == "IPv6 流量" {
      return package(rule: rule, source: source, context: context)
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
    return package(rule: rule, source: source, context: context)
  }
  public static func condition(
    rule: String, source: String = "", context: PresentationContext = .chinese
  ) -> String {
    if let values = evidence(rule) {
      let target = values["Target"] ?? context.text("traffic_format.a456dde4ec")
      switch values["Method"] {
      case "domain": return context.text("traffic_format.21270ebee5") + target
      case "domain_suffix": return context.text("traffic_format.ec3be5bad0") + target
      case "ip", "cidr": return context.text("traffic_format.e71cb84455") + target
      case "application": return context.text("traffic_format.878d0a58a8") + target
      case "process": return context.text("traffic_format.6b112cc97c") + target
      default: return target
      }
    }
    let value = rule.trimmingCharacters(in: .whitespacesAndNewlines).replacingOccurrences(
      of: "geosite_cn", with: "geosite-cn"
    ).replacingOccurrences(of: "geoip_cn", with: "geoip-cn")
    if rule.isEmpty { return context.text("traffic_format.595945ac3f") }
    if rule == "final" { return context.text("traffic_format.f465077619") }
    if !source.hasPrefix("Pax 包："),
      ["rule_set=geosite-cn", "rule_set=[geosite-cn]"].contains(value)
    {
      return context.text("traffic_format.15e39c6a73")
    }
    if !source.hasPrefix("Pax 包："), ["rule_set=geoip-cn", "rule_set=[geoip-cn]"].contains(value) {
      return context.text("traffic_format.6a13ef0a94")
    }
    if !source.hasPrefix("Pax 包："), ["ip_is_private", "ip_is_private=true"].contains(value) {
      return context.text("traffic_format.0813baf380")
    }
    for prefix in ["builtin=", "source="] {
      if rule.hasPrefix(prefix) { return String(rule.dropFirst(prefix.count)) }
    }
    if rule.hasPrefix("application=") {
      return context.text("traffic_format.878d0a58a8")
        + String(rule.dropFirst("application=".count))
    }
    if rule.hasPrefix("process=") {
      return context.text("traffic_format.6b112cc97c") + String(rule.dropFirst("process=".count))
    }
    var result = rule
    for (key, title) in [
      ("domain_suffix=", context.text("traffic_format.ec3be5bad0")),
      ("domain=", context.text("traffic_format.e81dba9c98")),
      ("ip_cidr=", context.text("traffic_format.e71cb84455")),
      ("process_path_regex=", context.text("traffic_format.d1a32bd7d8")),
      ("process_path=", context.text("traffic_format.069cf829b0")),
      ("process_name=", context.text("traffic_format.65b82773c8")),
    ] { result = result.replacingOccurrences(of: key, with: title) }
    return result
  }
}
