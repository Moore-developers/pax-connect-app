import AppKit
import PaxCore
import PaxUI

/// One readout for the limits page. Row details and help icons share its lifetime and styling.
@MainActor final class AILimitReadout {
  let interfaceCopy: InterfaceCopyBindings
  var context: PresentationContext { interfaceCopy.context }

  let hover = PaxHoverReadout()

  init(interfaceCopy: InterfaceCopyBindings = InterfaceCopyBindings()) {
    self.interfaceCopy = interfaceCopy
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

  func help(_ title: String, _ text: String) -> NSView { help { _ in (title, text) } }

  func help(_ content: @escaping (PresentationContext) -> (String, String)) -> NSView {
    let image = NSImageView()
    image.image = PaxIcon.image("questionmark.circle", size: PaxTheme.Routes.metaFont)
    image.contentTintColor = PaxTheme.Color.textSecondary
    image.widthAnchor.constraint(equalToConstant: PaxTheme.Spacing.regular).isActive = true
    image.heightAnchor.constraint(equalTo: image.widthAnchor).isActive = true
    let host = PaxHoverReadoutHost(image, readout: hover) {
      let (title, text) = content(self.context)
      return Self.explanation(title, text)
    }
    host.setAccessibilityElement(true)
    host.setAccessibilityRole(.button)
    interfaceCopy.bind(host) { host, context in
      let (title, text) = content(context)
      host.setAccessibilityLabel(title + context.text("session_limits.4262c45dc7"))
      host.setAccessibilityHelp(text)
    }
    return host
  }
}
