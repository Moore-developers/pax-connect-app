import AppKit

/// Shared settings section: heading, card, rows and separators, without feature state.
public final class PaxPreferenceGroup: NSStackView {
  public init(title: String, symbol: String, rows: [NSView]) {
    super.init(frame: .zero)
    orientation = .vertical
    alignment = .leading
    spacing = PaxTheme.Spacing.tight
    let heading = NSStackView(views: [
      NSImageView(image: PaxIcon.image(symbol, size: PaxTheme.Control.navigationIcon) ?? NSImage()),
      PaxText.label(
        title, font: PaxTheme.Typography.label(size: PaxTheme.Routes.bodyFont, weight: .medium)),
      NSView(),
    ])
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
    let card = PaxCard(style: .routesSection)
    card.contentView = body
    body.bottomAnchor.constraint(equalTo: card.bottomAnchor, constant: -PaxTheme.Routes.sectionTop)
      .isActive = true
    for item in [heading, card] {
      addArrangedSubview(item)
      item.widthAnchor.constraint(equalTo: widthAnchor).isActive = true
    }
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
}
