import AppKit
import PaxUI

/// One readout for the limits page. Row details and help icons share its lifetime and styling.
@MainActor final class AILimitReadout {
  private let panel = NSPopover()
  private weak var owner: NSView?
  private var monitor: Any?
  private weak var scrollingWindow: NSWindow?

  init() {
    panel.behavior = .applicationDefined
    panel.animates = false
    monitor = NSEvent.addLocalMonitorForEvents(
      matching: [.scrollWheel, .mouseMoved, .leftMouseDown, .rightMouseDown, .keyDown]
    ) { [weak self] event in
      self?.receive(event)
      return event
    }
  }
  isolated deinit {
    if let monitor { NSEvent.removeMonitor(monitor) }
  }
  func close(from host: NSView? = nil) {
    if let host, owner !== host { return }
    panel.close()
    owner = nil
  }
  func leave(_ host: NSView) { close(from: host) }
  func isShowing(from host: NSView) -> Bool { panel.isShown && owner === host }
  func show(_ content: NSView, from host: NSView) {
    guard let window = host.window, window !== scrollingWindow else { return }
    close()
    owner = host
    let controller = NSViewController()
    controller.view = AIUsageLayout.card(content)
    controller.view.layoutSubtreeIfNeeded()
    panel.contentViewController = controller
    panel.contentSize = controller.view.fittingSize
    panel.show(relativeTo: host.bounds, of: host, preferredEdge: .minY)
  }

  private func receive(_ event: NSEvent) {
    switch event.type {
    case .scrollWheel:
      scrollingWindow = event.window
      close()
    case .mouseMoved:
      scrollingWindow = nil
    case .keyDown:
      close()
    case .leftMouseDown, .rightMouseDown:
      scrollingWindow = nil
      if let owner, event.window === owner.window,
        owner.bounds.contains(owner.convert(event.locationInWindow, from: nil))
      {
        return
      }
      close()
    default: break
    }
  }

  static func explanation(_ title: String, _ text: String) -> NSView {
    let font = PaxTheme.Typography.caption()
    let detail = PaxText.label(text, font: font, secondary: true, wrapping: true)
    let paragraph = NSMutableParagraphStyle()
    paragraph.lineSpacing = (font.ascender - font.descender + font.leading) * 0.3
    detail.attributedStringValue = NSAttributedString(
      string: text,
      attributes: [
        .font: font, .foregroundColor: PaxTheme.Color.textSecondary, .paragraphStyle: paragraph,
      ])
    let column = AIUsageLayout.stack([AIUsageLayout.heading(title), detail])
    column.spacing = PaxTheme.Spacing.medium * 0.5
    column.widthAnchor.constraint(equalToConstant: PaxTheme.AISession.selectorWidth).isActive = true
    return column
  }

  func help(_ title: String, _ text: String) -> NSView {
    let image = NSImageView()
    image.image = PaxIcon.image("questionmark.circle", size: PaxTheme.Routes.metaFont)
    image.contentTintColor = PaxTheme.Color.textSecondary
    image.widthAnchor.constraint(equalToConstant: PaxTheme.Spacing.regular).isActive = true
    image.heightAnchor.constraint(equalTo: image.widthAnchor).isActive = true
    let host = AILimitReadoutHost(image, readout: self) {
      Self.explanation(title, text)
    }
    host.setAccessibilityElement(true)
    host.setAccessibilityRole(.button)
    host.setAccessibilityLabel(title + "说明")
    host.setAccessibilityHelp(text)
    return host
  }
}

/// Hover opens a styled readout; scroll/detach closes it. Mouse movement resumes after scrolling.
@MainActor final class AILimitReadoutHost: NSView {
  private let readout: AILimitReadout
  private let content: () -> NSView?
  init(
    _ child: NSView, readout: AILimitReadout,
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
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func mouseEntered(with event: NSEvent) {
    if let content = content() { readout.show(content, from: self) }
  }
  override func mouseMoved(with event: NSEvent) {
    // The coordinator keeps one panel; don't rebuild it for every pixel of movement.
    if !readout.isShowing(from: self), let content = content() {
      readout.show(content, from: self)
    }
  }
  override func mouseExited(with event: NSEvent) { readout.leave(self) }
  override func viewDidMoveToWindow() {
    super.viewDidMoveToWindow()
    if window == nil { readout.close(from: self) }
  }
  override func accessibilityPerformPress() -> Bool {
    guard let content = content() else { return false }
    readout.show(content, from: self)
    return true
  }
}
