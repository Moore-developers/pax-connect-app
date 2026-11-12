import AppKit
import PaxUI

/// 工作台 sheet 复用相同的标题、说明、分组与取消规则；仅由当前页持有。
@MainActor
final class WorkbenchSheet: NSViewController {
  private let content = NSStackView()
  private let heading: String
  private let explanation: String
  init(title: String, explanation: String) {
    self.heading = title
    self.explanation = explanation
    super.init(nibName: nil, bundle: nil)
    preferredContentSize = PaxTheme.Layout.sheet
  }
  @available(*, unavailable)
  required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    view = WorkbenchSurface()
    view.frame = NSRect(origin: .zero, size: PaxTheme.Layout.sheet)
    content.orientation = .vertical
    content.alignment = .leading
    content.spacing = PaxTheme.Spacing.regular
    let title = PaxText.label(heading, font: PaxTheme.Typography.routeTitle, wrapping: true)
    let detail = PaxText.label(explanation, secondary: true, wrapping: true)
    content.addArrangedSubview(title)
    content.addArrangedSubview(detail)
    let scroll = NSScrollView()
    scroll.drawsBackground = false
    scroll.hasVerticalScroller = true
    let document = WorkbenchDocument()
    document.translatesAutoresizingMaskIntoConstraints = false
    scroll.documentView = document
    content.translatesAutoresizingMaskIntoConstraints = false
    document.addSubview(content)
    scroll.translatesAutoresizingMaskIntoConstraints = false
    view.addSubview(scroll)
    let done = PaxButton(.secondary, title: "完成", target: self, action: #selector(close))
    done.keyEquivalent = "\u{1b}"
    done.translatesAutoresizingMaskIntoConstraints = false
    view.addSubview(done)
    NSLayoutConstraint.activate([
      scroll.leadingAnchor.constraint(equalTo: view.leadingAnchor),
      scroll.trailingAnchor.constraint(equalTo: view.trailingAnchor),
      scroll.topAnchor.constraint(equalTo: view.topAnchor),
      scroll.bottomAnchor.constraint(equalTo: done.topAnchor, constant: -PaxTheme.Spacing.regular),
      document.widthAnchor.constraint(equalTo: scroll.contentView.widthAnchor),
      content.leadingAnchor.constraint(
        equalTo: document.leadingAnchor, constant: PaxTheme.Spacing.page),
      content.trailingAnchor.constraint(
        equalTo: document.trailingAnchor, constant: -PaxTheme.Spacing.page),
      content.topAnchor.constraint(equalTo: document.topAnchor, constant: PaxTheme.Spacing.page),
      content.bottomAnchor.constraint(
        equalTo: document.bottomAnchor, constant: -PaxTheme.Spacing.regular),
      done.trailingAnchor.constraint(
        equalTo: view.trailingAnchor, constant: -PaxTheme.Spacing.page),
      done.bottomAnchor.constraint(equalTo: view.bottomAnchor, constant: -PaxTheme.Spacing.card),
    ])
    for label in [title, detail] {
      label.widthAnchor.constraint(equalTo: content.widthAnchor).isActive = true
    }
  }
  func addInformation(_ title: String, _ value: String) {
    let labels = NSStackView(views: [
      PaxText.label(title, font: PaxTheme.Typography.caption(), secondary: true),
      PaxText.label(value, wrapping: true),
    ])
    labels.orientation = .vertical
    labels.alignment = .leading
    labels.spacing = PaxTheme.Spacing.xs
    for label in labels.arrangedSubviews {
      label.widthAnchor.constraint(equalTo: labels.widthAnchor).isActive = true
    }
    addView(labels)
  }
  func addView(_ view: NSView, height: CGFloat? = nil) {
    loadViewIfNeeded()
    content.addArrangedSubview(view)
    view.widthAnchor.constraint(equalTo: content.widthAnchor).isActive = true
    if let height { view.heightAnchor.constraint(equalToConstant: height).isActive = true }
  }
  private var actions: [() -> Void] = []
  func addAction(_ title: String, action: @escaping () -> Void) {
    loadViewIfNeeded()
    let button = PaxButton(
      .secondary, title: title, target: self, action: #selector(performAction(_:)))
    button.tag = actions.count
    actions.append(action)
    button.alignment = .left
    addView(button)
  }
  @objc private func performAction(_ sender: NSButton) {
    guard actions.indices.contains(sender.tag) else { return }
    actions[sender.tag]()
  }
  @objc private func close() { dismissSheet() }
  func dismissSheet() { presentingViewController?.dismiss(self) }
}
