import AppKit
import PaxCore
import PaxUI

extension AIUsageViewController {
  func renderProfiles() {
    let addProfile = actions.button(
      "", primary: true, enabled: store.configuration.available, id: "ai-add-profile"
    ) { [weak self] in
      guard let self else { return }
      self.presentWorkbenchModal(AIProfileEditor(store: self.store))
    }
    interfaceCopy.button(addProfile, key: "ai_usage.add_profile")
    let tools = AIUsageLayout.stack([addProfile], vertical: false)
    let count = AIUsageLayout.label("", secondary: true)
    let header = AIUsageLayout.stack(
      [
        count,
        NSView(), tools,
      ], vertical: false)
    let update: () -> Void = { [weak self, weak count] in
      guard let self else { return }
      count?.stringValue =
        self.interfaceCopy.text(
          "interface_detail.e06e69085b",
          [
            "arg0": String(describing: self.store.profiles.count),
            "arg1": String(describing: self.store.configurableClients.count),
          ])
    }
    contentRefreshers.append(update)
    update()
    add(bindingStrip())
    add(header)
    add(recordList())
  }
  func showProfileMenu(_ p: AIProfile) {
    profileMenu(p).popUp(positioning: nil, at: NSEvent.mouseLocation, in: nil)
  }
  func profileMenu(_ p: AIProfile) -> NSMenu {
    let menu = NSMenu()
    menu.autoenablesItems = false
    let edit = NSMenuItem(
      title: self.interfaceCopy.text("ai_usage.edit"), action: #selector(editProfileMenu(_:)),
      keyEquivalent: "")
    edit.target = self
    edit.representedObject = p.id
    menu.addItem(edit)
    let reprice = NSMenuItem(
      title: self.interfaceCopy.text("ai_usage.reprice"), action: #selector(repriceProfileMenu(_:)),
      keyEquivalent: "")
    reprice.target = self
    reprice.representedObject = p.id
    menu.addItem(reprice)
    let folder = NSMenuItem(
      title: self.interfaceCopy.text("ai_usage.open_profile_folder"),
      action: #selector(openProfileFolder(_:)), keyEquivalent: "")
    folder.target = self
    folder.representedObject = p.id
    folder.isEnabled = !store.preview
    menu.addItem(folder)
    let remove = NSMenuItem(
      title: self.interfaceCopy.text("ai_usage.delete_profile"),
      action: #selector(deleteProfileMenu(_:)), keyEquivalent: "")
    remove.target = self
    remove.representedObject = p.id
    if store.isBound(p.id) {
      remove.isEnabled = false
      remove.toolTip = self.interfaceCopy.text("ai_usage.switch_bound_cli")
      remove.subtitle = self.interfaceCopy.text("ai_usage.switch_bound_cli")
    }
    menu.addItem(remove)
    return menu
  }
  @objc private func editProfileMenu(_ sender: NSMenuItem) { openEditor(sender, scrollTo: nil) }
  @objc private func repriceProfileMenu(_ sender: NSMenuItem) {
    openEditor(sender, scrollTo: .reprice)
  }
  private func openEditor(_ sender: NSMenuItem, scrollTo: AIProfileEditor.Section?) {
    guard let id = sender.representedObject as? String,
      let profile = store.profiles.first(where: { $0.id == id })
    else { return }
    presentWorkbenchModal(AIProfileEditor(store: store, profile: profile, scrollTo: scrollTo))
  }
  @objc private func openProfileFolder(_ sender: NSMenuItem) {
    guard let id = sender.representedObject as? String,
      store.profiles.contains(where: { $0.id == id })
    else { return }
    let folder = FileManager.default.homeDirectoryForCurrentUser
      .appendingPathComponent(
        "Library/Application Support/Pax Connect/AIProfiles/profiles", isDirectory: true
      )
      .appendingPathComponent(id, isDirectory: true)
    NSWorkspace.shared.open(folder)
  }
  @objc private func deleteProfileMenu(_ sender: NSMenuItem) {
    guard let id = sender.representedObject as? String,
      let profile = store.profiles.first(where: { $0.id == id })
    else { return }
    guard !store.isBound(profile.id) else { return }
    confirmDelete(profile)
  }
  private func confirmDelete(_ p: AIProfile) {
    guard let window = view.window else { return }
    let context = interfaceCopy.context
    let alert = NSAlert()
    alert.messageText = self.interfaceCopy.text(
      "interface_detail.24cbabea63", ["arg0": String(describing: p.name)])
    alert.informativeText = self.interfaceCopy.text("ai_usage.preserve_profile_history")
    alert.addButton(withTitle: self.interfaceCopy.text("ai_usage.cancel"))
    alert.addButton(withTitle: self.interfaceCopy.text("ai_usage.delete"))
    alert.beginSheetModal(for: window) { [weak self] response in
      guard response == .alertSecondButtonReturn, let self else { return }
      self.store.perform(.remove(p.id))
      if let text = self.store.error {
        let error = NSAlert()
        error.messageText =
          self.store.isBound(p.id)
          ? context.text("profile_error.ai_profile_in_use")
          : self.store.configuration.error(context: context) ?? text
        error.addButton(withTitle: context.text("common.ok"))
        error.beginSheetModal(for: window)
      }
    }
  }
}
