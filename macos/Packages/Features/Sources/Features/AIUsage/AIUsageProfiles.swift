import AppKit
import PaxCore
import PaxUI

extension AIUsageViewController {
  func renderProfiles() {
    let tools = AIUsageLayout.stack(
      [
        actions.button("添加 Profile", primary: true, enabled: store.configuration.available) {
          [weak self] in
          guard let self else { return }
          self.presentWorkbenchModal(AIProfileEditor(store: self.store))
        }
      ], vertical: false)
    let count = AIUsageLayout.label("", secondary: true)
    let header = AIUsageLayout.stack(
      [
        count,
        NSView(), tools,
      ], vertical: false)
    let update: () -> Void = { [weak self, weak count] in
      guard let self else { return }
      count?.stringValue =
        "\(self.store.profiles.count) 个配置 · 可应用到 \(self.store.configurableClients.count) 个 CLI"
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
    let edit = NSMenuItem(title: "编辑", action: #selector(editProfileMenu(_:)), keyEquivalent: "")
    edit.target = self
    edit.representedObject = p.id
    menu.addItem(edit)
    let reprice = NSMenuItem(
      title: "重算历史费用…", action: #selector(repriceProfileMenu(_:)), keyEquivalent: "")
    reprice.target = self
    reprice.representedObject = p.id
    menu.addItem(reprice)
    let folder = NSMenuItem(
      title: "打开 Profile 文件夹", action: #selector(openProfileFolder(_:)), keyEquivalent: "")
    folder.target = self
    folder.representedObject = p.id
    folder.isEnabled = !store.preview
    menu.addItem(folder)
    let remove = NSMenuItem(
      title: "删除 Profile…", action: #selector(deleteProfileMenu(_:)), keyEquivalent: "")
    remove.target = self
    remove.representedObject = p.id
    if store.isBound(p.id) {
      remove.isEnabled = false
      remove.toolTip = "先切换使用此 Profile 的 CLI"
      remove.subtitle = "先切换使用此 Profile 的 CLI"
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
    let alert = NSAlert()
    alert.messageText = "删除 \(p.name)？"
    alert.informativeText = "保留历史用量；正在绑定的 Profile 需要先切换 CLI。"
    alert.addButton(withTitle: "取消")
    alert.addButton(withTitle: "删除")
    alert.beginSheetModal(for: window) { [weak self] response in
      guard response == .alertSecondButtonReturn else { return }
      self?.store.perform(.remove(p.id))
      if let text = self?.store.error {
        let error = NSAlert()
        error.messageText = text
        error.beginSheetModal(for: window)
      }
    }
  }
}
