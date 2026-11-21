import Foundation

/// 流量的十进制字节格式，保持 GB/MB 同一口径，供各页的值呈现复用。
public enum TrafficFormatting {
  /// A whole path is a connection restriction only when every saved basis proves it.
  public static func isIPv6LimitedPath(_ row: TrafficRow) -> Bool {
    row.direction == "blocked" && !row.bases.isEmpty
      && row.bases.allSatisfy {
        TrafficRuleFormatting.isIPv4OnlyRule(rule: $0.rule, source: $0.source)
      }
  }
  public static func connectionEvidence(_ row: TrafficRow, context: PresentationContext = .chinese)
    -> String
  {
    if isIPv6LimitedPath(row) { return TrafficRuleFormatting.ipv6Limitation(context: context) }
    return
      row.exits.isEmpty
      ? actualConnection(direction: row.direction, route: row.routeName, context: context)
      : row.exits.map {
        actualConnection(direction: $0.direction, route: $0.routeName, context: context)
          + context.text("traffic_format.d3e2074222", ["arg0": String(describing: $0.connections)])
      }.joined(separator: "\n")
  }
  public static func ruleEvidence(_ row: TrafficRow, context: PresentationContext = .chinese)
    -> String
  {
    row.bases.map {
      TrafficRuleFormatting.package(rule: $0.rule, source: $0.source) + " · "
        + TrafficRuleFormatting.condition(rule: $0.rule, source: $0.source)
        + context.text("traffic_format.d3e2074222", ["arg0": String(describing: $0.connections)])
    }.joined(separator: "\n")
  }
  public static func actualConnections(_ row: TrafficRow, context: PresentationContext = .chinese)
    -> String
  {
    guard !row.exits.isEmpty else {
      return actualConnection(direction: row.direction, route: row.routeName, context: context)
    }
    return row.exits.map {
      actualConnection(direction: $0.direction, route: $0.routeName, context: context)
    }.joined(
      separator: "\n")
  }
  public static func actualConnection(direction: String, route: String) -> String {
    actualConnection(direction: direction, route: route, context: .chinese)
  }
  public static func actualConnection(
    direction: String, route: String, context: PresentationContext
  ) -> String {
    switch direction {
    case "direct", "直连": return context.text("common.direct")
    case "blocked", "已阻断": return context.text("flow.blocked")
    case "proxy", "代理":
      return route.isEmpty || route.hasPrefix("pax-node-")
        ? context.text("traffic.proxy_name_missing") : route
    case "mixed": return context.text("traffic.mixed_destinations")
    default: return context.text("traffic.direction_missing")
    }
  }
  public static func dateMinute(_ date: Date) -> String {
    let parts = Calendar(identifier: .gregorian).dateComponents(in: .current, from: date)
    return String(
      format: "%04d-%02d-%02d %02d:%02d", parts.year ?? 0, parts.month ?? 0, parts.day ?? 0,
      parts.hour ?? 0, parts.minute ?? 0)
  }
  public static func dateAndClock(_ date: Date) -> (date: String, clock: String) {
    let parts = Calendar(identifier: .gregorian).dateComponents(in: .current, from: date)
    return (
      String(format: "%04d-%02d-%02d", parts.year ?? 0, parts.month ?? 0, parts.day ?? 0),
      String(format: "%02d:%02d:%02d", parts.hour ?? 0, parts.minute ?? 0, parts.second ?? 0)
    )
  }
  public static func responseLatency(_ flow: FlowFact) -> Double? {
    if flow.responseKnown { return Double(flow.responseMS) }
    if flow.establishmentState == "response_ready" && flow.evidence == "first_response" {
      return Double(flow.establishmentMS)
    }
    return nil
  }
  public static func latestLatency(_ row: TrafficRow) -> Double? {
    row.latestLatencyAt == nil ? nil : Double(row.latestLatencyMS)
  }
  public static func latencySamples(_ row: TrafficRow) -> [(at: Date, milliseconds: Double?)] {
    row.latencySamples.filter(\.known).sorted { $0.at < $1.at }.map {
      ($0.at, $0.known ? Double($0.milliseconds) : nil)
    }
  }
  public static func logDate(_ date: Date) -> String {
    let parts = Calendar(identifier: .gregorian).dateComponents(in: TimeZone.current, from: date)
    return String(
      format: "%04d-%02d-%02d %02d:%02d", parts.year ?? 0, parts.month ?? 0, parts.day ?? 0,
      parts.hour ?? 0, parts.minute ?? 0)
  }
  public static func logDateWithoutYear(_ date: Date) -> String {
    String(logDate(date).dropFirst(5))
  }
  public static func time(_ date: Date) -> String {
    date.formatted(.dateTime.hour(.twoDigits(amPM: .omitted)).minute(.twoDigits).second(.twoDigits))
  }
  public static func milliseconds(_ value: Double?, unit: String = "ms") -> String {
    guard let value, value.isFinite, value >= 0 else { return "—" }
    if unit == "ms" { return MeasurementFormatting.latency(value)?.text ?? "—" }
    return MeasurementFormatting.number(value) + " " + unit
  }
  /// Charts carry decimal MB values; reuse the shared byte display for each
  /// individual readout so a tiny upload beside a large download stays nonzero.
  public static func megabytes(_ value: Double) -> String {
    guard value.isFinite, value >= 0 else { return "—" }
    let bytes = min(value * 1_000_000, Double(UInt64.max).nextDown)
    return self.bytes(UInt64(bytes.rounded(.down)))
  }
  public static func bytes(_ value: UInt64) -> String {
    let gb = Double(value) / 1_000_000_000
    if gb >= 1 { return gb.formatted(.number.precision(.fractionLength(2))) + " GB" }
    if value >= 1_000_000 {
      return (Double(value) / 1_000_000).formatted(.number.precision(.fractionLength(0...1)))
        + " MB"
    }
    if value >= 1_000 {
      return (Double(value) / 1_000).formatted(.number.precision(.fractionLength(0...1))) + " KB"
    }
    return "\(value) B"
  }
}

extension TrafficFormatting {
  public static func connectionDuration(
    _ metrics: ConnectionMetrics, context: PresentationContext = .chinese
  ) -> String {
    guard ["closed", "active", "observed"].contains(metrics.durationState) else { return "—" }
    let seconds = Double(metrics.durationMS) / 1000
    let text =
      seconds < 60
      ? context.text("traffic_duration.seconds", ["seconds": String(format: "%.1f", seconds)])
      : seconds < 3600
        ? context.text(
          "traffic_format.3717406cb2",
          [
            "arg0": String(describing: Int(seconds) / 60),
            "arg1": String(describing: Int(seconds) % 60),
          ])
        : context.text(
          "traffic_format.b44a73e31d",
          [
            "arg0": String(describing: Int(seconds) / 3600),
            "arg1": String(describing: Int(seconds) / 60 % 60),
          ])
    switch metrics.durationState {
    case "active": return text + context.text("traffic_format.d09dd1e6e4")
    case "observed": return context.text("traffic_format.2aecd93622") + text
    default: return text
    }
  }
  public static func connectionBytes(_ metrics: ConnectionMetrics) -> String {
    guard ["complete", "partial"].contains(metrics.bytesState) else { return "—" }
    return "↑ " + bytes(metrics.upload) + " / ↓ " + bytes(metrics.download)
  }
  public static func connectionMetricsNote(
    _ metrics: ConnectionMetrics, context: PresentationContext = .chinese
  ) -> String {
    let time = metrics.asOf.map(logDate) ?? context.text("traffic_format.4d8c1c5b42")
    let reasons = [
      "observation_gap": context.text("traffic_format.c8786c5178"),
      "after_clear": context.text("traffic_format.96554763bc"),
      "historical_checkpoint": context.text("traffic_format.33446ca7d8"),
      "legacy_history": context.text("traffic_format.6d0e886d90"),
      "missing_metrics": context.text("traffic_format.c77be98933"),
      "missing_start": context.text("traffic_format.2d957e9abe"),
      "counter_overflow": context.text("traffic_format.4995141c93"),
    ]
    let state =
      metrics.bytesState == "partial"
      ? context.text("traffic_format.6b2461813f")
      : metrics.bytesState == "complete"
        ? context.text("traffic_format.d427994adb") : context.text("traffic_format.7decbfb05e")
    let reason =
      metrics.reason.isEmpty
      ? "" : " · " + reasons[metrics.reason, default: context.text("traffic_format.4e9fcac3cb")]
    return state + context.text("traffic_format.d487d80fbe") + time + reason
  }
}
