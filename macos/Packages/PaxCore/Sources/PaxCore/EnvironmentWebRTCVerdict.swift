import Foundation
import PaxClient

/// The only WebRTC judgement; the row, merged row and report all render this value.
extension EnvironmentDiagnosticPresentation {
  public struct WebRTCVerdict: Sendable, Equatable {
    public let conclusion: String
    public let tone: Tone
    /// One sentence about the STUN route evidence, kept apart from the conclusion.
    public let route: String
    /// Per-target lines; never contain the physical egress address.
    public let routeDetails: [String]
    /// Reading from the read-only physical egress check, for the UI details only.
    public let physicalDetail: String?
    public var text: String { conclusion + "；" + route }
  }

  private struct StunRoute {
    let target, state, route, source, rule, process: String
    init(_ check: EnvironmentDiagnosticReport.Check) {
      let parts = check.value.components(separatedBy: "\t")
      func part(_ index: Int) -> String { index < parts.count ? parts[index] : "" }
      target = check.target
      state = check.state
      route = part(0)
      source = part(1)
      rule = part(2)
      process = part(3)
    }
    var restricted: Bool { source.contains("WebRTC出口限制") }
  }

  public static func webRTCVerdict(_ sample: EnvironmentDiagnosticReport.Sample?, pending: Bool)
    -> WebRTCVerdict
  {
    let checks = sample?.checks ?? []
    let stun = checks.filter { $0.kind == "route_stun" }.map(StunRoute.init)
    let physical = checks.first { $0.kind == "egress_physical" && $0.state == "observed" }
    let physicalValues = physicalEgress(physical)
    let collecting = pending && stun.isEmpty
    let (route, routeDetails) = routeEvidence(stun, collecting: collecting)
    var physicalDetail: String?
    if let check = checks.first(where: { $0.kind == "egress_physical" }) {
      physicalDetail =
        check.state == "observed"
        ? "物理出口："
          + [physicalValues.v4, physicalValues.v6].filter { !$0.isEmpty }.joined(
            separator: " / ") + (check.at.map { " · 测量于 " + timeText($0) } ?? "")
        : "物理出口：未取得"
    }
    func verdict(_ text: String, _ tone: Tone) -> WebRTCVerdict {
      WebRTCVerdict(
        conclusion: text, tone: tone, route: route, routeDetails: routeDetails,
        physicalDetail: physicalDetail)
    }
    guard let sample else {
      return verdict(pending ? "正在检测 WebRTC 地址" : "泄露风险待确认 · 浏览器未返回候选地址", .pending)
    }
    if sample.rtcState == "unsupported" { return verdict("泄露风险待确认 · 浏览器不支持 WebRTC，无法检测", .pending) }
    let publics = sample.rtcAddresses.filter(isPublicAddress)
    if !publics.isEmpty {
      if collecting && !checks.contains(where: { $0.kind == "egress_physical" }) {
        return verdict("已取得 \(publics.count) 个公网候选，正在核对出口", .pending)
      }
      let result = compare(publics, web: (sample.ipv4, sample.ipv6), physical: physicalValues)
      return verdict(result.0, result.1)
    }
    if pending { return verdict("正在采集浏览器候选地址", .pending) }
    let rtcChecks = checks.filter { $0.kind.hasPrefix("rtc_") }
    let broken =
      rtcChecks.contains { $0.state == "timeout" || $0.state == "failed" || !$0.errorCode.isEmpty }
      || sample.rtcState == "failed"
    if stun.contains(where: { $0.state == "blocked" })
      && !stun.contains(where: { $0.state == "proxy" })
    {
      return verdict("泄露风险待确认 · 探测被规则阻断，未取得公网地址", .pending)
    }
    if broken && stun.contains(where: { $0.state == "proxy" }) {
      return verdict("泄露风险待确认 · 探测经代理但未返回地址（代理可能不支持 UDP）", .pending)
    }
    if broken { return verdict("泄露风险待确认 · 探测失败或到期，无法核对公网地址", .failure) }
    if !sample.rtcState.isEmpty {
      return verdict(
        sample.rtcAddresses.isEmpty
          ? "泄露风险待确认 · 未取得候选地址，不能据此确认防泄露"
          : "泄露风险待确认 · 仅取得局域网 / mDNS 候选，未取得公网地址",
        .pending)
    }
    return verdict("泄露风险待确认 · 浏览器未返回候选地址", .pending)
  }

  private static func physicalEgress(_ check: EnvironmentDiagnosticReport.Check?) -> (
    v4: String, v6: String
  ) {
    var result = (v4: "", v6: "")
    for part in (check?.value ?? "").components(separatedBy: ";") {
      let pair = part.split(separator: "=", maxSplits: 1).map(String.init)
      guard pair.count == 2 else { continue }
      if pair[0] == "v4" { result.v4 = pair[1] } else if pair[0] == "v6" { result.v6 = pair[1] }
    }
    return result
  }

  private static func compare(
    _ publics: [String], web: (v4: String, v6: String), physical: (v4: String, v6: String)
  ) -> (String, Tone) {
    enum Kind: Int { case exposed, differs, differsWeb, direct, missing, matching }
    let kinds = publics.map { address -> Kind in
      let v6 = address.contains(":")
      let web = v6 ? web.v6 : web.v4
      let local = v6 ? physical.v6 : physical.v4
      guard !web.isEmpty else { return .missing }
      if !local.isEmpty {
        if address == local && local != web { return .exposed }
        if local == web { return .direct }
        return address == web ? .matching : .differs
      }
      return address == web ? .matching : .differsWeb
    }
    switch kinds.min(by: { $0.rawValue < $1.rawValue }) ?? .missing {
    case .exposed: return ("已发现公网地址泄露 · WebRTC 暴露本机真实 IP", .difference)
    case .differs: return ("存在潜在风险 · 与网页、物理出口都不同，需核对", .difference)
    case .differsWeb: return ("存在潜在风险 · 公网地址与网页出口不同，需核对保护策略", .difference)
    case .direct: return ("当前未经代理，WebRTC 与网页同为本机出口", .information)
    case .missing: return ("泄露风险待确认 · 缺少同地址族网页出口，无法对照公网地址", .pending)
    case .matching: return ("本次未发现公网地址泄露 · 与网页出口一致", .matching)
    }
  }

  private static func routeEvidence(_ stun: [StunRoute], collecting: Bool) -> (String, [String]) {
    guard !stun.isEmpty else { return (collecting ? "正在采集线路证据" : "旧记录未采集线路证据", []) }
    var sentences: [String] = []
    func add(_ text: String) { if !sentences.contains(text) { sentences.append(text) } }
    for item in stun {
      switch item.state {
      case "proxy":
        add(
          "STUN 经 " + (item.route.isEmpty ? "代理" : item.route)
            + (item.restricted ? "（WebRTC 出口限制生效）" : ""))
      case "direct": add("STUN 直连")
      case "blocked": add("STUN 被阻断")
      case "unobserved": add("未观察到 STUN 连接")
      case "unavailable": add("Pax 未接管，无线路证据")
      default: add("STUN 方向未知")
      }
    }
    let details = stun.map { item -> String in
      let direction =
        [
          "proxy": "经代理", "direct": "直连", "blocked": "被阻断", "unobserved": "未观察到连接",
          "unavailable": "无线路证据",
        ][item.state] ?? "方向未知"
      return
        ([item.target + " → " + direction]
        + [item.route, item.source, item.process].filter { !$0.isEmpty }).joined(separator: " · ")
    }
    return (sentences.joined(separator: "，"), details)
  }

  static func timeText(_ date: Date) -> String {
    let format = DateFormatter()
    format.locale = Locale(identifier: "en_US_POSIX")
    format.dateFormat = "yyyy-MM-dd HH:mm:ss"
    return format.string(from: date)
  }
}
