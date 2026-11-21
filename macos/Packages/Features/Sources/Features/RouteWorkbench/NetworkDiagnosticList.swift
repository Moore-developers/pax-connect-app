import AppKit
import PaxCore
import PaxUI

extension NetworkDiagnosticView {
  func diagnosticRow(
    title: String, kind: String? = nil, subtitle: String, symbol: String, detail: String = "",
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
    let key = "diagnostic-row-" + (kind ?? title)
    let expanded = store.expanded.contains(key)
    let toggle = action(
      expanded
        ? context.text("native_diagnostic.afd4b78353")
        : context.text("native_diagnostic.979a332955")
    ) { [weak self] in
      guard let self else { return }
      if store.expanded.contains(key) {
        store.expanded.remove(key)
      } else {
        store.expanded.insert(key)
      }
    }
    toggle.controlSize = .small
    toggle.setAccessibilityIdentifier(key)
    toggle.setAccessibilityLabel(
      title
        + (expanded
          ? context.text("native_diagnostic.0ae7312510")
          : context.text("native_diagnostic.4bf0fb2140")))
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
    let sections = [
      context.text("native_diagnostic.ae4d388f24"), context.text("native_diagnostic.f20d58c484"),
      context.text("native_diagnostic.5614081437"), context.text("native_diagnostic.d46cb91a2d"),
    ]
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
        || [
          context.text("native_diagnostic.946d1e4eb0"),
          context.text("native_diagnostic.9f6fee1aba"),
          context.text("native_diagnostic.b5d72c5c00"), "DNS", "WebRTC",
          context.text("native_diagnostic.8afecdeb38"),
        ].contains(where: {
          line.hasSuffix(" · " + $0)
        })
        || line.hasPrefix(context.text("native_diagnostic.a7a79ba0d3"))
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
