import AppKit
import PaxCore
import PaxUI

/// The two estimates always read official first, then channel; colour supplements their labels.
enum AIUsageCostDisplay {
  static let officialColor = PaxTheme.Color.error
  static let channelColor = PaxTheme.Chart.cacheLabel
  static func explanation(context: PresentationContext = .chinese) -> String {
    context.text("model_copy.4e994775e3")
  }

  static func text(official: String, channel: String) -> String {
    official + " / " + channel
  }

  static func attributed(
    official: String, channel: String, font: NSFont, stacked: Bool = false,
    context: PresentationContext = .chinese
  ) -> NSAttributedString {
    let result = NSMutableAttributedString()
    func append(_ text: String, _ color: NSColor) {
      result.append(
        NSAttributedString(string: text, attributes: [.font: font, .foregroundColor: color]))
    }
    if stacked { append(context.text("model_copy.5aee18ffc3"), PaxTheme.Color.textSecondary) }
    append(official, officialColor)
    append(stacked ? context.text("model_copy.2d31155200") : " / ", PaxTheme.Color.textSecondary)
    append(channel, channelColor)
    return result
  }

  @MainActor
  static func apply(
    _ label: NSTextField, official: String, channel: String?, stacked: Bool = false,
    lineSpacing: CGFloat = 0, context: PresentationContext = .chinese
  ) {
    guard let channel else {
      label.stringValue = official + context.text("model_copy.3bfc15f873")
      label.textColor = officialColor
      label.maximumNumberOfLines = 1
      label.toolTip =
        context.text("model_copy.83b6c5ab43") + official + "\n" + explanation(context: context)
      label.setAccessibilityLabel(context.text("model_copy.83b6c5ab43") + official)
      return
    }
    let text = NSMutableAttributedString(
      attributedString: attributed(
        official: official, channel: channel, font: label.font ?? PaxTheme.Typography.time,
        stacked: stacked, context: context))
    let paragraph = NSMutableParagraphStyle()
    paragraph.alignment = label.alignment
    paragraph.lineSpacing = lineSpacing
    text.addAttribute(
      .paragraphStyle, value: paragraph, range: NSRange(location: 0, length: text.length))
    label.attributedStringValue = text
    label.maximumNumberOfLines = stacked ? 2 : 1
    label.toolTip =
      context.text("model_copy.7887a7c947") + official + context.text("model_copy.eb33a66004")
      + channel + "\n" + explanation(context: context)
    label.setAccessibilityLabel(
      context.text("model_copy.7887a7c947") + official + context.text("model_copy.330463a5ec")
        + channel)
  }

  @MainActor
  static func label(
    official: String, channel: String, stacked: Bool = false, alignment: NSTextAlignment = .left,
    context: PresentationContext = .chinese
  ) -> NSTextField {
    let label = AIUsageLayout.label("", numeric: true)
    label.alignment = alignment
    apply(label, official: official, channel: channel, stacked: stacked, context: context)
    return label
  }
}
