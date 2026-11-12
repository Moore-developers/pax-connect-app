import AppKit

/// Horizontal group of radio options: exactly one choice, reported by index.
/// Keeps the selection itself so it never depends on AppKit's implicit radio grouping.
public final class PaxRadioGroup: NSStackView {
  public var onSelect: ((Int) -> Void)?
  public var selectedIndex = 0 { didSet { showSelection() } }
  public var isEnabled = true {
    didSet { for option in options { option.isEnabled = isEnabled } }
  }
  private var options: [PaxRadioOption] = []

  public init(_ titles: [String]) {
    super.init(frame: .zero)
    orientation = .horizontal
    alignment = .centerY
    spacing = PaxTheme.Spacing.roomy
    for (index, title) in titles.enumerated() {
      let option = PaxRadioOption(title, target: self, action: #selector(choose(_:)))
      option.tag = index
      options.append(option)
      addArrangedSubview(option)
    }
    showSelection()  // observers do not run for assignments inside the initializer
    setAccessibilityRole(.radioGroup)
    setAccessibilityLabel(titles.joined(separator: " / "))
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }

  private func showSelection() {
    for (index, option) in options.enumerated() {
      option.state = index == selectedIndex ? .on : .off
    }
  }

  @objc private func choose(_ sender: NSButton) {
    selectedIndex = sender.tag
    onSelect?(sender.tag)
  }
}

/// One option: a raised 16pt dial that fills with the accent and shows a lit centre when chosen.
final class PaxRadioOption: NSButton {
  private static let dial: CGFloat = 16
  private static let gap: CGFloat = 7
  private static let inset: CGFloat = 3

  init(_ title: String, target: AnyObject?, action: Selector?) {
    super.init(frame: .zero)
    self.title = title
    self.target = target
    self.action = action
    isBordered = false
    setButtonType(.radio)
    focusRingType = .none
    setAccessibilityRole(.radioButton)
    setAccessibilityLabel(title)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  private var labelFont: NSFont {
    PaxTheme.Typography.label(
      size: PaxTheme.Routes.bodyFont, weight: state == .on ? .semibold : .regular)
  }
  override var intrinsicContentSize: NSSize {
    let text = (title as NSString).size(withAttributes: [.font: labelFont]).width
    return NSSize(
      width: 2 * Self.inset + Self.dial + Self.gap + text,
      height: Self.dial + 2 * Self.inset + 8)
  }
  override var state: NSControl.StateValue { didSet { needsDisplay = true } }
  override var isEnabled: Bool { didSet { needsDisplay = true } }

  override func draw(_ dirtyRect: NSRect) {
    let fade: CGFloat = isEnabled ? 1 : 0.45
    let on = state == .on
    let dial = NSRect(
      x: Self.inset, y: bounds.midY - Self.dial / 2, width: Self.dial, height: Self.dial)
    let down: CGFloat = isFlipped ? 1 : -1
    let accent = PaxTheme.Routes.blue
    NSGraphicsContext.saveGraphicsState()
    let shadow = NSShadow()
    shadow.shadowColor = (on ? accent : NSColor.black).withAlphaComponent((on ? 0.2 : 0.08) * fade)
    shadow.shadowBlurRadius = on ? 2.5 : 1.5
    shadow.shadowOffset = NSSize(width: 0, height: 0.75 * down)
    shadow.set()
    (on ? accent : NSColor(white: 0.96, alpha: 1)).withAlphaComponent(fade).setFill()
    NSBezierPath(ovalIn: dial).fill()
    NSGraphicsContext.restoreGraphicsState()
    NSGraphicsContext.saveGraphicsState()
    let face = NSBezierPath(ovalIn: dial)
    face.addClip()
    NSGradient(
      starting: NSColor.white.withAlphaComponent((on ? 0.3 : 0.9) * fade),
      ending: NSColor.white.withAlphaComponent(0))?.draw(in: dial, angle: isFlipped ? 90 : -90)
    NSGraphicsContext.restoreGraphicsState()
    let edge =
      on
      ? accent.blended(withFraction: 0.15, of: .black) ?? accent
      : NSColor.black.withAlphaComponent(0.14)
    edge.withAlphaComponent(fade).setStroke()
    face.lineWidth = 1
    face.stroke()
    if on {
      let centre = dial.insetBy(dx: 5, dy: 5)
      NSGraphicsContext.saveGraphicsState()
      let glow = NSShadow()
      glow.shadowColor = NSColor.black.withAlphaComponent(0.2 * fade)
      glow.shadowBlurRadius = 1
      glow.shadowOffset = NSSize(width: 0, height: 0.5 * down)
      glow.set()
      NSColor.white.withAlphaComponent(fade).setFill()
      NSBezierPath(ovalIn: centre).fill()
      NSGraphicsContext.restoreGraphicsState()
    }
    let attrs: [NSAttributedString.Key: Any] = [
      .font: labelFont,
      .foregroundColor: (isEnabled ? PaxTheme.Color.text : PaxTheme.Color.textDisabled),
    ]
    let size = (title as NSString).size(withAttributes: attrs)
    (title as NSString).draw(
      at: NSPoint(x: dial.maxX + Self.gap, y: bounds.midY - size.height / 2), withAttributes: attrs)
  }
}
