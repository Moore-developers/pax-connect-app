import AppKit

/// 空状态：符号 + 标题 + 说明 + 可选 CTA；不伪造数据，缺什么写什么。
public final class PaxEmptyState: NSView {
  private let captionField: NSTextField
  private var actionButton: PaxButton?

  public init(
    symbol: String, title: String, caption: String, actionTitle: String? = nil,
    target: AnyObject? = nil, action: Selector? = nil, identifier: String? = nil
  ) {
    let icon = PaxIcon.view(symbol, size: 28, weight: .regular)
    let titleField = NSTextField(labelWithString: title)
    titleField.font = PaxTheme.Typography.title(15)
    titleField.textColor = PaxTheme.Color.text
    captionField = NSTextField(wrappingLabelWithString: caption)
    captionField.font = PaxTheme.Typography.label(size: 12)
    captionField.textColor = PaxTheme.Color.textSecondary
    captionField.alignment = .center

    let stack = NSStackView(views: [icon, titleField, captionField])
    stack.orientation = .vertical
    stack.alignment = .centerX
    stack.spacing = PaxTheme.Spacing.tight
    if let actionTitle {
      let button = PaxButton(.secondary, title: actionTitle, target: target, action: action)
      actionButton = button
      stack.addArrangedSubview(button)
      stack.setCustomSpacing(PaxTheme.Spacing.regular, after: captionField)
    }
    if let identifier { stack.setAccessibilityIdentifier(identifier) }
    super.init(frame: .zero)
    addSubview(stack)
    stack.translatesAutoresizingMaskIntoConstraints = false
    NSLayoutConstraint.activate([
      stack.centerXAnchor.constraint(equalTo: centerXAnchor),
      stack.centerYAnchor.constraint(equalTo: centerYAnchor),
      stack.leadingAnchor.constraint(
        greaterThanOrEqualTo: leadingAnchor, constant: PaxTheme.Spacing.regular),
      stack.trailingAnchor.constraint(
        lessThanOrEqualTo: trailingAnchor, constant: -PaxTheme.Spacing.regular),
    ])
  }

  @available(*, unavailable)
  public required init?(coder: NSCoder) {
    fatalError("PaxUI 不使用 XIB/Storyboard（code-standards.md §二）")
  }

  public func update(caption: String) {
    captionField.stringValue = caption
  }
}
