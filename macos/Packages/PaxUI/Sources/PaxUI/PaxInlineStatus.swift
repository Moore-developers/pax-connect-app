import AppKit

/// Shared compact feedback. All actions are supplied by the owning feature.
public final class PaxInlineStatus: NSView {
  public enum State { case idle, working, success, warning, failure }
  public var onAction: (() -> Void)?
  public var onMore: (() -> Void)?
  public var onDismiss: (() -> Void)?
  private let spinner = NSProgressIndicator()
  private let symbol = NSImageView()
  private let label = NSTextField(labelWithString: "")
  private let action = NSButton(title: "", target: nil, action: nil)
  private let more = NSButton(title: "", target: nil, action: nil)
  private let close = NSButton(title: "", target: nil, action: nil)
  private lazy var fade = PaxFadeVisibility(self)
  /// 带淡变的显示/隐藏；淡出期间不响应点击。
  public func setVisible(_ visible: Bool) { fade.set(visible) }
  public override func hitTest(_ point: NSPoint) -> NSView? {
    fade.isHiding ? nil : super.hitTest(point)
  }
  public override var intrinsicContentSize: NSSize {
    NSSize(width: PaxTheme.InlineStatus.width, height: PaxTheme.Routes.controlHeight)
  }
  public init() {
    super.init(frame: .zero)
    wantsLayer = true
    spinner.style = .spinning
    spinner.controlSize = .small
    label.font = PaxTheme.Typography.caption()
    label.lineBreakMode = .byTruncatingTail
    for button in [action, more, close] {
      button.isBordered = false
      button.font = PaxTheme.Typography.caption()
      button.contentTintColor = PaxTheme.Color.accent
      button.target = self
      button.setContentCompressionResistancePriority(.required, for: .horizontal)
    }
    action.action = #selector(performAction)
    more.action = #selector(showMore)
    close.action = #selector(dismissNotice)
    close.image = NSImage(systemSymbolName: "xmark", accessibilityDescription: "关闭提示")
    close.contentTintColor = PaxTheme.Color.textSecondary
    close.toolTip = "仅关闭提示，不改变连接状态"
    for item in [spinner, symbol, label, action, more, close] { addSubview(item) }
    setAccessibilityIdentifier("proxy-inline-status")
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public func apply(
    _ state: State, text: String, action title: String? = nil,
    additionalCount: Int = 0, dismissible: Bool = false, detail: String? = nil
  ) {
    label.stringValue = text
    toolTip = detail ?? text
    action.title = title.map { $0 + " ›" } ?? ""
    action.isHidden = title == nil
    more.title = "查看 \(additionalCount) 项"
    more.isHidden = additionalCount == 0
    close.isHidden = !dismissible
    spinner.isHidden = state != .working
    symbol.isHidden = state == .working || state == .idle
    symbol.image = NSImage(
      systemSymbolName: state == .success ? "checkmark.circle" : "exclamationmark.circle",
      accessibilityDescription: nil)
    if state == .working { spinner.startAnimation(nil) } else { spinner.stopAnimation(nil) }
    let color: NSColor =
      state == .failure
      ? PaxTheme.Color.error
      : state == .warning
        ? PaxTheme.Color.warning
        : state == .success ? PaxTheme.Color.healthy : PaxTheme.Color.textSecondary
    label.textColor = color
    symbol.contentTintColor = color
    layer?.backgroundColor = color.withAlphaComponent(PaxTheme.Color.noticeFillOpacity).cgColor
    layer?.cornerRadius = PaxTheme.Radius.control
    setAccessibilityLabel(text)
    needsLayout = true
  }
  public override func layout() {
    super.layout()
    let inset = PaxTheme.Spacing.tight
    let icon = PaxTheme.Control.navigationIcon
    var trailing = bounds.width - inset
    for button in [close, more, action] where !button.isHidden {
      let width = max(icon, button.fittingSize.width)
      trailing -= width
      button.frame = NSRect(x: trailing, y: 0, width: width, height: bounds.height)
      trailing -= inset
    }
    let leading = spinner.isHidden && symbol.isHidden ? inset : inset + icon + inset
    let iconFrame = NSRect(x: inset, y: (bounds.height - icon) / 2, width: icon, height: icon)
    spinner.frame = iconFrame
    symbol.frame = iconFrame
    label.frame = NSRect(
      x: leading, y: (bounds.height - label.intrinsicContentSize.height) / 2,
      width: max(0, trailing - leading), height: label.intrinsicContentSize.height)
  }
  @objc private func performAction() { onAction?() }
  @objc private func showMore() { onMore?() }
  @objc private func dismissNotice() { onDismiss?() }
}
