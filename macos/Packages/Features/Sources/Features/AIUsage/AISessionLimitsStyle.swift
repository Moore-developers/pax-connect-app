import AppKit
import PaxCore
import PaxUI

/// Colours, formats and labels shared by the limits cards. Colours reuse existing tokens; the
/// three bands are safe (green) / warning (orange) / over the limit (Coral).
enum AILimitStyle {
  static let multipleExplanation =
    "超限请求相对安全基准的输入费用倍数。\n例如 ×2：安全每次 $1，超限每次 $2。\nDeepSeek按同模型、同峰谷时段比较。"
  static let spendExplanation =
    "以安全区间单次均费为参照，估计超限请求多花的部分。\n已含在输入费用内，未扣开新会话的成本。\nDeepSeek只使用同模型、同峰谷时段的基准。"
  static let neutral = PaxTheme.Routes.muted
  /// Limit line and over-limit tint; the flat-case limit uses a darker Coral.
  static let warnLine = PaxTheme.Chart.inputSeries
  static let limitLine = PaxTheme.DashboardColor.red
  static let flatLine = PaxTheme.DashboardColor.redLabel
  private static func soft(_ color: NSColor, _ fraction: CGFloat) -> NSColor {
    color.blended(withFraction: fraction, of: .white) ?? color
  }
  /// Light solid fills of the annotated bar; the over-limit segment is Coral with stripes.
  static let bandFills: [NSColor] = [
    soft(PaxTheme.DashboardColor.green, 0.6), soft(PaxTheme.Chart.inputSeries, 0.55),
    PaxTheme.DashboardColor.red,
  ]
  static let neutralFill = soft(PaxTheme.Routes.muted, 0.6)
  static let bandLabels: [NSColor] = [
    PaxTheme.DashboardColor.greenLabel, PaxTheme.DashboardColor.uploadLabel,
    PaxTheme.DashboardColor.redLabel,
  ]
  static let bandNames = ["安全", "预警", "超限"]
  /// Cost sticks of the trend by band: green / orange / Coral.
  static let bandColors: [NSColor] = [
    PaxTheme.DashboardColor.green, PaxTheme.Chart.inputSeries, PaxTheme.DashboardColor.red,
  ]
  /// Stacked context layers of the trend (spec §4.3); none uses the band colours.
  static let cacheRead = rgb(0x0ea5e9)
  static let cacheWrite = rgb(0xec4899)
  static let fresh = rgb(0x8b5cf6)
  private static func rgb(_ hex: UInt32) -> NSColor {
    NSColor(
      srgbRed: CGFloat((hex >> 16) & 255) / 255, green: CGFloat((hex >> 8) & 255) / 255,
      blue: CGFloat(hex & 255) / 255, alpha: 1)
  }
  /// Muted dash for values that do not apply.
  static let dim = PaxTheme.Color.textSecondary.withAlphaComponent(0.55)

  static func effortTone(_ value: String) -> PaxFeatureTag.Tone {
    switch value.lowercased() {
    case "light", "low": .green
    case "medium": .blue
    case "high": .yellow
    case "xhigh": .amber
    case "max": .red
    default: .neutral
    }
  }
  static func k(_ tokens: Int) -> String { "\(Int((Double(tokens) / 1000).rounded()))k" }
  /// Axis tick "100k" / "0"; a step below 1k keeps its remainder.
  static func axisTokens(_ value: Double) -> String {
    guard value >= 1 else { return "0" }
    guard value >= 1000 else { return String(Int(value.rounded())) }
    return trimmed(value / 1000, digits: 1) + "k"
  }
  /// Axis tick "$0.03": two decimals, more only when the step needs them.
  static func axisCost(_ value: Double) -> String {
    guard value > 0 else { return "$0" }
    return "$" + trimmed(value, digits: 4, minimum: 2)
  }
  private static func trimmed(_ value: Double, digits: Int, minimum: Int = 0) -> String {
    var text = String(format: "%.\(digits)f", locale: Locale(identifier: "en_US_POSIX"), value)
    while text.contains("."), text.hasSuffix("0"),
      text.count
        - (text.firstIndex(of: ".").map { text.distance(from: text.startIndex, to: $0) + 1 } ?? 0)
        > minimum
    { text.removeLast() }
    if text.hasSuffix(".") { text.removeLast() }
    return text
  }
  static func times(_ value: Double) -> String { String(format: "×%.1f", value) }
  static func money(_ value: Double?, precision: Int = 4) -> String {
    value.map { AIUsageFormatting.cost($0, precision: precision) } ?? "—"
  }
  static func plus(_ value: Double?, precision: Int = 2) -> String {
    value.map { ($0 < 0 ? "−" : "+") + money(abs($0), precision: precision) } ?? "—"
  }
  /// "官方 $1.20 · 渠道 $0.42".
  static func both(_ value: AILimitMoney, precision: Int = 2, plus: Bool = false) -> String {
    let sign = plus ? "+" : ""
    return "官方 " + sign + money(value.official, precision: precision) + " · 渠道 " + sign
      + money(value.channel, precision: precision)
  }
  /// Band edge labels of one model, e.g. "≤150k", "150k–200k", ">200k".
  static func edges(_ lines: AIModelLimit?) -> [String] {
    guard let lines else { return ["未设上限"] }
    return [
      "≤" + k(lines.warn), k(lines.warn) + "–" + k(lines.limit), ">" + k(lines.limit),
    ]
  }
  static func multipleText(_ multiple: AILimitMultiple) -> String {
    switch multiple {
    case .value(let value): times(value)
    case .dash: "—"
    }
  }
}
