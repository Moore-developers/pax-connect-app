import AppKit

/// A compact filter row shares control height, baseline and spacing with route controls.
/// Callers own widths and filtering; the component owns only presentation.
public final class PaxFilterRow: NSStackView {
  public init(_ controls: [NSView]) {
    super.init(frame: .zero)
    orientation = .horizontal
    alignment = .centerY
    spacing = PaxTheme.Spacing.tight
    for control in controls {
      addArrangedSubview(control)
      control.heightAnchor.constraint(equalToConstant: PaxTheme.Control.filterHeight).isActive =
        true
    }
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
}

/// Equal-width range inputs; validation, units and delegate callbacks belong to the caller.
public final class PaxFilterRange: NSStackView {
  public let minimum = PaxRouteField("", style: .filter)
  public let maximum = PaxRouteField("", style: .filter)

  public init(title: String) {
    super.init(frame: .zero)
    orientation = .horizontal
    alignment = .centerY
    spacing = PaxTheme.Spacing.tight
    let label = PaxText.label(title, font: PaxTheme.Typography.caption(), secondary: true)
    let separator = PaxText.label("–", font: PaxTheme.Typography.caption(), secondary: true)
    for text in [label, separator] {
      text.setContentCompressionResistancePriority(.required, for: .horizontal)
      text.setContentHuggingPriority(.required, for: .horizontal)
    }
    for field in [minimum, maximum] {
      field.font = PaxTheme.Typography.metric(size: PaxTheme.Table.font)
    }
    for view in [label, minimum, separator, maximum] { addArrangedSubview(view) }
    minimum.widthAnchor.constraint(equalTo: maximum.widthAnchor).isActive = true
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
}
