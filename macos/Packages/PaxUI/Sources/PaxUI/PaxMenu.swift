import AppKit

extension NSMenuItem {
  /// macOS 27 起 AppKit 按系统配置决定菜单项图标是否显示，SDK 明示默认「通常会隐藏」。
  /// 需要常显的图标在设置 `image` 后显式声明，避免同一菜单在系统版本间外观不一致。
  public func preferVisibleImage() {
    if #available(macOS 27.0, *) { preferredImageVisibility = .visible }
  }
}

/// 破坏性菜单项的红色文字只在未高亮时成立：系统高亮会把图标改成高亮前景色，
/// 却原样使用 `attributedTitle` 的颜色，于是红字压在强调色药丸上，还与白图标自相矛盾。
/// 由菜单代理在高亮期间换成系统的高亮菜单文字色，菜单关闭时还原。
///
/// `NSMenu.delegate` 是弱引用，所以由菜单的持有方（页面控制器）保存本实例。
@MainActor
public final class PaxMenuDestructiveTitles: NSObject, NSMenuDelegate {
  private static let destructive = NSUserInterfaceItemIdentifier("pax.menu.destructive")

  /// 标记破坏性条目：常态使用 `Color.error`，高亮态交给代理。
  public func mark(_ item: NSMenuItem) {
    item.identifier = Self.destructive
    item.attributedTitle = Self.title(item, color: PaxTheme.Color.error)
  }

  public func menu(_ menu: NSMenu, willHighlight item: NSMenuItem?) {
    for candidate in menu.items where candidate.identifier == Self.destructive {
      let color = candidate === item ? NSColor.selectedMenuItemTextColor : PaxTheme.Color.error
      candidate.attributedTitle = Self.title(candidate, color: color)
    }
  }

  private static func title(_ item: NSMenuItem, color: NSColor) -> NSAttributedString {
    NSAttributedString(string: item.title, attributes: [.foregroundColor: color])
  }
}
