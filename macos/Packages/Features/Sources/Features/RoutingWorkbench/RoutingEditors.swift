import AppKit
import PaxCore
import PaxUI

@MainActor
class RoutingModalController: WorkbenchModalController {
  override func button(_ title: String, _ action: Selector, primary: Bool = false) -> PaxRouteButton
  {
    let value = super.button(title, action, primary: primary)
    if primary { value.keyEquivalent = "\r" }
    return value
  }
  override func showError(_ error: Error) {
    super.showError(error)
    pane.status.stringValue = (error as? RoutingWorkbenchStore.EditError)?.message ?? "操作失败，请检查输入。"
  }
  func picker(
    _ title: String, items: [String], selected: Int, action: Selector, section: NSStackView? = nil
  ) -> PaxRoutePicker {
    let input = PaxRoutePicker()
    input.addItems(withTitles: items)
    if items.indices.contains(selected) { input.selectItem(at: selected) }
    input.target = self
    input.action = action
    input.setAccessibilityLabel(title)
    let label = PaxText.label(title, font: PaxTheme.Typography.caption(), secondary: true)
    for item in [label, input] as [NSView] {
      if let section {
        section.addArrangedSubview(item)
        item.widthAnchor.constraint(equalTo: section.widthAnchor).isActive = true
      } else {
        pane.add(item)
      }
    }
    return input
  }
}

@MainActor
final class RoutingPackageEditor: RoutingModalController {
  private let store: RoutingWorkbenchStore
  private var draft: RoutingWorkbenchStore.PackageDraft
  private var name: NSTextField?
  private var destination: NSPopUpButton?
  private var kind: NSPopUpButton?
  private var source: NSTextField?
  init(store: RoutingWorkbenchStore, id: String? = nil) {
    self.store = store
    draft = id.flatMap(store.packageDraft) ?? store.newPackageDraft()
    super.init(title: id == nil ? "添加包" : "编辑规则包", symbol: "folder")
  }
  override func loadView() {
    super.loadView()
    render()
  }
  private func capture() {
    if let name { draft.name = name.stringValue }
    if let destination,
      store.destinationOptions(current: draft.destination).indices.contains(
        destination.indexOfSelectedItem)
    {
      draft.destination =
        store.destinationOptions(current: draft.destination)[destination.indexOfSelectedItem]
    }
    if let source { draft.source = source.stringValue }
  }
  private func render() {
    pane.clear()
    if draft.id == nil {
      kind = picker(
        "规则由谁维护", items: ["自己维护", "从链接订阅"], selected: draft.kind == .local ? 0 : 1,
        action: #selector(kindChanged))
    }
    let (nameView, input) = field("名称", value: draft.name, identifier: "routing-package-name")
    name = input
    pane.add(nameView)
    if draft.kind == .subscription {
      let (sourceView, input) = field(
        "兼容规则集 HTTPS 地址", value: draft.source, identifier: "routing-source")
      source = input
      pane.add(sourceView)
    } else {
      source = nil
    }
    destination = picker(
      "包内目标统一走",
      items: store.destinationOptions(current: draft.destination).map(store.destinationName),
      selected: store.destinationOptions(current: draft.destination).firstIndex(
        of: draft.destination) ?? 0,
      action: #selector(destinationChanged))
    pane.setActions([button("取消", #selector(close)), button("保存", #selector(save), primary: true)])
  }
  @objc private func kindChanged() {
    capture()
    draft.kind = kind?.indexOfSelectedItem == 1 ? .subscription : .local
    render()
  }
  @objc private func destinationChanged() { capture() }
  @objc private func save() {
    capture()
    let intent = RoutingWorkbenchStore.EditIntent.package(draft)
    Task { @MainActor [weak self] in
      guard let self else { return }
      do {
        try await store.saveLibrary(intent)
        close()
      } catch { showError(error) }
    }
  }
}
