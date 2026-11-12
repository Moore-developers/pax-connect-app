import AppKit

/// Reusable two-line metric: both labels share the column's complete content width.
public final class PaxMetricCell: NSView {
  private let value = PaxText.label("", font: PaxTheme.Typography.metric(size: PaxTheme.Table.font))
  private let caption = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  public init() {
    super.init(frame: .zero)
    for label in [value, caption] {
      label.alignment = .right
      label.lineBreakMode = .byTruncatingTail
      label.translatesAutoresizingMaskIntoConstraints = false
      addSubview(label)
      label.leadingAnchor.constraint(equalTo: leadingAnchor).isActive = true
      label.trailingAnchor.constraint(equalTo: trailingAnchor).isActive = true
    }
    NSLayoutConstraint.activate([
      value.topAnchor.constraint(equalTo: topAnchor),
      caption.topAnchor.constraint(equalTo: value.bottomAnchor, constant: PaxTheme.Spacing.xs),
      caption.bottomAnchor.constraint(equalTo: bottomAnchor),
    ])
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public func apply(value: String, caption: String) {
    self.value.stringValue = value
    self.caption.stringValue = caption
    toolTip = value + " · " + caption
  }
}
