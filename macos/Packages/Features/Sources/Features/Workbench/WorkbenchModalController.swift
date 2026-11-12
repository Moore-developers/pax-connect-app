import AppKit
import PaxUI

/// 工作台各页共用的原生浮层骨架；业务错误文案由页面覆盖。
@MainActor
class WorkbenchModalController: NSViewController {
  let pane: PaxDialogView
  var maximumPresentationWidth: CGFloat? { nil }
  var alignsToTrailingEdge: Bool { false }
  init(
    title: String, subtitle: String = "", size: NSSize = PaxTheme.Routes.picker,
    style: PaxDialogView.Style = .auxiliary,
    symbol: String = "point.3.connected.trianglepath.dotted", tone: PaxFeatureTag.Tone = .blue
  ) {
    pane = PaxDialogView(title: title, subtitle: subtitle, style: style, symbol: symbol, tone: tone)
    super.init(nibName: nil, bundle: nil)
    preferredContentSize = size
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func loadView() {
    view = pane
    view.frame = NSRect(origin: .zero, size: preferredContentSize)
    let close = PaxRouteButton(
      "关闭", symbol: "xmark", style: pane.style == .event ? .secondary : .quiet, target: self,
      action: #selector(close))
    close.imagePosition = .imageOnly
    close.setAccessibilityLabel("关闭浮层")
    pane.identityActions.addArrangedSubview(close)
  }
  func button(_ title: String, _ action: Selector, primary: Bool = false) -> PaxRouteButton {
    PaxRouteButton(title, style: primary ? .primary : .secondary, target: self, action: action)
  }
  func showError(_ error: Error) {
    pane.status.stringValue = "操作失败，请检查输入"
    pane.status.textColor = PaxTheme.Color.error
    pane.status.isHidden = false
    pane.status.superview?.isHidden = false
  }
  @objc func close() { presentingViewController?.dismiss(self) }
  override func cancelOperation(_ sender: Any?) { close() }
  func field(_ title: String, value: String, identifier: String) -> (NSStackView, NSTextField) {
    let input = PaxRouteField(value)
    input.font = PaxTheme.Typography.label(size: PaxTheme.Routes.bodyFont)
    input.focusRingType = .none
    input.identifier = NSUserInterfaceItemIdentifier(identifier)
    input.setAccessibilityIdentifier(identifier)
    input.setAccessibilityLabel(title)
    let stack = NSStackView(views: [
      PaxRouteText.label(title, font: PaxTheme.Typography.caption(), secondary: true), input,
    ])
    stack.orientation = .vertical
    stack.alignment = .leading
    stack.spacing = PaxTheme.Spacing.tight
    input.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
    return (stack, input)
  }
}
