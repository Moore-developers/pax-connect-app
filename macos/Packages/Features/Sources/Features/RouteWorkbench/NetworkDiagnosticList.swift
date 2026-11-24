import AppKit
import PaxCore
import PaxUI

extension NetworkDiagnosticView: NSGestureRecognizerDelegate {
  func diagnosticTable(_ rows: [NSView]) -> NSView {
    let fields = [
      "interface_detail.79f326be44", "event.field.conclusion", "diagnostic.overview.basis",
    ].map {
      text(context.text($0), secondary: true)
    }
    let header = columns(fields[0], fields[1], fields[2])
    header.heightAnchor.constraint(equalToConstant: PaxTheme.Table.compactHeader).isActive = true
    var content: [NSView] = [header]
    for row in rows { content += [divider(), row] }
    return card(vertical(content, spacing: 0), style: .routesList)
  }

  private func columns(_ item: NSView, _ verdict: NSView, _ reason: NSView) -> NSStackView {
    let row = horizontal([item, verdict, reason])
    let widths = PaxTheme.NetworkDiagnostics.serviceColumns
    item.widthAnchor.constraint(equalTo: row.widthAnchor, multiplier: widths[0]).isActive = true
    verdict.widthAnchor.constraint(equalTo: row.widthAnchor, multiplier: widths[1]).isActive = true
    return row
  }

  func diagnosticRow(
    title: String, kind: String? = nil, subtitle: String, symbol: String, detail: String = "",
    tone: E.Tone = .information, verdict: String? = nil, basis: String? = nil,
    detailAction: NSButton? = nil
  ) -> NSView {
    let parts = subtitle.components(separatedBy: " · ")
    let conclusion = verdict ?? parts[0]
    let remainder = parts.dropFirst().joined(separator: " · ")
    let reason =
      basis
      ?? (remainder.isEmpty
        ? detail.components(separatedBy: .newlines).first(where: { !$0.isEmpty && $0 != subtitle })
          ?? "—"
        : remainder)
    let icon = PaxIcon.view(symbol, size: PaxTheme.Table.font)
    icon.contentTintColor = PaxTheme.Color.textSecondary
    icon.widthAnchor.constraint(equalToConstant: PaxTheme.NetworkDiagnostics.environmentIcon)
      .isActive = true
    let name = text(title)
    name.maximumNumberOfLines = 2
    let color: PaxToneBadge.Tone =
      switch tone {
      case .matching: .success
      case .difference: .warning
      case .failure: .error
      case .information, .pending: .neutral
      }
    let badge = PaxToneBadge(conclusion, tone: color)
    badge.toolTip = conclusion
    badge.setContentHuggingPriority(.required, for: .horizontal)
    badge.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    let summary = text(reason, secondary: true)
    summary.maximumNumberOfLines = 2
    summary.lineBreakMode = .byTruncatingTail
    summary.toolTip = reason
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
    toggle.image = PaxIcon.image(
      expanded ? "chevron.down" : "chevron.right", size: PaxTheme.Dashboard.captionFont)
    toggle.imagePosition = .imageOnly
    toggle.contentTintColor = PaxTheme.Color.textSecondary
    toggle.controlSize = .small
    toggle.setAccessibilityIdentifier(key)
    toggle.setAccessibilityLabel(
      title
        + (expanded
          ? context.text("native_diagnostic.0ae7312510")
          : context.text("native_diagnostic.4bf0fb2140")))
    let line = columns(
      horizontal([icon, name], spacing: PaxTheme.Spacing.tight),
      horizontal([badge, NSView()], spacing: 0),
      horizontal([summary, toggle], spacing: PaxTheme.Spacing.tight))
    line.heightAnchor.constraint(greaterThanOrEqualToConstant: PaxTheme.Table.compactRow).isActive =
      true
    let click = NSClickGestureRecognizer(
      target: toggle, action: #selector(NSButton.performClick(_:)))
    click.delegate = self
    line.addGestureRecognizer(click)
    line.setAccessibilityElement(true)
    line.setAccessibilityRole(.group)
    line.setAccessibilityLabel(title)
    line.setAccessibilityChildren([name, badge, summary, toggle])
    guard expanded else { return line }
    let evidence = diagnosticDetailCard(detail.isEmpty ? subtitle : detail, omitting: subtitle)
    evidence.setAccessibilityIdentifier(key + "-evidence")
    var views = [line, padded(evidence, vertical: PaxTheme.Spacing.tight)]
    if let detailAction { views.append(horizontal([detailAction, NSView()])) }
    return vertical(views, spacing: 0)
  }

  func gestureRecognizer(
    _ gestureRecognizer: NSGestureRecognizer, shouldAttemptToRecognizeWith event: NSEvent
  ) -> Bool {
    guard let button = gestureRecognizer.target as? NSButton else { return true }
    return !button.bounds.contains(button.convert(event.locationInWindow, from: nil))
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

  func topicSummary(
    _ finding: String, tone: E.Tone, metadata: String, note: String? = nil
  ) -> NSView {
    let label = PaxRouteText.label("", font: PaxTheme.Typography.caption(), secondary: true)
    label.maximumNumberOfLines = 1
    label.lineBreakMode = .byTruncatingTail
    let text = [finding, metadata].filter { !$0.isEmpty }.joined(separator: " · ")
    let styled = NSMutableAttributedString(
      string: text,
      attributes: [
        .font: PaxTheme.Typography.caption(),
        .foregroundColor: label.textColor ?? PaxTheme.Color.textSecondary,
      ])
    if !finding.isEmpty {
      styled.addAttribute(
        .foregroundColor, value: assessmentColor(tone),
        range: NSRange(location: 0, length: finding.utf16.count))
    }
    label.attributedStringValue = styled
    label.toolTip = [text, note].compactMap { $0 }.joined(separator: "\n")
    label.setAccessibilityIdentifier("diagnostic-topic-summary")
    return label
  }

}
