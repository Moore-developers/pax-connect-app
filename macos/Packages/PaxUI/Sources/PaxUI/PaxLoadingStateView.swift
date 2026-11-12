import AppKit

/// Shared loading/result presentation. Callers own work and cancellation; this view
/// only renders a state and label, with no business or network dependency.
public final class PaxLoadingStateView: NSView {
  public enum State { case idle, loading, success, partial, failure }
  private let spinner = NSProgressIndicator()
  private let label = NSTextField(wrappingLabelWithString: "")
  public init() {
    super.init(frame: .zero)
    spinner.style = .spinning
    spinner.controlSize = .small
    spinner.isDisplayedWhenStopped = false
    label.font = PaxTheme.Typography.label()
    let stack = NSStackView(views: [spinner, label])
    stack.orientation = .horizontal
    stack.alignment = .centerY
    stack.spacing = PaxTheme.Spacing.regular
    stack.translatesAutoresizingMaskIntoConstraints = false
    addSubview(stack)
    NSLayoutConstraint.activate([
      stack.leadingAnchor.constraint(equalTo: leadingAnchor),
      stack.trailingAnchor.constraint(equalTo: trailingAnchor),
      stack.topAnchor.constraint(equalTo: topAnchor),
      stack.bottomAnchor.constraint(equalTo: bottomAnchor),
    ])
    setAccessibilityIdentifier("loading-state")
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public func apply(_ state: State, message: String) {
    label.stringValue = message
    switch state {
    case .loading: spinner.startAnimation(nil)
    default: spinner.stopAnimation(nil)
    }
    label.textColor = state == .failure ? PaxTheme.Color.error : PaxTheme.Color.text
    isHidden = state == .idle && message.isEmpty
    setAccessibilityLabel(message)
  }
}
