import Foundation

public enum AIUsageFormatting {
  public static func tokens(_ value: Int) -> String {
    let number = Double(value)
    for (scale, suffix) in [(1_000_000_000.0, "B"), (1_000_000.0, "M"), (1_000.0, "K")] {
      if number >= scale {
        return String(
          format: "%.1f%@", locale: Locale(identifier: "en_US_POSIX"), number / scale, suffix)
      }
    }
    return String(value)
  }
  /// Count with thousands separators, e.g. "1,930".
  public static func count(_ value: Int) -> String {
    value.formatted(.number.locale(Locale(identifier: "en_US")))
  }
  /// Claude Code writes "<synthetic>" on failed requests; it is no model, so lists and rankings skip it.
  public static func isSyntheticModel(_ id: String) -> Bool { id == "<synthetic>" }
  public static func cost(_ value: Double, precision: Int = 3) -> String {
    String(format: "$%.*f", locale: Locale(identifier: "en_US_POSIX"), precision, value)
  }
  /// Profile multiplier: at most 3 decimals, trailing zeros trimmed, e.g. "0.265×", "1×".
  public static func multiplier(_ value: Double) -> String {
    var text = String(format: "%.3f", locale: Locale(identifier: "en_US_POSIX"), value)
    while text.hasSuffix("0") { text.removeLast() }
    if text.hasSuffix(".") { text.removeLast() }
    return text + "×"
  }
  /// Plain number for an editor field: the same digits without the "×".
  public static func multiplierInput(_ value: Double) -> String {
    String(multiplier(value).dropLast())
  }
  /// Banner text for a failed collection; an old database needs the rebuild program.
  public static func collectionError(_ code: String) -> String {
    code == "ai_usage_rebuild_required" ? "需要运行重建" : "本地采集暂不可用，请重试"
  }
  public static func percent(_ value: Double?) -> String {
    value.map { String(format: "%.1f%%", locale: Locale(identifier: "en_US_POSIX"), $0 * 100) }
      ?? "—"
  }
  /// Zero-padded month and day (MM-DD) in the current time zone.
  public static func monthDay(_ value: Date) -> String {
    let parts = Calendar.current.dateComponents([.month, .day], from: value)
    return String(format: "%02d-%02d", parts.month ?? 0, parts.day ?? 0)
  }
  /// Latest-use stamp: HH:mm on the same day as `now`, otherwise MM-DD.
  public static func recentUse(_ value: Date, now: Date, calendar: Calendar = .current) -> String {
    guard calendar.isDate(value, inSameDayAs: now) else { return monthDay(value) }
    let parts = calendar.dateComponents([.hour, .minute], from: value)
    return String(format: "%02d:%02d", parts.hour ?? 0, parts.minute ?? 0)
  }
  public static func date(_ value: Date, time: Bool = false) -> String {
    value.formatted(
      .dateTime.year().month(.twoDigits).day(.twoDigits).hour(
        time ? .twoDigits(amPM: .omitted) : .omitted
      ).minute(time ? .twoDigits : .omitted))
  }
  /// Cache read share of input as a whole percent, e.g. "96%"; nil without input.
  public static func cacheShare(read: Int, input: Int) -> String? {
    input > 0 ? percent(Double(read) / Double(input)) : nil
  }
  /// Log token line: "182.0K（96.0% 缓存）"; the suffix is omitted without input.
  public static func tokenLine(tokens: Int, read: Int, input: Int) -> String {
    Self.tokens(tokens) + (cacheShare(read: read, input: input).map { "（\($0) 缓存）" } ?? "")
  }
  /// Comparison with the previous period: "较上一时段 ↑12%" / "↓5%" / "持平"; nil previous
  /// (no data or a zero base) reads "上一时段无数据". `points` compares rates in percentage points.
  public static func comparison(current: Double, previous: Double?, points: Bool = false) -> String
  {
    guard let previous, points || previous > 0 else { return "上一时段无数据" }
    let delta = points ? (current - previous) * 100 : (current - previous) / previous * 100
    let rounded = Int(delta.rounded())
    guard rounded != 0 else { return "较上一时段 持平" }
    return "较上一时段 " + (rounded > 0 ? "↑" : "↓") + "\(abs(rounded))" + (points ? "pt" : "%")
  }
  /// Short failure text for a table cell; the full reason stays in the tooltip.
  public static func failureBrief(_ code: String?) -> String {
    let text = code ?? ""
    let digits = text.split(whereSeparator: { !$0.isNumber }).first { $0.count == 3 }.map(
      String.init)
    switch (digits, text.uppercased()) {
    case ("401", _), ("403", _), (_, "AUTH"): return (digits ?? "401") + " 认证"
    case ("402", _): return "402 额度"
    case ("429", _), (_, "RATE_LIMIT"): return "429 限流"
    case (let d?, _) where d.hasPrefix("5"): return d + " 上游"
    case (let d?, _): return d + " 错误"
    case (_, "TIMEOUT"): return "超时"
    case (_, "TRANSPORT"): return "连接失败"
    default: return "失败"
    }
  }
}
