import AppKit
import PaxCore
import PaxUI

/// The two estimates always read official first, then channel; colour supplements their labels.
enum AIUsageCostDisplay {
  static let officialColor = PaxTheme.Color.error
  static let channelColor = PaxTheme.Chart.cacheLabel
  static let explanation =
    "官方费用按已记录 API 单价以1×折算，包含订阅的 API 等值估算；渠道费用按已记录倍率计算。订阅、来源待确认或混合记录不展示渠道金额。均非平台实际账单，不含订阅剩余额度。"

  static func text(official: String, channel: String) -> String {
    official + " / " + channel
  }

  static func attributed(
    official: String, channel: String, font: NSFont, stacked: Bool = false
  ) -> NSAttributedString {
    let result = NSMutableAttributedString()
    func append(_ text: String, _ color: NSColor) {
      result.append(
        NSAttributedString(string: text, attributes: [.font: font, .foregroundColor: color]))
    }
    if stacked { append("官方 ", PaxTheme.Color.textSecondary) }
    append(official, officialColor)
    append(stacked ? "\n渠道 " : " / ", PaxTheme.Color.textSecondary)
    append(channel, channelColor)
    return result
  }

  @MainActor
  static func apply(
    _ label: NSTextField, official: String, channel: String?, stacked: Bool = false,
    lineSpacing: CGFloat = 0
  ) {
    guard let channel else {
      label.stringValue = official + "（API 等值）"
      label.textColor = officialColor
      label.maximumNumberOfLines = 1
      label.toolTip = "API 等值估算 " + official + "\n" + explanation
      label.setAccessibilityLabel("API 等值估算 " + official)
      return
    }
    let text = NSMutableAttributedString(
      attributedString: attributed(
        official: official, channel: channel, font: label.font ?? PaxTheme.Typography.time,
        stacked: stacked))
    let paragraph = NSMutableParagraphStyle()
    paragraph.alignment = label.alignment
    paragraph.lineSpacing = lineSpacing
    text.addAttribute(
      .paragraphStyle, value: paragraph, range: NSRange(location: 0, length: text.length))
    label.attributedStringValue = text
    label.maximumNumberOfLines = stacked ? 2 : 1
    label.toolTip = "官方费用 " + official + " · 渠道费用 " + channel + "\n" + explanation
    label.setAccessibilityLabel("官方费用 " + official + "，渠道费用 " + channel)
  }

  @MainActor
  static func label(
    official: String, channel: String, stacked: Bool = false, alignment: NSTextAlignment = .left
  ) -> NSTextField {
    let label = AIUsageLayout.label("", numeric: true)
    label.alignment = alignment
    apply(label, official: official, channel: channel, stacked: stacked)
    return label
  }
}
