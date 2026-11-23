import AppKit

/// Shared settings section: heading, card, rows and separators, without feature state.
public final class PaxPreferenceGroup: NSStackView {
  public enum Style { case standard, compact }
  private let titleField: NSTextField
  private let summaryField = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)

  public init(
    title: String, symbol: String, rows: [NSView], style: Style = .standard,
    headingAccessory: NSView? = nil, footer: NSView? = nil
  ) {
    titleField = PaxText.label(
      title, font: PaxTheme.Typography.label(size: PaxTheme.Routes.bodyFont, weight: .medium))
    super.init(frame: .zero)
    orientation = .vertical
    alignment = .leading
    spacing = PaxTheme.Spacing.tight
    titleField.setContentCompressionResistancePriority(.required, for: .horizontal)
    summaryField.lineBreakMode = .byTruncatingTail
    summaryField.isHidden = true
    let heading = NSStackView(
      views: [
        NSImageView(
          image: PaxIcon.image(symbol, size: PaxTheme.Control.navigationIcon) ?? NSImage()),
        titleField,
      ] + (headingAccessory.map { [$0] } ?? []) + [summaryField, NSView()])
    heading.alignment = .centerY
    heading.spacing = PaxTheme.Spacing.tight
    let body = NSStackView()
    body.orientation = .vertical
    body.alignment = .leading
    body.spacing = 0
    for (index, row) in rows.enumerated() {
      if index > 0 {
        let separator = NSBox()
        separator.boxType = .separator
        body.addArrangedSubview(separator)
        separator.widthAnchor.constraint(equalTo: body.widthAnchor).isActive = true
      }
      body.addArrangedSubview(row)
      row.widthAnchor.constraint(equalTo: body.widthAnchor).isActive = true
    }
    let verticalInset = style == .compact ? 0 : PaxTheme.Routes.sectionTop
    let horizontalInset =
      style == .compact ? PaxTheme.Settings.compactHorizontalInset : PaxTheme.Routes.sectionInset
    let card = PaxCard(
      insets: NSEdgeInsets(
        top: verticalInset, left: horizontalInset,
        bottom: verticalInset, right: horizontalInset),
      style: .routesSection)
    card.contentView = body
    body.bottomAnchor.constraint(equalTo: card.bottomAnchor, constant: -verticalInset)
      .isActive = true
    for item in [heading, card] {
      addArrangedSubview(item)
      item.widthAnchor.constraint(equalTo: widthAnchor).isActive = true
    }
    if let footer {
      let container = NSView()
      footer.translatesAutoresizingMaskIntoConstraints = false
      container.addSubview(footer)
      addArrangedSubview(container)
      NSLayoutConstraint.activate([
        container.widthAnchor.constraint(equalTo: widthAnchor),
        footer.leadingAnchor.constraint(
          equalTo: container.leadingAnchor, constant: horizontalInset),
        footer.trailingAnchor.constraint(
          equalTo: container.trailingAnchor, constant: -horizontalInset),
        footer.topAnchor.constraint(equalTo: container.topAnchor),
        footer.bottomAnchor.constraint(equalTo: container.bottomAnchor),
      ])
    }
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public func apply(title: String) { titleField.stringValue = title }
  public func apply(summary: String) {
    summaryField.stringValue = summary
    summaryField.toolTip = summary.isEmpty ? nil : summary
    summaryField.isHidden = summary.isEmpty
  }
}
