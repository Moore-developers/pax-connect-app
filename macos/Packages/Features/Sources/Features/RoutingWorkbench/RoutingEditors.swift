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
    pane.status.stringValue =
      (error as? RoutingWorkbenchStore.EditError)?.message(context: interfaceCopy.context)
      ?? self.interfaceCopy.text("interface_detail.5910b8db90")
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
  override var localizedTitleKey: String? {
    draft.id == nil ? "rules_ui.new_package" : "rules_ui.edit_package"
  }
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
        self.interfaceCopy.text("interface_detail.801a1a5ba1"),
        items: [
          self.interfaceCopy.text("interface_detail.320e0f7238"),
          self.interfaceCopy.text("interface_detail.e16d41c1ea"),
        ], selected: draft.kind == .local ? 0 : 1,
        action: #selector(kindChanged))
    }
    let (nameView, input) = field(
      self.interfaceCopy.text("profile_editor.name"), value: draft.name,
      identifier: "routing-package-name")
    name = input
    pane.add(nameView)
    if draft.kind == .subscription {
      let (sourceView, input) = field(
        self.interfaceCopy.text("interface_detail.c0e426e719"), value: draft.source,
        identifier: "routing-source")
      source = input
      pane.add(sourceView)
    } else {
      source = nil
    }
    destination = picker(
      self.interfaceCopy.text("interface_detail.bb8741da97"),
      items: store.destinationOptions(current: draft.destination).map {
        store.destinationName($0, context: interfaceCopy.context)
      },
      selected: store.destinationOptions(current: draft.destination).firstIndex(
        of: draft.destination) ?? 0,
      action: #selector(destinationChanged))
    pane.setActions([
      button(self.interfaceCopy.text("common.cancel"), #selector(close)),
      button(self.interfaceCopy.text("action.common.save"), #selector(save), primary: true),
    ])
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
