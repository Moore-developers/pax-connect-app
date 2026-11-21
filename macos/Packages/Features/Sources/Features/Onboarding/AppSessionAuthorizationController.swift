import AppKit
import Observation
import PaxCore
import PaxUI

/// Explicit local sudo authorization. No password is retained in a Store or preferences.
@MainActor
public final class AppSessionAuthorizationController: NSViewController {
  private let store: AppSessionStore
  private let context: PresentationContext
  private let onReady: () -> Void
  private let onCancel: () -> Void
  private let password = NSSecureTextField()
  private let message = NSTextField(wrappingLabelWithString: "")
  private let submit = NSButton(title: "", target: nil, action: nil)
  private let cancel = NSButton(title: "", target: nil, action: nil)
  public init(
    store: AppSessionStore, context: PresentationContext,
    onReady: @escaping () -> Void, onCancel: @escaping () -> Void
  ) {
    self.store = store
    self.context = context
    self.onReady = onReady
    self.onCancel = onCancel
    super.init(nibName: nil, bundle: nil)
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override func loadView() {
    let title = NSTextField(labelWithString: context.text("app_session.title"))
    title.font = PaxTheme.Typography.section
    let explanation = NSTextField(wrappingLabelWithString: context.text("app_session.detail"))
    explanation.font = PaxTheme.Typography.caption()
    explanation.textColor = PaxTheme.Color.textSecondary
    password.placeholderString = context.text("app_session.password")
    password.setAccessibilityLabel(context.text("app_session.password"))
    password.setAccessibilityIdentifier("session-password")
    password.target = self
    password.action = #selector(authorize)
    submit.target = self
    submit.action = #selector(authorize)
    submit.bezelStyle = .rounded
    cancel.target = self
    cancel.title = context.text("common.cancel")
    cancel.action = #selector(cancelAuthorization)
    cancel.bezelStyle = .rounded
    let actions = NSStackView(views: [cancel, submit])
    let stack = NSStackView(views: [title, explanation, password, message, actions])
    stack.orientation = .vertical
    stack.alignment = .leading
    stack.spacing = PaxTheme.Spacing.regular
    stack.edgeInsets = NSEdgeInsets(
      top: PaxTheme.Spacing.roomy, left: PaxTheme.Spacing.roomy,
      bottom: PaxTheme.Spacing.roomy, right: PaxTheme.Spacing.roomy)
    stack.widthAnchor.constraint(equalToConstant: PaxTheme.Layout.sheet.width / 2).isActive = true
    for label in [explanation, message] {
      label.widthAnchor.constraint(
        equalTo: stack.widthAnchor, constant: -2 * PaxTheme.Spacing.roomy
      ).isActive = true
    }
    password.widthAnchor.constraint(equalToConstant: PaxTheme.Layout.searchWidth).isActive = true
    view = stack
  }
  public override func viewDidAppear() {
    super.viewDidAppear()
    view.window?.makeFirstResponder(password)
  }
  public override func viewDidLoad() {
    super.viewDidLoad()
    observe()
  }
  private func observe() {
    withObservationTracking {
      message.stringValue =
        store.state == .starting
        ? context.text("app_session.starting") : store.failure?.message(context: context) ?? ""
      message.font = PaxTheme.Typography.caption()
      message.textColor =
        store.state == .starting ? PaxTheme.Color.textSecondary : PaxTheme.Color.error
      submit.isEnabled = store.state != .starting
      cancel.isEnabled = store.state != .starting
      password.isEnabled = store.state != .starting
      password.isHidden = store.state != .needsPassword && store.state != .starting
      submit.title = context.text(
        store.state == .needsPassword ? "app_session.submit" : "app_session.retry")
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observe() }
    }
  }
  @objc private func authorize() {
    guard submit.isEnabled else { return }
    let needsPassword = store.state == .needsPassword
    guard !needsPassword || !password.stringValue.isEmpty else { return }
    let value: String? = needsPassword ? password.stringValue : nil
    password.stringValue = ""
    Task { [weak self, store] in
      await store.start(password: value)
      if store.state == .running { self?.onReady() }
    }
  }
  public override func cancelOperation(_ sender: Any?) { cancelAuthorization() }
  @objc private func cancelAuthorization() {
    guard cancel.isEnabled else { return }
    password.stringValue = ""
    onCancel()
    dismiss(self)
  }
}
