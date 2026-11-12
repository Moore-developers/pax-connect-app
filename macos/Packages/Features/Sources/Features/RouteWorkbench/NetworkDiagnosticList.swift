import AppKit
import PaxCore
import PaxUI

extension NetworkDiagnosticView {
  func diagnosticRow(
    title: String, subtitle: String, symbol: String, detail: String = "",
    tone: E.Tone = .information
  ) -> NSView {
    let icon = PaxIcon.view(symbol, size: PaxTheme.NetworkDiagnostics.environmentIcon)
    icon.contentTintColor = assessmentColor(tone)
    icon.widthAnchor.constraint(equalToConstant: PaxTheme.Spacing.page).isActive = true
    let name = text(title + "：")
    name.font = PaxTheme.Typography.label(size: PaxTheme.Routes.bodyFont, weight: .medium)
    name.maximumNumberOfLines = 1
    name.lineBreakMode = .byTruncatingTail
    name.widthAnchor.constraint(lessThanOrEqualToConstant: PaxTheme.NetworkDiagnostics.labelWidth)
      .isActive = true
    name.setContentHuggingPriority(.required, for: .horizontal)
    name.toolTip = title
    let summary = text(subtitle)
    summary.isSelectable = true
    summary.maximumNumberOfLines = 1
    summary.lineBreakMode = .byTruncatingTail
    summary.textColor = assessmentColor(tone)
    let key = "diagnostic-row-" + title
    let expanded = store.expanded.contains(key)
    let toggle = action(expanded ? "收起" : "详情") { [weak self] in
      guard let self else { return }
      if store.expanded.contains(key) {
        store.expanded.remove(key)
      } else {
        store.expanded.insert(key)
      }
    }
    toggle.controlSize = .small
    toggle.setAccessibilityIdentifier(key)
    toggle.setAccessibilityLabel(title + (expanded ? "收起详情" : "展开详情"))
    let line = padded(
      horizontal([icon, name, summary, toggle], spacing: PaxTheme.Spacing.tight),
      vertical: PaxTheme.NetworkDiagnostics.environmentRowInset)
    line.toolTip = [subtitle, detail].filter { !$0.isEmpty }.joined(separator: "\n")
    line.setAccessibilityElement(true)
    line.setAccessibilityRole(.group)
    line.setAccessibilityLabel(title + "：" + subtitle)
    line.setAccessibilityChildren([name, summary, toggle])
    guard expanded else { return line }
    let evidence = diagnosticDetailCard(detail.isEmpty ? subtitle : detail, omitting: subtitle)
    evidence.setAccessibilityIdentifier(key + "-evidence")
    return vertical([line, padded(evidence, vertical: PaxTheme.Spacing.tight)], spacing: 0)
  }

  /// Row evidence uses the existing inset surface.
  func diagnosticDetailCard(_ detail: String, omitting summary: String = "") -> NSView {
    let sections = ["Mac全部解析器：", "浏览器全部解析器：", "WebRTC 候选（含局域网 / mDNS）：", "采样证据"]
    var lines = detail.components(separatedBy: .newlines).map {
      $0.trimmingCharacters(in: .whitespaces)
    }.filter { !$0.isEmpty && $0 != summary }
    if lines.isEmpty { lines = [summary] }
    let font = PaxTheme.Typography.caption()
    let paragraph = NSMutableParagraphStyle()
    paragraph.lineSpacing = PaxTheme.Spacing.xs
    paragraph.paragraphSpacing = PaxTheme.Spacing.xs
    paragraph.minimumLineHeight = font.pointSize + PaxTheme.Spacing.medium
    paragraph.lineBreakMode = .byWordWrapping
    let content = NSMutableAttributedString(string: "")
    for (index, line) in lines.enumerated() {
      let title =
        sections.contains(line)
        || ["公网 IP", "语言", "时区", "DNS", "WebRTC", "完整读值"].contains(where: {
          line.hasSuffix(" · " + $0)
        })
        || line.hasPrefix("上次环境成功读值（历史）")
      let style = paragraph.mutableCopy() as? NSMutableParagraphStyle ?? paragraph
      if title && index > 0 { style.paragraphSpacingBefore = PaxTheme.Spacing.medium }
      let attributed = NSMutableAttributedString(
        string: line,
        attributes: [
          .font: font, .foregroundColor: PaxTheme.Color.text, .paragraphStyle: style,
        ])
      if title {
        attributed.addAttribute(
          .font, value: PaxTheme.Typography.label(size: font.pointSize, weight: .medium),
          range: NSRange(location: 0, length: attributed.length))
      } else if let colon = line.firstIndex(of: "："),
        line.distance(from: line.startIndex, to: colon) <= 18
      {
        let label = NSRange(line.startIndex...colon, in: line)
        attributed.addAttributes(
          [
            .foregroundColor: PaxTheme.Color.textSecondary,
            .font: PaxTheme.Typography.label(size: font.pointSize, weight: .medium),
          ], range: label)
      }
      content.append(attributed)
      if index < lines.count - 1 { content.append(NSAttributedString(string: "\n")) }
    }
    let field = text("")
    field.attributedStringValue = content
    field.isSelectable = true
    let inset = PaxTheme.Spacing.medium
    let result = PaxCard(
      insets: NSEdgeInsets(top: inset, left: inset, bottom: inset, right: inset), style: .inset)
    result.contentView = field
    result.heightAnchor.constraint(equalTo: field.heightAnchor, constant: inset * 2).isActive = true
    result.setAccessibilityElement(true)
    result.setAccessibilityRole(.group)
    result.setAccessibilityChildren([field])
    return result
  }

  func assessmentColor(_ tone: E.Tone) -> NSColor {
    switch tone {
    case .failure: PaxTheme.Color.error
    case .difference: PaxTheme.Color.amberLabel
    case .matching: PaxTheme.Color.healthy
    case .pending: PaxTheme.Color.textSecondary
    case .information: PaxTheme.Color.text
    }
  }

  func assessmentText(_ value: P.Assessment) -> NSView {
    let label = text(value.text)
    label.textColor = assessmentColor(value.tone)
    return padded(label, vertical: PaxTheme.Spacing.tight)
  }

}
