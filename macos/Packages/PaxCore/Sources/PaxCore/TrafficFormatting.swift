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
  public static func connectionEvidence(_ row: TrafficRow) -> String {
    if isIPv6LimitedPath(row) { return TrafficRuleFormatting.ipv6Limitation }
    return
      row.exits.isEmpty
      ? actualConnection(direction: row.direction, route: row.routeName)
      : row.exits.map {
        actualConnection(direction: $0.direction, route: $0.routeName) + " · \($0.connections) 个连接"
      }.joined(separator: "\n")
  }
  public static func ruleEvidence(_ row: TrafficRow) -> String {
    row.bases.map {
      TrafficRuleFormatting.package(rule: $0.rule, source: $0.source) + " · "
        + TrafficRuleFormatting.condition(rule: $0.rule, source: $0.source)
        + " · \($0.connections) 个连接"
    }.joined(separator: "\n")
  }
  public static func actualConnections(_ row: TrafficRow) -> String {
    guard !row.exits.isEmpty else {
      return actualConnection(direction: row.direction, route: row.routeName)
    }
    return row.exits.map { actualConnection(direction: $0.direction, route: $0.routeName) }.joined(
      separator: "\n")
  }
  public static func actualConnection(direction: String, route: String) -> String {
    switch direction {
    case "direct", "直连": return "直连"
    case "blocked", "已阻断": return "已阻断"
    case "proxy", "代理":
      return route.isEmpty || route.hasPrefix("pax-node-") ? "代理线路名称未记录" : route
    case "mixed": return "多个连接去向，见详情"
    default: return "去向未记录"
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
  public static func connectionDuration(_ metrics: ConnectionMetrics) -> String {
    guard ["closed", "active", "observed"].contains(metrics.durationState) else { return "—" }
    let seconds = Double(metrics.durationMS) / 1000
    let text =
      seconds < 60
      ? String(format: "%.1f秒", seconds)
      : seconds < 3600
        ? "\(Int(seconds) / 60)分\(Int(seconds) % 60)秒"
        : "\(Int(seconds) / 3600)小时\(Int(seconds) / 60 % 60)分"
    switch metrics.durationState {
    case "active": return text + " · 未结束"
    case "observed": return "已观测" + text
    default: return text
    }
  }
  public static func connectionBytes(_ metrics: ConnectionMetrics) -> String {
    guard ["complete", "partial"].contains(metrics.bytesState) else { return "—" }
    return "↑ " + bytes(metrics.upload) + " / ↓ " + bytes(metrics.download)
  }
  public static func connectionMetricsNote(_ metrics: ConnectionMetrics) -> String {
    let time = metrics.asOf.map(logDate) ?? "未知"
    let reasons = [
      "observation_gap": "观测存在缺口",
      "after_clear": "仅包含清除后的观测",
      "historical_checkpoint": "历史截止时间无法精确还原",
      "legacy_history": "旧历史缺少累计证据",
      "missing_metrics": "未保存累计数据",
      "missing_start": "未记录完整连接起点",
      "counter_overflow": "计数超过可记录范围",
    ]
    let state =
      metrics.bytesState == "partial"
      ? "仅为已观测下界，存在缺口" : metrics.bytesState == "complete" ? "累计转发字节" : "累计数据未知"
    let reason = metrics.reason.isEmpty ? "" : " · " + reasons[metrics.reason, default: "记录不完整"]
    return state + " · 截至 " + time + reason
  }
}
