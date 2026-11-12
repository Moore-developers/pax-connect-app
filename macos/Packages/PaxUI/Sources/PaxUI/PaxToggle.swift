import AppKit

/// 开关：原生 NSSwitch + 标签，不自制滑块。
public final class PaxToggle: NSView {
  private let toggle = NSSwitch()
  private let label = NSTextField(labelWithString: "")

  public var isChecked: Bool {
    get { toggle.state == .on }
    set { toggle.state = newValue ? .on : .off }
  }

  public var target: AnyObject? {
    get { toggle.target }
    set { toggle.target = newValue }
  }

  public var action: Selector? {
    get { toggle.action }
    set { toggle.action = newValue }
  }

  public var isEnabled: Bool {
    get { toggle.isEnabled }
    set {
      toggle.isEnabled = newValue
      label.textColor = newValue ? PaxTheme.Color.text : PaxTheme.Color.textSecondary
    }
  }

  public override var intrinsicContentSize: NSSize {
    let size = toggle.intrinsicContentSize
    return NSSize(
      width: size.width
        + (label.isHidden ? 0 : label.intrinsicContentSize.width + PaxTheme.Spacing.tight),
      height: max(size.height, label.isHidden ? 0 : label.intrinsicContentSize.height))
  }

  public init(
    label title: String, showsLabel: Bool = true, target: AnyObject? = nil, action: Selector? = nil
  ) {
    super.init(frame: .zero)
    label.stringValue = title
    label.isHidden = !showsLabel
    label.font = PaxTheme.Typography.label()
    label.textColor = PaxTheme.Color.text
    self.target = target
    self.action = action
    setAccessibilityLabel(title)
    toggle.setAccessibilityLabel(title)
    let stack = NSStackView(views: [label, toggle])
    stack.orientation = .horizontal
    stack.spacing = PaxTheme.Spacing.tight
    addSubview(stack)
    stack.translatesAutoresizingMaskIntoConstraints = false
    NSLayoutConstraint.activate([
      stack.leadingAnchor.constraint(equalTo: leadingAnchor),
      stack.trailingAnchor.constraint(equalTo: trailingAnchor),
      stack.topAnchor.constraint(equalTo: topAnchor),
      stack.bottomAnchor.constraint(equalTo: bottomAnchor),
    ])
  }

  @available(*, unavailable)
  public required init?(coder: NSCoder) {
    fatalError("PaxUI 不使用 XIB/Storyboard（code-standards.md §二）")
  }
}
