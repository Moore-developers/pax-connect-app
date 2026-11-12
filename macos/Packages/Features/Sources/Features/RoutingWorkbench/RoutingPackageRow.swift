import AppKit
import PaxUI

/// 包项保留操作区宽度，悬停时才显示排序按钮，避免文字随鼠标移动跳动。
@MainActor
final class RoutingPackageRow: NSStackView {
  var hoverActions: NSView? {
    didSet {
      detachesHiddenViews = false
      hoverActions?.isHidden = true
      hoverFade = hoverActions.map { PaxFadeVisibility($0) }
    }
  }
  private var hoverFade: PaxFadeVisibility?
  /// 淡出期间的操作区不可点，避免半透明残留仍可触发。
  override func hitTest(_ point: NSPoint) -> NSView? {
    let hit = super.hitTest(point)
    if hoverFade?.isHiding == true, let actions = hoverActions, let hit,
      hit.isDescendant(of: actions)
    {
      return self
    }
    return hit
  }
  override func updateTrackingAreas() {
    super.updateTrackingAreas()
    for area in trackingAreas { removeTrackingArea(area) }
    addTrackingArea(
      NSTrackingArea(
        rect: .zero, options: [.mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect],
        owner: self))
    updateHoverActions()
  }
  override func layout() {
    super.layout()
    updateHoverActions()
  }
  override func mouseEntered(with event: NSEvent) { setHoverActions(hidden: false) }
  override func mouseExited(with event: NSEvent) { setHoverActions(hidden: true) }
  /// 命中范围比行本身宽，鼠标停在右侧操作区里也不能让按钮闪掉。
  private func updateHoverActions() {
    guard let window else { return }
    let hitRect = bounds.insetBy(dx: -PaxTheme.Routing.sidebarInset, dy: 0)
    let inside = hitRect.contains(convert(window.mouseLocationOutsideOfEventStream, from: nil))
    setHoverActions(hidden: !inside)
  }
  private func setHoverActions(hidden: Bool) {
    hoverFade?.set(!hidden)
  }
}
