import AppKit
import PaxUI

// Explicit group editing and subscription actions share the existing draft owner.
extension RouteGroupsController {
  func move(_ id: String, to index: Int) {
    guard !saving else { return }
    guard let old = working.firstIndex(where: { $0.id == id }), working.indices.contains(index)
    else { return }
    let group = working.remove(at: old)
    working.insert(group, at: index)
    renderSidebar()
  }
  func groupMenu(_ id: String) -> NSMenu {
    let menu = NSMenu()
    menu.autoenablesItems = false
    menu.delegate = destructiveTitles
    guard let index = working.firstIndex(where: { $0.id == id }) else {
      let remove = NSMenuItem(
        title: self.interfaceCopy.text("action.routes.delete_subscription"),
        action: #selector(deleteSubscription(_:)), keyEquivalent: "")
      remove.target = self
      remove.representedObject = id
      remove.image = PaxIcon.image("trash")
      remove.isEnabled = !saving && store.canDeleteSubscriptions
      destructiveTitles.mark(remove)
      menu.addItem(remove)
      let automatic = NSMenuItem(
        title: self.interfaceCopy.text("action.routes.subscription_group_note"), action: nil,
        keyEquivalent: "")
      automatic.isEnabled = false
      menu.addItem(automatic)
      return menu
    }
    for (title, command, symbol) in [
      (self.interfaceCopy.text("interface_detail.523be37de2"), "rename", "pencil"),
      (self.interfaceCopy.text("interface_detail.f853a70b12"), "up", "arrow.up"),
      (self.interfaceCopy.text("interface_detail.e75e8b4e5c"), "down", "arrow.down"),
      (self.interfaceCopy.text("interface_detail.7605c1d82e"), "delete", "trash"),
    ] {
      let item = NSMenuItem(title: title, action: #selector(groupAction(_:)), keyEquivalent: "")
      item.target = self
      item.representedObject = [id, command]
      item.image = PaxIcon.image(symbol)
      item.preferVisibleImage()
      item.isEnabled =
        (command == "delete" || command == "rename")
        || (command == "up" ? index > 0 : index < working.count - 1)
      if command == "delete" { destructiveTitles.mark(item) }
      menu.addItem(item)
    }
    return menu
  }
  @objc func deleteSelectedSubscription() {
    guard let id = selectedID else { return }
    let item = NSMenuItem()
    item.representedObject = id
    deleteSubscription(item)
  }
  @objc func deleteSubscription(_ item: NSMenuItem) {
    guard !saving, store.canDeleteSubscriptions, let id = item.representedObject as? String,
      let group = displayedGroups.first(where: { $0.id == id && $0.kind == .subscription }),
      let window = view.window
    else { return }
    let memberCount = store.nodes.filter { "subscription:" + $0.sourceID == id }.count
    let alert = NSAlert()
    alert.messageText = self.interfaceCopy.text(
      "interface_detail.298ee9dec4",
      ["arg0": String(describing: group.name), "arg1": String(describing: memberCount)])
    alert.informativeText =
      self.interfaceCopy.text("interface_detail.06b0ddc8ad")
    alert.addButton(withTitle: self.interfaceCopy.text("common.cancel"))
    alert.addButton(withTitle: self.interfaceCopy.text("interface_detail.fa1382e05a"))
    alert.beginSheetModal(for: window) { [weak self] response in
      Task { @MainActor [weak self] in
        guard let self, response == .alertSecondButtonReturn, !self.saving else { return }
        self.saving = true
        self.new.isEnabled = false
        self.pane.status.stringValue = self.interfaceCopy.text("interface_detail.61f93110be")
        self.pane.status.isHidden = false
        do {
          let committedRevision = try await self.store.deleteSubscriptionGroup(
            id, expectedRevision: self.expectedRevision)
          let existing = Set(self.store.nodes.map(\.id))
          for index in self.working.indices {
            self.working[index].members.formIntersection(existing)
          }
          self.expectedRevision = committedRevision
          self.selectedID = self.working.first?.id ?? self.store.subscriptionGroups.first?.id
          self.saving = false
          self.new.isEnabled = true
          self.removed = nil
          self.pane.status.stringValue = self.interfaceCopy.text("interface_detail.57bb2a1117")
          self.render()
        } catch {
          self.saving = false
          self.new.isEnabled = true
          self.showError(error)
        }
      }
    }
  }
  @objc func groupAction(_ item: NSMenuItem) {
    guard !saving else { return }
    guard let args = item.representedObject as? [String], args.count == 2,
      let index = working.firstIndex(where: { $0.id == args[0] })
    else { return }
    if args[1] == "rename" {
      showNameEditor(groupID: args[0])
    } else if args[1] == "delete" {
      removed = (working[index], index)
      working.remove(at: index)
      if selectedID == args[0] {
        selectedID = working.first?.id ?? store.subscriptionGroups.first?.id
      }
      render()
    } else {
      move(args[0], to: index + (args[1] == "up" ? -1 : 1))
    }
  }
  @objc func restoreGroup() {
    guard !saving else { return }
    guard let (group, index) = removed else { return }
    working.insert(group, at: min(index, working.count))
    selectedID = group.id
    removed = nil
    render()
  }
}
