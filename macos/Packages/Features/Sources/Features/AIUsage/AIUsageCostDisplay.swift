import AppKit
import PaxCore
import PaxUI

/// The two estimates always read official first, then channel; colour supplements their labels.
enum AIUsageCostDisplay {
  static let officialColor = PaxTheme.Color.error
  static let channelColor = PaxTheme.Chart.cacheLabel
  static let explanation = "官方费用按已记录官方单价以1×计算；渠道费用按已记录倍率计算。均为本地估算，非平台账单。"

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
    _ label: NSTextField, official: String, channel: String, stacked: Bool = false,
    lineSpacing: CGFloat = 0
  ) {
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
