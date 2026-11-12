import AppKit
import PaxUI
import QuartzCore

extension NSViewController {
  /// 应用内遮罩不会增加系统标题栏；嵌套候选浮层仍由原生控制器拥有。
  func presentRouteModal(_ controller: RouteModalController) {
    presentWorkbenchModal(controller)
  }
  func presentWorkbenchModal(_ controller: WorkbenchModalController) {
    present(controller, animator: WorkbenchPresentation())
  }
}
extension NSPopover {
  /// 弹出层动画跟随系统「减少动态效果」（motion.md §3）；图表读数浮层不走这里，保持即时。
  func applyMotionPreference() { animates = !PaxMotion.reducesMotion }
}
@MainActor
final class WorkbenchPresentation: NSObject, NSViewControllerPresentationAnimator {
  private var overlay: WorkbenchOverlay?
  private var closing = false
  /// 收尾（移除遮罩、恢复无障碍与焦点）实际执行的次数；幂等，正常恒为 0 或 1。
  private(set) var cleanupCount = 0
  var isOverlayPresent: Bool { overlay != nil }
  private var hiddenSiblings: [(NSView, Bool)] = []
  private var oldChildren: [Any]?
  private var oldModal = false
  private var oldResponder: NSResponder?
  func animatePresentation(of presented: NSViewController, from presenting: NSViewController) {
    guard let host = presenting.view.window?.contentView else {
      return
    }
    oldChildren = host.window?.accessibilityChildren()
    oldModal = host.window?.isAccessibilityModal() ?? false
    oldResponder = host.window?.firstResponder
    let overlay = WorkbenchOverlay()
    self.overlay = overlay
    hiddenSiblings = host.subviews.map { ($0, $0.isAccessibilityHidden()) }
    for (sibling, _) in hiddenSiblings { sibling.setAccessibilityHidden(true) }
    let parent = host
    overlay.frame = host.bounds
    overlay.translatesAutoresizingMaskIntoConstraints = false
    parent.addSubview(overlay)
    NSLayoutConstraint.activate([
      overlay.leadingAnchor.constraint(equalTo: host.leadingAnchor),
      overlay.trailingAnchor.constraint(equalTo: host.trailingAnchor),
      overlay.topAnchor.constraint(equalTo: host.topAnchor),
      overlay.bottomAnchor.constraint(equalTo: host.bottomAnchor),
    ])
    presented.view.translatesAutoresizingMaskIntoConstraints = true
    presented.view.autoresizingMask = []
    overlay.addSubview(presented.view)
    presented.view.setAccessibilityElement(true)
    presented.view.setAccessibilityRole(.group)
    presented.view.setAccessibilitySubrole(.dialog)
    presented.view.setAccessibilityModal(true)
    overlay.controller = presented
    host.window?.setAccessibilityChildren([presented.view])
    host.window?.setAccessibilityModal(true)
    let size = presented.preferredContentSize
    overlay.preferredSize = size
    (presented.view as? PaxDialogView)?.onContentResize = { [weak overlay] in
      Task { @MainActor in overlay?.resizePanel() }
    }
    overlay.keyMonitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) {
      [weak overlay, weak parent] event in
      guard let overlay, let parent, parent.subviews.last === overlay,
        event.window === overlay.window, overlay.window?.isKeyWindow == true
      else { return event }
      if event.keyCode == 53 {
        if PaxTimeSeriesChart.consumeEscape(in: overlay.window?.firstResponder) { return nil }
        overlay.controller?.cancelOperation(overlay)
        return nil
      }
      guard event.keyCode == 48 else { return event }
      let controls = overlay.focusable()
      guard !controls.isEmpty else { return nil }
      let responder = overlay.window?.firstResponder
      let selected = controls.firstIndex { control in
        if responder === control { return true }
        if let field = control as? NSTextField, let editor = field.currentEditor(),
          responder === editor
        {
          return true
        }
        return false
      }
      let backwards = event.modifierFlags.contains(.shift)
      let next =
        selected.map { ($0 + (backwards ? controls.count - 1 : 1)) % controls.count }
        ?? (backwards ? controls.count - 1 : 0)
      overlay.window?.makeFirstResponder(controls[next])
      return nil
    }
    parent.layoutSubtreeIfNeeded()
    overlay.resizePanel()
    overlay.window?.makeFirstResponder(overlay)
    runOpenAnimation(overlay: overlay, dialog: presented.view)
  }
  /// 状态先于动画：焦点、AX 已是终值，这里只动 alpha 与对话框缩放（layer 支撑，绕中心）。
  private func runOpenAnimation(overlay: WorkbenchOverlay, dialog: NSView) {
    let step = PaxMotion.plan(.presentationOpen).primary
    guard step.duration > 0 else { return }
    dialog.wantsLayer = true
    overlay.alphaValue = 0
    if step.kind == .fadeScale, let layer = dialog.layer {
      layer.transform = Self.scaleTransform(0.98, layer: layer)
    }
    PaxMotion.run(
      step,
      {
        overlay.animator().alphaValue = 1
        if step.kind == .fadeScale { dialog.layer?.transform = CATransform3DIdentity }
      },
      completion: { [weak self, weak overlay, weak dialog] in
        // 被关闭接管时由收尾负责，不再改动。
        guard let self, !self.closing, let overlay, self.overlay === overlay else { return }
        overlay.alphaValue = 1
        dialog?.layer?.transform = CATransform3DIdentity
      })
  }
  private static func scaleTransform(_ scale: CGFloat, layer: CALayer) -> CATransform3D {
    let cx = (0.5 - layer.anchorPoint.x) * layer.bounds.width
    let cy = (0.5 - layer.anchorPoint.y) * layer.bounds.height
    var t = CATransform3DMakeTranslation(cx, cy, 0)
    t = CATransform3DScale(t, scale, scale, 1)
    return CATransform3DTranslate(t, -cx, -cy, 0)
  }
  func animateDismissal(of dismissed: NSViewController, from presenting: NSViewController) {
    guard let overlay, !closing else { return }
    closing = true
    // 关闭期间不再响应键盘与点击。
    if let monitor = overlay.keyMonitor {
      NSEvent.removeMonitor(monitor)
      overlay.keyMonitor = nil
    }
    overlay.isClosing = true
    // 焦点移到遮罩，由它吞掉按键；焦点在 finishDismissal 中恢复。
    overlay.window?.makeFirstResponder(overlay)
    let step = PaxMotion.plan(.presentationClose).primary
    PaxMotion.run(
      step, { overlay.animator().alphaValue = 0 },
      // 强引用：AppKit 不持有 animator，收尾完成前必须由回调保活。
      completion: { self.finishDismissal(of: dismissed) })
  }
  private func finishDismissal(of dismissed: NSViewController) {
    guard let overlay else { return }
    cleanupCount += 1
    let window = overlay.window
    dismissed.view.layer?.transform = CATransform3DIdentity
    dismissed.view.removeFromSuperview()
    overlay.removeFromSuperview()
    self.overlay = nil
    window?.setAccessibilityChildren(oldChildren)
    window?.setAccessibilityModal(oldModal)
    oldChildren = nil
    for (sibling, wasHidden) in hiddenSiblings { sibling.setAccessibilityHidden(wasHidden) }
    hiddenSiblings = []
    window?.makeFirstResponder(oldResponder)
  }
}
@MainActor
private final class WorkbenchOverlay: NSView {
  weak var controller: NSViewController?
  /// 关闭动画期间整层吞掉点击，避免命中正在淡出的对话框。
  var isClosing = false
  override func hitTest(_ point: NSPoint) -> NSView? {
    isClosing ? (frame.contains(point) ? self : nil) : super.hitTest(point)
  }
  var keyMonitor: Any?
  var preferredSize = NSSize.zero
  private var sizing = false
  override func setFrameSize(_ newSize: NSSize) {
    super.setFrameSize(newSize)
    resizePanel()
  }
  func resizePanel() {
    guard !sizing, let controller, bounds.width > 0, bounds.height > 0 else { return }
    sizing = true
    defer { sizing = false }
    let c = PaxTheme.Routes.self
    let available = NSSize(
      width: max(0, bounds.width - c.overlayInset * 2),
      height: max(0, bounds.height - c.overlayInset * 2))
    let pane = controller.view as? PaxDialogView
    let maximum =
      (controller as? WorkbenchModalController)?.maximumPresentationWidth
      ?? (pane?.style == .detail ? c.detailMaximumWidth : c.auxiliaryMaximumWidth)
    let width = min(
      available.width,
      min(maximum, max(preferredSize.width, bounds.width * c.dialogWindowFraction)))
    // 自定义呈现由容器管理 frame，避免 preferredContentSize 约束抬高宿主窗口最小尺寸。
    controller.view.setFrameSize(
      NSSize(width: width, height: min(available.height, preferredSize.height)))
    pane?.layoutSubtreeIfNeeded()
    let height = min(available.height, pane?.naturalHeight ?? preferredSize.height)
    let trailing = (controller as? WorkbenchModalController)?.alignsToTrailingEdge == true
    controller.view.frame = NSRect(
      x: trailing ? bounds.width - width - c.overlayInset : (bounds.width - width) / 2,
      y: trailing ? bounds.height - height - c.overlayInset : (bounds.height - height) / 2,
      width: width, height: height)
  }
  func focusable() -> [NSView] {
    func walk(_ view: NSView) -> [NSView] {
      guard !view.isHidden else { return [] }
      if let control = view as? NSControl, control.isEnabled && control.acceptsFirstResponder {
        return [control]
      }
      if let editor = view as? NSTextView, editor.isEditable { return [editor] }
      if view is PaxTrendView || view is PaxRouteTrafficChart { return [view] }
      return view.subviews.flatMap(walk)
    }
    return subviews.flatMap(walk)
  }
  override var acceptsFirstResponder: Bool { true }
  override func cancelOperation(_ sender: Any?) {
    guard !isClosing else { return }
    controller?.cancelOperation(sender)
  }
  /// 关闭中对话框子树不得响应 Return 等快捷键。
  override func performKeyEquivalent(with event: NSEvent) -> Bool {
    isClosing || super.performKeyEquivalent(with: event)
  }
  override func keyDown(with event: NSEvent) {
    guard !isClosing else { return }
    if event.keyCode == 53 { controller?.cancelOperation(self) } else { super.keyDown(with: event) }
  }
  override init(frame: NSRect) {
    super.init(frame: frame)
    wantsLayer = true
  }
  convenience init() { self.init(frame: .zero) }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override var isFlipped: Bool { true }
  override func draw(_ dirtyRect: NSRect) {
    PaxTheme.Routes.backdrop.setFill()
    bounds.fill()
  }
  override func mouseDown(with event: NSEvent) {}
  override func rightMouseDown(with event: NSEvent) {}
}
