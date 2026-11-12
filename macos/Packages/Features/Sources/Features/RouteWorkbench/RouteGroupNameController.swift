import AppKit
import PaxUI

/// 新建与改名共用的名称编辑浮层；确认成功才更新父窗口草稿。
@MainActor
final class RouteGroupNameController: NSViewController {
  enum Mode {
    case create
    case rename(String)
  }
  private let mode: Mode
  private let applyName: (String) throws -> Void
  private let dismiss: () -> Void
  private let input = PaxRouteField("")
  private let message = PaxText.label(
    "1–24 个字，与已有分组不同。", font: PaxTheme.Typography.caption(), secondary: true)
  init(
    mode: Mode = .create, confirm: @escaping (String) throws -> Void, dismiss: @escaping () -> Void
  ) {
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
      titleText = "新建分组"
      actionText = "创建"
    case .rename(let name):
      titleText = "编辑名称"
      actionText = "保存"
      input.stringValue = name
    }
    let title = PaxText.label(
      titleText,
      font: PaxTheme.Typography.label(size: PaxTheme.Routes.auxiliaryTitleFont, weight: .semibold))
    input.placeholderString = "输入分组名称"
    input.setAccessibilityLabel("分组名称")
    input.setAccessibilityIdentifier("group-name-editor")
    input.identifier = .init("group-name-editor")
    input.target = self
    input.action = #selector(confirmName)
    let cancel = PaxRouteButton("取消", target: self, action: #selector(cancel))
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
      message.stringValue = (error as? RouteWorkbenchStore.EditError)?.message ?? "请输入有效的分组名称"
      message.textColor = PaxTheme.Color.error
    }
  }
  @objc private func cancel() { dismiss() }
  override func cancelOperation(_ sender: Any?) { dismiss() }
}
