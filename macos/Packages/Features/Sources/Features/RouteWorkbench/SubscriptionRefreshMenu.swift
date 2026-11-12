import AppKit
import PaxUI

/// Both subscription entry points choose a transport explicitly; no implicit
/// fallback changes the user's requested network path.
@MainActor
final class SubscriptionRefreshMenu: NSObject {
  private let store: RouteWorkbenchStore
  private let groupID: String
  private weak var anchor: NSView?
  private init(store: RouteWorkbenchStore, groupID: String) {
    self.store = store
    self.groupID = groupID
  }
  static func present(store: RouteWorkbenchStore, groupID: String, from anchor: NSView) {
    guard let source = store.subscription(groupID), store.canRefreshSubscription(groupID) else {
      return
    }
    let handler = SubscriptionRefreshMenu(store: store, groupID: groupID)
    handler.anchor = anchor
    let menu = NSMenu()
    menu.autoenablesItems = false
    let heading = NSMenuItem(title: "更新订阅：" + source.name, action: nil, keyEquivalent: "")
    heading.isEnabled = false
    menu.addItem(heading)
    if source.hasRefreshChannel {
      let item = NSMenuItem(
        title: "沿用上次成功通道更新", action: #selector(handler.refreshSaved), keyEquivalent: "")
      item.target = handler
      menu.addItem(item)
    }
    for (title, via) in [("系统网络更新", false), ("通过所选线路更新", true)] {
      let item = NSMenuItem(title: title, action: #selector(handler.refresh(_:)), keyEquivalent: "")
      item.target = handler
      item.representedObject = via
      item.isEnabled = !via || store.canRefreshViaRoute
      item.toolTip =
        via
        ? (store.refreshRoute.map { "通过「" + $0.name + "」的独立出站，不开启TUN；失败不回退直连" } ?? "请先选择可用线路")
        : "不使用HTTP代理；若其它工具TUN仍接管系统流量，系统网络仍可能经过它"
      menu.addItem(item)
    }
    if store.canEditSubscriptionSource {
      menu.addItem(.separator())
      let edit = NSMenuItem(
        title: "更换地址与请求标识…", action: #selector(handler.editSource), keyEquivalent: "")
      edit.target = handler
      menu.addItem(edit)
    }
    // popUp is synchronous; handler owns targets for the complete menu lifetime.
    menu.popUp(positioning: nil, at: NSPoint(x: 0, y: anchor.bounds.maxY), in: anchor)
    withExtendedLifetime(handler) {}
  }
  @objc private func editSource() {
    guard let window = anchor?.window, let source = store.subscription(groupID) else { return }
    let alert = NSAlert()
    alert.messageText = "更新订阅来源"
    alert.informativeText = "粘贴长期订阅入口；临时链接过期后需要新地址。新内容成功读取后才替换原来源，失败保留原线路。地址留空仅更新请求标识。"
    let address = NSTextField(string: "")
    address.placeholderString = "https://…（留空保留原地址）"
    let agent = NSTextField(string: source.userAgent)
    agent.placeholderString = "User-Agent（留空使用 Pax 标识）"
    let fields = NSStackView(views: [address, agent])
    fields.orientation = .vertical
    fields.spacing = PaxTheme.Spacing.tight
    fields.frame = NSRect(
      x: 0, y: 0, width: PaxTheme.Workbench.identityColumn,
      height: PaxTheme.Control.height * 2 + PaxTheme.Spacing.tight)
    for field in [address, agent] {
      field.widthAnchor.constraint(equalTo: fields.widthAnchor).isActive = true
    }
    alert.accessoryView = fields
    alert.addButton(withTitle: "读取并更新")
    alert.addButton(withTitle: "取消")
    let store = store
    let groupID = groupID
    alert.beginSheetModal(for: window) { response in
      guard response == .alertFirstButtonReturn else { return }
      store.replaceSubscription(
        groupID, address: address.stringValue.trimmingCharacters(in: .whitespacesAndNewlines),
        agent: agent.stringValue)
    }
  }
  @objc private func refreshSaved() { store.refreshSavedSubscription(groupID) }
  @objc private func refresh(_ item: NSMenuItem) {
    store.refreshSubscription(groupID, viaRoute: item.representedObject as? Bool == true)
  }
}
