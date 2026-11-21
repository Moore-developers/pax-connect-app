import AppKit
import PaxCore
import PaxUI

/// 新建与改名共用的名称编辑浮层；确认成功才更新父窗口草稿。
@MainActor
final class RouteGroupNameController: NSViewController {
  enum Mode {
    case create
    case rename(String)
  }
  private let context: PresentationContext
  private let mode: Mode
  private let applyName: (String) throws -> Void
  private let dismiss: () -> Void
  private let input = PaxRouteField("")
  private lazy var message = PaxText.label(
    context.text("routing_copy.c8b21c62bd"), font: PaxTheme.Typography.caption(), secondary: true)
  init(
    mode: Mode = .create, context: PresentationContext = .chinese,
    confirm: @escaping (String) throws -> Void, dismiss: @escaping () -> Void
  ) {
    self.context = context
    self.mode = mode
    self.applyName = confirm
    self.dismiss = dismiss
    super.init(nibName: nil, bundle: nil)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    view = NSView()
    let titleText: String
    let actionText: String
    switch mode {
    case .create:
      titleText = context.text("routing_copy.3d49d9fcb6")
      actionText = context.text("routing_copy.cde2cd071d")
    case .rename(let name):
      titleText = context.text("routing_copy.d4d65e113b")
      actionText = context.text("routing_copy.a3030bf8f1")
      input.stringValue = name
    }
    let title = PaxText.label(
      titleText,
      font: PaxTheme.Typography.label(size: PaxTheme.Routes.auxiliaryTitleFont, weight: .semibold))
    input.placeholderString = context.text("routing_copy.6621657b14")
    input.setAccessibilityLabel(context.text("routing_copy.d7e266bdc8"))
    input.setAccessibilityIdentifier("group-name-editor")
    input.identifier = .init("group-name-editor")
    input.target = self
    input.action = #selector(confirmName)
    let cancel = PaxRouteButton(
      context.text("routing_copy.2cd0f3be87"), target: self, action: #selector(cancel))
    cancel.keyEquivalent = "\u{1b}"
    let create = PaxRouteButton(
      actionText, style: .primary, target: self, action: #selector(confirmName))
    create.keyEquivalent = "\r"
    let actions = NSStackView(views: [NSView(), cancel, create])
    actions.spacing = PaxTheme.Spacing.tight
    message.maximumNumberOfLines = 2
    message.lineBreakMode = .byWordWrapping
    let content = NSStackView(views: [title, input, message, actions])
    content.orientation = .vertical
    content.alignment = .leading
    content.spacing = PaxTheme.Spacing.regular
    content.translatesAutoresizingMaskIntoConstraints = false
    view.addSubview(content)
    for item in [input, message, actions] {
      item.widthAnchor.constraint(equalTo: content.widthAnchor).isActive = true
    }
    NSLayoutConstraint.activate([
      content.leadingAnchor.constraint(
        equalTo: view.leadingAnchor, constant: PaxTheme.Spacing.medium),
      content.trailingAnchor.constraint(
        equalTo: view.trailingAnchor, constant: -PaxTheme.Spacing.medium),
      content.topAnchor.constraint(equalTo: view.topAnchor, constant: PaxTheme.Spacing.medium),
      content.bottomAnchor.constraint(
        equalTo: view.bottomAnchor, constant: -PaxTheme.Spacing.medium),
    ])
    preferredContentSize = NSSize(
      width: PaxTheme.Groups.namePopoverWidth,
      height: content.fittingSize.height + PaxTheme.Spacing.medium * 2)
    view.frame = NSRect(origin: .zero, size: preferredContentSize)
  }
  override func viewDidAppear() {
    super.viewDidAppear()
    view.window?.makeFirstResponder(input)
    if case .rename = mode { input.selectText(nil) }
  }
  @objc private func confirmName() {
    do {
      try applyName(input.stringValue)
      dismiss()
    } catch {
      message.stringValue =
        (error as? RouteWorkbenchStore.EditError)?.message(context: context)
        ?? context.text("routing_copy.1e08246a8b")
      message.textColor = PaxTheme.Color.error
    }
  }
  @objc private func cancel() { dismiss() }
  override func cancelOperation(_ sender: Any?) { dismiss() }
}
