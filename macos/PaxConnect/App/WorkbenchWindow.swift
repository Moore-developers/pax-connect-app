import AppKit
import PaxUI

/// Owns the workbench frame and its title bar's fill/restore action.
@MainActor
final class WorkbenchWindow: NSWindow {
  weak var extendedTitlebar: NSView?
  private var restoreFrame: NSRect?
  private var screenWidthLimit: NSLayoutConstraint?
  private var fittingScreen = false
  private var enteringFullScreen = false

  private var visibleScreen: NSRect? {
    guard !enteringFullScreen, !styleMask.contains(.fullScreen) else { return nil }
    return (screen ?? NSScreen.main)?.visibleFrame
  }

  override func sendEvent(_ event: NSEvent) {
    if event.type == .leftMouseDown, event.clickCount == 2, attachedSheet == nil,
      isTitlebarBackground(at: event.locationInWindow)
    {
      toggleScreenFill()
      return
    }
    super.sendEvent(event)
  }

  private func isTitlebarBackground(at point: NSPoint) -> Bool {
    guard !styleMask.contains(.fullScreen) else { return false }
    if let extendedTitlebar, extendedTitlebar.window === self,
      extendedTitlebar.bounds.contains(extendedTitlebar.convert(point, from: nil))
    {
      return true
    }
    guard point.y >= contentLayoutRect.maxY else { return false }
    return ![ButtonType.closeButton, .miniaturizeButton, .zoomButton].contains { type in
      guard let button = standardWindowButton(type), !button.isHidden else { return false }
      return button.bounds.contains(button.convert(point, from: nil))
    }
  }

  private func toggleScreenFill() {
    guard let visible = visibleScreen else { return }
    let current = frame
    let fillsScreen =
      abs(current.minX - visible.minX) < 1 && abs(current.maxX - visible.maxX) < 1
      && abs(current.minY - visible.minY) < 1 && abs(current.maxY - visible.maxY) < 1
    if var normal = restoreFrame ?? (fillsScreen ? current : nil) {
      restoreFrame = nil
      let widerThanScreen = normal.width > visible.width + 0.5
      let fillsBothAxes = normal.width >= visible.width - 1 && normal.height >= visible.height - 1
      if widerThanScreen || fillsBothAxes {
        let center = normal.midX
        normal.size.width =
          PaxTheme.Layout.defaultWindow.width < visible.width
          ? PaxTheme.Layout.defaultWindow.width : minimumSize(within: visible).width
        normal.origin.x = center - normal.width / 2
      }
      applyFrame(normal, within: visible, animate: true)
    } else {
      restoreFrame = Self.fitted(current, within: visible)
      applyFrame(visible, within: visible, animate: true)
    }
  }

  func resumeNormalSizing() {
    // AppKit reports live-resize callbacks while animating a programmatic frame change.
    if !fittingScreen { restoreFrame = nil }
  }

  func resize(to contentSize: NSSize) {
    resumeNormalSizing()
    guard let visible = visibleScreen else { return }
    var target = frameRect(forContentRect: NSRect(origin: .zero, size: contentSize))
    target.origin = NSPoint(x: frame.minX, y: frame.maxY - target.height)
    applyFrame(target, within: visible)
  }

  func boundedResize(_ size: NSSize) -> NSSize {
    guard let visible = visibleScreen else { return size }
    let minimum = minimumSize(within: visible)
    return NSSize(
      width: min(max(size.width, minimum.width), visible.width),
      height: min(max(size.height, minimum.height), visible.height))
  }

  override func constrainFrameRect(_ frameRect: NSRect, to screen: NSScreen?) -> NSRect {
    guard !enteringFullScreen, !styleMask.contains(.fullScreen),
      let visible = screen?.visibleFrame
    else {
      return super.constrainFrameRect(frameRect, to: screen)
    }
    return Self.fitted(frameRect, within: visible)
  }

  func prepareForFullScreen() {
    enteringFullScreen = true
    screenWidthLimit?.isActive = false
    screenWidthLimit = nil
  }

  func fullScreenTransitionFinished() {
    enteringFullScreen = false
    scheduleScreenFit()
  }

  func scheduleScreenFit() {
    guard !fittingScreen, isVisible, visibleScreen != nil else { return }
    NSObject.cancelPreviousPerformRequests(
      withTarget: self, selector: #selector(fitAfterDrag), object: nil)
    perform(#selector(fitAfterDrag), with: nil, afterDelay: 0)
  }

  @objc private func fitAfterDrag() {
    guard isVisible, !isMiniaturized, visibleScreen != nil else { return }
    // A drag can cross displays. Let WindowServer finish before moving the window inside one.
    if NSEvent.pressedMouseButtons & 1 != 0 || inLiveResize {
      perform(#selector(fitAfterDrag), with: nil, afterDelay: 0.1)
      return
    }
    fitToScreen()
  }

  func fitToScreen() {
    guard let visible = visibleScreen else { return }
    applyFrame(restoreFrame == nil ? frame : visible, within: visible)
  }

  private func minimumSize(within visible: NSRect) -> NSSize {
    let standard = PaxTheme.Layout.minimumWindow
    // Keep a narrower restore size available even on screens at or below the design minimum.
    let width = standard.width < visible.width ? standard.width : floor(visible.width * 0.9)
    return NSSize(width: width, height: min(standard.height, visible.height))
  }

  private func applyFrame(_ proposed: NSRect, within visible: NSRect, animate: Bool = false) {
    guard !fittingScreen else { return }
    fittingScreen = true
    defer { fittingScreen = false }
    minSize = minimumSize(within: visible)
    // Auto Layout can resize past NSWindow's interactive limits.
    if let contentView, screenWidthLimit?.firstItem !== contentView {
      screenWidthLimit?.isActive = false
      let limit = contentView.widthAnchor.constraint(lessThanOrEqualToConstant: visible.width)
      limit.identifier = "Workbench screen width"
      limit.isActive = true
      screenWidthLimit = limit
    } else {
      screenWidthLimit?.constant = visible.width
    }
    let nextFrame = Self.fitted(proposed, within: visible)
    if frame != nextFrame { setFrame(nextFrame, display: true, animate: animate) }
  }

  private static func fitted(_ frame: NSRect, within visible: NSRect) -> NSRect {
    let width = min(frame.width, visible.width)
    let height = min(frame.height, visible.height)
    return NSRect(
      x: min(max(frame.minX, visible.minX), visible.maxX - width),
      y: min(max(frame.minY, visible.minY), visible.maxY - height),
      width: width, height: height)
  }
}

/// Extends the native title bar's drag region over the workspace's empty top inset.
final class WorkbenchTitlebarDragArea: NSView {
  override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

  override func mouseDown(with event: NSEvent) {
    if event.clickCount == 1, let window, !window.styleMask.contains(.fullScreen) {
      window.performDrag(with: event)
    }
  }
}
