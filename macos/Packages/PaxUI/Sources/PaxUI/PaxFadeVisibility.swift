import AppKit

/// 出现/消失的透明度淡变（motion.md §3）。只经 `PaxMotion.plan` / `run`。
/// 状态先于动画：显示时 `isHidden = false` 与 AX 立即为终值；消失时 AX 立即隐藏、
/// `isHiding` 期间调用方应让视图不可交互，`isHidden = true` 在收尾时落定。
/// 淡出期间再次显示会作废待执行的收尾。无窗口的视图没有可见过程，直接落终值。
@MainActor
public final class PaxFadeVisibility {
  private weak var view: NSView?
  private var generation = 0
  /// 正在淡出：视图仍可见但已不可交互。
  public private(set) var isHiding = false

  public init(_ view: NSView) { self.view = view }

  public func set(_ visible: Bool) {
    guard let view else { return }
    if visible { show(view) } else { hide(view) }
  }

  private func show(_ view: NSView) {
    guard isHiding || view.isHidden else { return }
    generation += 1
    let wasHidden = view.isHidden
    isHiding = false
    view.setAccessibilityHidden(false)
    let step = view.window == nil ? PaxMotion.Step.instant : PaxMotion.plan(.appear).primary
    if wasHidden {
      view.alphaValue = 0
      view.isHidden = false
    }
    PaxMotion.run(step) { view.animator().alphaValue = 1 }
    if step.duration == 0 { view.alphaValue = 1 }
  }

  private func hide(_ view: NSView) {
    guard !view.isHidden, !isHiding else { return }
    generation += 1
    let token = generation
    isHiding = true
    view.setAccessibilityHidden(true)
    let step = view.window == nil ? PaxMotion.Step.instant : PaxMotion.plan(.disappear).primary
    PaxMotion.run(
      step, { view.animator().alphaValue = 0 },
      completion: { [weak self, weak view] in
        guard let self, let view, token == self.generation else { return }
        self.isHiding = false
        view.isHidden = true
        view.alphaValue = 1
      })
  }
}
