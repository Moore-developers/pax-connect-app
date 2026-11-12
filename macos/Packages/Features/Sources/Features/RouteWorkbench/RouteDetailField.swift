import AppKit
import PaxUI

/// A detail value keeps its label above it so both grid columns can wrap independently.
@MainActor
final class RouteDetailField: NSStackView {
  let value: NSTextField

  init(_ title: String, value text: String = "", identifier: String) {
    value = PaxRouteText.label(text, wrapping: true)
    value.isSelectable = true
    value.setAccessibilityIdentifier(identifier)
    super.init(frame: .zero)
    orientation = .vertical
    alignment = .leading
    spacing = PaxTheme.Spacing.xs
    addArrangedSubview(
      PaxRouteText.label(title, font: PaxTheme.Typography.caption(), secondary: true))
    addArrangedSubview(value)
    for child in arrangedSubviews {
      child.widthAnchor.constraint(equalTo: widthAnchor).isActive = true
    }
  }

  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
}
