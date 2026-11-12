import AppKit
import Observation
import PaxCore
import PaxUI

/// 最小后台服务引导；不触及 ServiceManagement。
@MainActor
public final class DaemonSetupViewController: NSViewController {
  private let store: DaemonSetupStore
  private let onEnable: @MainActor () -> Void
  private let onCancel: (@MainActor () -> Void)?
  private let onCheck: @MainActor () -> Void
  private let label = NSTextField(wrappingLabelWithString: "")
  private let enable = NSButton(title: "启用后台服务", target: nil, action: nil)

  public init(
    store: DaemonSetupStore, onEnable: @escaping @MainActor () -> Void,
    onCheck: @escaping @MainActor () -> Void, onCancel: (@MainActor () -> Void)? = nil
  ) {
    self.store = store
    self.onEnable = onEnable
    self.onCheck = onCheck
    self.onCancel = onCancel
    super.init(nibName: nil, bundle: nil)
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }

  public override func loadView() {
    view = NSView(
      frame: NSRect(
        x: 0, y: 0, width: PaxTheme.InlineStatus.detailsWidth,
        height: PaxTheme.InlineStatus.detailsMaximumHeight))
    let title = NSTextField(labelWithString: "首次连接需要系统授权")
    title.font = PaxTheme.Typography.metric(size: 24)
    label.setAccessibilityIdentifier("daemon-setup-state")
    enable.target = self
    enable.action = #selector(enableService)
    let explanation = NSTextField(
      wrappingLabelWithString: "授权由 macOS 完成；如需密码，请在系统窗口输入。授权成功后继续连接，取消则保持直连。")
    var contents: [NSView] = [title, explanation, label, enable]
    if onCancel != nil {
      contents.append(NSButton(title: "取消", target: self, action: #selector(cancelSetup)))
    }
    let stack = NSStackView(views: contents)
    stack.orientation = .vertical
    stack.alignment = .leading
    stack.spacing = PaxTheme.Spacing.roomy
    stack.translatesAutoresizingMaskIntoConstraints = false
    view.addSubview(stack)
    NSLayoutConstraint.activate([
      stack.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: PaxTheme.Spacing.roomy),
      stack.topAnchor.constraint(equalTo: view.topAnchor, constant: PaxTheme.Spacing.roomy),
      stack.trailingAnchor.constraint(
        lessThanOrEqualTo: view.trailingAnchor, constant: -PaxTheme.Spacing.roomy),
    ])
  }
  public override func viewDidLoad() {
    super.viewDidLoad()
    observe()
  }
  private func observe() {
    withObservationTracking {
      label.stringValue = store.message
      switch store.state {
      case .requiresApproval: enable.title = "前往系统授权"
      case .enabled: enable.title = "重新检查连接"
      default: enable.title = "授权并连接"
      }
      if store.error == .serviceIdentityUnknown { enable.title = "重新登记后台服务" }
      if store.error == .updateFailed { enable.title = "重试更新" }
      enable.isEnabled = !store.busy
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observe() }
    }
  }
  public override func cancelOperation(_ sender: Any?) { cancelSetup() }
  @objc private func cancelSetup() {
    onCancel?()
    dismiss(self)
  }
  @objc private func enableService() {
    if store.error == .serviceIdentityUnknown || store.error == .updateFailed {
      onEnable()
      return
    }
    switch store.state {
    case .requiresApproval: store.openSettings()
    case .enabled: onCheck()
    default: onEnable()
    }
  }
}
