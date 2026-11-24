import AppKit

/// Interactive readouts can change their fitting size when a detail is disclosed.
@MainActor public protocol PaxResizableReadout: AnyObject {
  var onReadoutResize: (() -> Void)? { get set }
}

/// A native popover with the shared card surface and hover dismissal.
@MainActor public final class PaxHoverReadout {
  private let panel = NSPopover()
  private weak var owner: NSView?
  private var monitor: Any?
  private weak var scrollingWindow: NSWindow?

  private let cardStyle: PaxCard.Style
  private let interactive: Bool
  private var keyboardPinned = false
  private var dismissal: Task<Void, Never>?

  public init(interactive: Bool = false, cardStyle: PaxCard.Style = .standard) {
    self.cardStyle = cardStyle
    self.interactive = interactive
    panel.behavior = .applicationDefined
    panel.animates = false
    monitor = NSEvent.addLocalMonitorForEvents(
      matching: [.scrollWheel, .mouseMoved, .leftMouseDown, .rightMouseDown, .keyDown]
    ) { [weak self] event in
      guard let self else { return event }
      return self.receive(event)
    }
  }
  isolated deinit {
    if let monitor { NSEvent.removeMonitor(monitor) }
  }
  public func close(from host: NSView? = nil) {
    if let host, owner !== host { return }
    dismissal?.cancel()
    dismissal = nil
    panel.close()
    keyboardPinned = false
    owner = nil
  }
  func isShowing(from host: NSView) -> Bool { panel.isShown && owner === host }
  func show(_ content: NSView, from host: NSView) {
    guard let window = host.window, window !== scrollingWindow else { return }
    if interactive, panel.isShown, owner !== host {
      if let panelWindow = panel.contentViewController?.view.window,
        panelWindow.frame.contains(NSEvent.mouseLocation)
      {
        return
      }
    }
    close()
    owner = host
    let controller = NSViewController()
    let inset = PaxTheme.Spacing.card
    let card = PaxCard(
      insets: .init(top: inset, left: inset, bottom: inset, right: inset), style: cardStyle)
    card.contentView = content
    card.heightAnchor.constraint(equalTo: content.heightAnchor, constant: inset * 2).isActive = true
    controller.view = card
    controller.view.layoutSubtreeIfNeeded()
    let size = controller.view.fittingSize
    let (anchor, edge) = placement(for: host, in: window, size: size)
    panel.appearance = host.effectiveAppearance
    panel.contentViewController = controller
    panel.contentSize = size
    if let resizable = content as? PaxResizableReadout {
      resizable.onReadoutResize = { [weak self, weak controller, weak content] in
        guard let self, let controller, let content, panel.contentViewController === controller
        else { return }
        let fit = content.fittingSize
        let next = NSSize(width: fit.width + 2 * inset, height: fit.height + 2 * inset)
        if panel.contentSize != next { panel.contentSize = next }
      }
    }
    panel.show(relativeTo: anchor, of: host, preferredEdge: edge)
  }

  /// Place the popover so it stays clear of the pointer and hovered row whenever screen space allows.
  private func placement(for host: NSView, in window: NSWindow, size: NSSize) -> (
    NSRect, NSRectEdge
  ) {
    let visible = (window.screen ?? NSScreen.main)?.visibleFrame ?? .zero
    let hostScreen = window.convertToScreen(host.convert(host.bounds, to: nil))
    let margin = PaxTheme.HoverReadout.arrowMargin
    let gap = PaxTheme.HoverReadout.cursorGap
    if !interactive {
      let fitsBelow = hostScreen.minY - visible.minY >= size.height + margin
      let fitsAbove = visible.maxY - hostScreen.maxY >= size.height + margin
      let belowEdge: NSRectEdge = host.isFlipped ? .maxY : .minY
      let aboveEdge: NSRectEdge = host.isFlipped ? .minY : .maxY
      if fitsBelow { return (host.bounds, belowEdge) }
      if fitsAbove { return (host.bounds, aboveEdge) }
    }
    let neededWidth = size.width + margin
    let rightOfHost = visible.maxX - hostScreen.maxX
    let leftOfHost = hostScreen.minX - visible.minX
    if rightOfHost >= neededWidth { return (host.bounds, .maxX) }
    if leftOfHost >= neededWidth { return (host.bounds, .minX) }

    let mouse = NSEvent.mouseLocation
    guard hostScreen.contains(mouse) else {
      return (host.bounds, rightOfHost >= leftOfHost ? .maxX : .minX)
    }
    let rightOfMouse = visible.maxX - mouse.x
    let leftOfMouse = mouse.x - visible.minX
    let localMouse = host.convert(window.convertPoint(fromScreen: mouse), from: nil)
    let openRight =
      rightOfMouse >= neededWidth + gap
      || (leftOfMouse < neededWidth + gap && rightOfMouse >= leftOfMouse)
    if openRight {
      let x = min(
        max(host.bounds.minX, localMouse.x + gap), max(host.bounds.minX, host.bounds.maxX - 1))
      return (NSRect(x: x, y: host.bounds.minY, width: 1, height: host.bounds.height), .maxX)
    }
    let x = max(host.bounds.minX, min(host.bounds.maxX - 1, localMouse.x - gap))
    return (NSRect(x: x, y: host.bounds.minY, width: 1, height: host.bounds.height), .minX)
  }

  func leave(from host: NSView) {
    guard owner === host else { return }
    if !interactive {
      close(from: host)
      return
    }
    deferClose()
  }
  private func deferClose() {
    guard dismissal == nil, !keyboardPinned else { return }
    dismissal = Task { @MainActor [weak self] in
      do { try await Task.sleep(for: .seconds(PaxTheme.HoverReadout.bridgeDelay)) } catch { return }
      guard let self else { return }
      if self.containsPointer {
        self.dismissal = nil
        return
      }
      self.close()
    }
  }
  private var containsPointer: Bool {
    let point = NSEvent.mouseLocation
    if panel.isShown, let window = panel.contentViewController?.view.window,
      window.frame.contains(point)
    {
      return true
    }
    guard let owner, let window = owner.window else { return false }
    return window.convertToScreen(owner.convert(owner.bounds, to: nil)).contains(point)
  }
  func focus() {
    guard interactive, let view = panel.contentViewController?.view else { return }
    keyboardPinned = true
    view.window?.makeKey()
    view.window?.selectNextKeyView(nil)
  }
  private func receive(_ event: NSEvent) -> NSEvent? {
    let inPanel = panel.isShown && event.window === panel.contentViewController?.view.window
    if interactive && inPanel && event.type != .keyDown { return event }
    switch event.type {
    case .scrollWheel:
      scrollingWindow = event.window
      close()
    case .mouseMoved:
      scrollingWindow = nil
      if interactive && panel.isShown {
        if containsPointer {
          dismissal?.cancel()
          dismissal = nil
        } else {
          deferClose()
        }
      }
    case .keyDown:
      if interactive {
        if event.keyCode == 53 && panel.isShown {
          close()
          return nil
        }
        return event
      }
      close()
    case .leftMouseDown, .rightMouseDown:
      scrollingWindow = nil
      if let owner, event.window === owner.window,
        owner.bounds.contains(owner.convert(event.locationInWindow, from: nil))
      {
        return event
      }
      close()
    default: break
    }
    return event
  }

}

/// Hover opens a styled readout; scroll/detach closes it. Mouse movement resumes after scrolling.
@MainActor public final class PaxHoverReadoutHost: NSView {
  private let readout: PaxHoverReadout
  private let content: () -> NSView?
  public init(
    _ child: NSView, readout: PaxHoverReadout,
    content: @escaping () -> NSView?
  ) {
    self.readout = readout
    self.content = content
    super.init(frame: .zero)
    child.translatesAutoresizingMaskIntoConstraints = false
    addSubview(child)
    NSLayoutConstraint.activate([
      child.leadingAnchor.constraint(equalTo: leadingAnchor),
      child.trailingAnchor.constraint(equalTo: trailingAnchor),
      child.topAnchor.constraint(equalTo: topAnchor),
      child.bottomAnchor.constraint(equalTo: bottomAnchor),
    ])
    addTrackingArea(
      NSTrackingArea(
        rect: .zero,
        options: [.mouseEnteredAndExited, .mouseMoved, .activeInKeyWindow, .inVisibleRect],
        owner: self))
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override func mouseEntered(with event: NSEvent) {
    if let content = content() { readout.show(content, from: self) }
  }
  public override func mouseMoved(with event: NSEvent) {
    // The coordinator keeps one panel; don't rebuild it for every pixel of movement.
    if !readout.isShowing(from: self), let content = content() {
      readout.show(content, from: self)
    }
  }
  public override func mouseExited(with event: NSEvent) { readout.leave(from: self) }
  public override func viewDidMoveToWindow() {
    super.viewDidMoveToWindow()
    if window == nil { readout.close(from: self) }
  }
  public override func accessibilityPerformPress() -> Bool {
    guard let content = content() else { return false }
    readout.show(content, from: self)
    readout.focus()
    return true
  }
}
