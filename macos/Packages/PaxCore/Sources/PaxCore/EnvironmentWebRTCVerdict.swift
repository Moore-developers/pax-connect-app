import Foundation
import PaxClient

/// The only WebRTC judgement; the row, merged row and report all render this value.
extension EnvironmentDiagnosticPresentation {
  public struct WebRTCVerdict: Sendable, Equatable {
    public let conclusion: String
    public let summary: String
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

  public static func webRTCVerdict(
    _ sample: EnvironmentDiagnosticReport.Sample?, pending: Bool,
    context: PresentationContext = .chinese
  )
    -> WebRTCVerdict
  {
    let checks = sample?.checks ?? []
    let stun = checks.filter { $0.kind == "route_stun" }.map(StunRoute.init)
    let physical = checks.first { $0.kind == "egress_physical" && $0.state == "observed" }
    let physicalValues = physicalEgress(physical)
    let collecting = pending && stun.isEmpty
    let (route, routeDetails) = routeEvidence(stun, collecting: collecting, context: context)
    var physicalDetail: String?
    if let check = checks.first(where: { $0.kind == "egress_physical" }) {
      physicalDetail =
        check.state == "observed"
        ? context.text("environment_evidence.422fd1f856")
          + [physicalValues.v4, physicalValues.v6].filter { !$0.isEmpty }.joined(
            separator: " / ")
          + (check.at.map { context.text("environment_evidence.e026a44250") + timeText($0) } ?? "")
        : context.text("environment_evidence.99dc5aaa4a")
    }
    func verdict(_ text: String, _ tone: Tone, summary: String? = nil) -> WebRTCVerdict {
      WebRTCVerdict(
        conclusion: text, summary: summary ?? text, tone: tone, route: route,
        routeDetails: routeDetails,
        physicalDetail: physicalDetail)
    }
    guard let sample else {
      return verdict(
        pending
          ? context.text("environment_evidence.c83f2bf886")
          : context.text("environment_evidence.c0c079662d"), .pending)
    }
    if sample.rtcState == "unsupported" {
      return verdict(
        context.text("environment_evidence.7898adb2b8"), .pending,
        summary: context.text("environment.webrtc.unsupported"))
    }
    let publics = sample.rtcAddresses.filter(isPublicAddress)
    if !publics.isEmpty {
      if collecting && !checks.contains(where: { $0.kind == "egress_physical" }) {
        return verdict(
          context.text(
            "environment_evidence.2356bb2b86", ["arg0": String(describing: publics.count)]),
          .pending)
      }
      let result = compare(
        publics, web: (sample.ipv4, sample.ipv6), physical: physicalValues, context: context)
      return verdict(result.0, result.1)
    }
    if pending { return verdict(context.text("environment_evidence.284be5ff65"), .pending) }
    let rtcChecks = checks.filter { $0.kind.hasPrefix("rtc_") }
    let timedOut = rtcChecks.contains { $0.state == "timeout" }
    let broken =
      timedOut || rtcChecks.contains { $0.state == "failed" || !$0.errorCode.isEmpty }
      || sample.rtcState == "failed"
    let failureSummary = context.text(
      timedOut ? "environment.webrtc.timeout" : "environment.webrtc.failed")
    if stun.contains(where: { $0.state == "blocked" })
      && !stun.contains(where: { $0.state == "proxy" })
    {
      return verdict(
        context.text("environment_evidence.661a67f342"), .pending,
        summary: context.text("environment.webrtc.blocked"))
    }
    if broken && stun.contains(where: { $0.state == "proxy" }) {
      return verdict(
        context.text("environment_evidence.c244987ba2"), .pending, summary: failureSummary)
    }
    if broken {
      return verdict(
        context.text("environment_evidence.be6d3493fd"), .failure, summary: failureSummary)
    }
    if !sample.rtcState.isEmpty {
      return verdict(
        sample.rtcAddresses.isEmpty
          ? context.text("environment_evidence.292055cffb")
          : context.text("environment_evidence.5b2707f29b"),
        .pending, summary: context.text("environment.webrtc.no_public"))
    }
    return verdict(context.text("environment_evidence.c0c079662d"), .pending)
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
    _ publics: [String], web: (v4: String, v6: String), physical: (v4: String, v6: String),
    context: PresentationContext = .chinese
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
    case .exposed: return (context.text("environment_evidence.7b0dd5e95f"), .difference)
    case .differs: return (context.text("environment_evidence.3d2d92b9c5"), .difference)
    case .differsWeb: return (context.text("environment_evidence.d310bbb488"), .difference)
    case .direct: return (context.text("environment_evidence.cf943558d7"), .information)
    case .missing: return (context.text("environment_evidence.1358862289"), .pending)
    case .matching: return (context.text("environment_evidence.18bba4f6b7"), .matching)
    }
  }

  private static func routeEvidence(
    _ stun: [StunRoute], collecting: Bool, context: PresentationContext = .chinese
  ) -> (String, [String]) {
    guard !stun.isEmpty else {
      return (
        collecting
          ? context.text("environment_evidence.b800649cdb")
          : context.text("environment_evidence.0b14cb55fa"), []
      )
    }
    var sentences: [String] = []
    func add(_ text: String) { if !sentences.contains(text) { sentences.append(text) } }
    for item in stun {
      switch item.state {
      case "proxy":
        add(
          context.text("environment_evidence.65056867fd")
            + (item.route.isEmpty ? context.text("environment_evidence.5e84ea61e8") : item.route)
            + (item.restricted ? context.text("environment_evidence.657ae6e7b8") : ""))
      case "direct": add(context.text("environment_evidence.33624cb33d"))
      case "blocked": add(context.text("environment_evidence.70cce55728"))
      case "unobserved": add(context.text("environment_evidence.781901bb36"))
      case "unavailable": add(context.text("environment_evidence.5619715e64"))
      default: add(context.text("environment_evidence.0d696bafc0"))
      }
    }
    let details = stun.map { item -> String in
      let direction =
        [
          "proxy": context.text("environment_evidence.c49b87cc96"),
          "direct": context.text("environment_evidence.b06325c566"),
          "blocked": context.text("environment_evidence.c9a7fb33f3"),
          "unobserved": context.text("environment_evidence.10c2a89724"),
          "unavailable": context.text("environment_evidence.7da664f560"),
        ][item.state] ?? context.text("environment_evidence.195f984a5c")
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
