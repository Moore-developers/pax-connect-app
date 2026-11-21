import AppKit
import PaxCore
import PaxUI

/// Both subscription entry points choose a transport explicitly; no implicit
/// fallback changes the user's requested network path.
@MainActor
final class SubscriptionRefreshMenu: NSObject {
  private let context: PresentationContext
  private let store: RouteWorkbenchStore
  private let groupID: String
  private weak var anchor: NSView?
  private init(store: RouteWorkbenchStore, groupID: String, context: PresentationContext) {
    self.context = context
    self.store = store
    self.groupID = groupID
  }
  static func present(
    store: RouteWorkbenchStore, groupID: String, from anchor: NSView,
    context: PresentationContext = .chinese
  ) {
    guard let source = store.subscription(groupID), store.canRefreshSubscription(groupID) else {
      return
    }
    let handler = SubscriptionRefreshMenu(store: store, groupID: groupID, context: context)
    handler.anchor = anchor
    let menu = NSMenu()
    menu.autoenablesItems = false
    let heading = NSMenuItem(
      title: context.text("route_readouts.5097f5d646") + source.name, action: nil, keyEquivalent: ""
    )
    heading.isEnabled = false
    menu.addItem(heading)
    if source.hasRefreshChannel {
      let item = NSMenuItem(
        title: context.text("route_readouts.f9d4cf74c0"), action: #selector(handler.refreshSaved),
        keyEquivalent: "")
      item.target = handler
      menu.addItem(item)
    }
    for (title, via) in [
      (context.text("route_readouts.2f8a1b1d7e"), false),
      (context.text("route_readouts.83478cf49a"), true),
    ] {
      let item = NSMenuItem(title: title, action: #selector(handler.refresh(_:)), keyEquivalent: "")
      item.target = handler
      item.representedObject = via
      item.isEnabled = !via || store.canRefreshViaRoute
      item.toolTip =
        via
        ? (store.refreshRoute.map {
          context.text("route_readouts.3b0569f696") + $0.name
            + context.text("route_readouts.3eade19257")
        } ?? context.text("route_readouts.993baeda70"))
        : context.text("route_readouts.258a88f233")
      menu.addItem(item)
    }
    if store.canEditSubscriptionSource {
      menu.addItem(.separator())
      let edit = NSMenuItem(
        title: context.text("route_readouts.fcb9cb891d"), action: #selector(handler.editSource),
        keyEquivalent: "")
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
    alert.messageText = context.text("route_readouts.b1fdce5ca2")
    alert.informativeText = context.text("route_readouts.bac337b7a5")
    let address = NSTextField(string: "")
    address.placeholderString = context.text("route_readouts.75927f73b7")
    let agent = NSTextField(string: source.userAgent)
    agent.placeholderString = context.text("route_readouts.16e97f9b2c")
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
    alert.addButton(withTitle: context.text("route_readouts.8c7b5ed663"))
    alert.addButton(withTitle: context.text("route_readouts.2cd0f3be87"))
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
