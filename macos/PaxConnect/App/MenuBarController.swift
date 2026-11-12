import AppKit
import Features

/// Owns the system status item for exactly the App lifetime.
@MainActor
final class MenuBarController {
  private let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
  private(set) var content: MenuBarMenuController?
  init() {
    let loading = NSMenu(title: "Pax Connect")
    loading.addItem(withTitle: "后台启动中…", action: nil, keyEquivalent: "").isEnabled = false
    let quit = loading.addItem(
      withTitle: "退出 Pax Connect", action: #selector(NSApplication.terminate(_:)), keyEquivalent: ""
    )
    quit.target = NSApp
    item.menu = loading
    item.button?.sendAction(on: [.leftMouseUp, .rightMouseUp])
    item.button?.setAccessibilityIdentifier("pax-status-item")
    update(MenuBarMenuController.loadingImage, "Pax Connect · 后台启动中")
  }
  func attach(_ content: MenuBarMenuController) {
    self.content = content
    item.menu = content.menu
    content.onStatusChange = { [weak self] image, title in self?.update(image, title) }
  }
  func showMenuPreview(in view: NSView) {
    content?.menu.popUp(
      positioning: nil,
      at: NSPoint(x: view.bounds.midX, y: view.bounds.midY), in: view)
  }
  private func update(_ image: NSImage, _ title: String) {
    item.button?.image = image
    item.button?.toolTip = title
    item.button?.setAccessibilityLabel(title)
  }
}
