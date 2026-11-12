import AppKit

/// Raised, tactile look of `PaxCompactButton` (`capsule = true`): a solid base colour with a soft
/// white gloss from the top, a 1.5pt highlight along the top edge, an outer ring, a drop shadow and
/// an embossed label. The base colour comes from `capsuleTone`, so one recipe serves every theme.
extension PaxCompactButton {
  public enum CapsuleTone {
    case light, graphite, blue, red
  }

  private static let capsuleInset: CGFloat = 3
  private static let capsulePadding: CGFloat = 13
  private static let graphite = NSColor(srgbRed: 0.114, green: 0.122, blue: 0.137, alpha: 1)

  private var capsuleFont: NSFont {
    PaxTheme.Typography.label(size: PaxTheme.Routes.bodyFont, weight: .semibold)
  }
  private var isDarkAppearance: Bool {
    effectiveAppearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
  }
  private var resolvedTone: CapsuleTone {
    capsuleTone ?? (compactStyle == .primary ? .blue : .light)
  }

  private struct Recipe {
    let base: NSColor
    let ring: NSColor
    let label: NSColor
    let gloss: CGFloat
    let labelShadow: NSColor?
  }

  private func recipe() -> Recipe {
    let dark = isDarkAppearance
    switch resolvedTone {
    case .graphite:
      return Recipe(
        base: Self.graphite, ring: NSColor.black.withAlphaComponent(0.4), label: .white,
        gloss: 0.15,
        labelShadow: NSColor.black.withAlphaComponent(0.4))
    case .blue, .red:
      let base = resolvedTone == .blue ? PaxTheme.Routes.blue : PaxTheme.Color.error
      return Recipe(
        base: base, ring: base.blended(withFraction: 0.22, of: .black) ?? base, label: .white,
        gloss: 0.2, labelShadow: NSColor.black.withAlphaComponent(0.25))
    case .light:
      if dark {
        return Recipe(
          base: NSColor(white: 0.27, alpha: 1), ring: NSColor.black.withAlphaComponent(0.4),
          label: .white, gloss: 0.12,
          labelShadow: NSColor.black.withAlphaComponent(0.4))
      }
      let selected = compactStyle == .selected
      let tint = PaxTheme.Routes.blue
      return Recipe(
        base: selected
          ? (NSColor(white: 0.96, alpha: 1).blended(withFraction: 0.14, of: tint) ?? .white)
          : NSColor(white: 0.955, alpha: 1),
        ring: selected ? tint.withAlphaComponent(0.55) : NSColor.black.withAlphaComponent(0.13),
        label: selected ? tint : PaxTheme.Color.text, gloss: 0.9,
        labelShadow: NSColor.white.withAlphaComponent(0.8))
    }
  }

  var capsuleSize: NSSize {
    let widths = capsuleWidths([.font: capsuleFont])
    return NSSize(
      width: widths.glyph + widths.text + widths.tail
        + 2 * (Self.capsulePadding + Self.capsuleInset),
      height: capsuleHeight)
  }

  private func capsuleWidths(_ attrs: [NSAttributedString.Key: Any])
    -> (glyph: CGFloat, text: CGFloat, tail: CGFloat)
  {
    let glyph = image == nil ? 0 : PaxTheme.Dashboard.iconGlyph + PaxTheme.Spacing.tight
    let tail = trailingWidth(attrs)
    return (glyph, (title as NSString).size(withAttributes: attrs).width, tail)
  }

  func drawCapsule() {
    let rect = bounds.insetBy(dx: Self.capsuleInset, dy: Self.capsuleInset)
    let radius = rect.height * 0.32
    let path = NSBezierPath(roundedRect: rect, xRadius: radius, yRadius: radius)
    let look = recipe()
    let down: CGFloat = isFlipped ? 1 : -1
    let fade: CGFloat = isEnabled ? 1 : 0.45
    NSGraphicsContext.saveGraphicsState()
    // Outer ring (1pt of the ring colour around the face) with a faint drop shadow behind it.
    let ring = NSBezierPath(
      roundedRect: rect.insetBy(dx: -1, dy: -1), xRadius: radius + 1, yRadius: radius + 1)
    let shadow = NSShadow()
    shadow.shadowColor = NSColor.black.withAlphaComponent(0.1 * fade)
    shadow.shadowBlurRadius = 2.5
    shadow.shadowOffset = NSSize(width: 0, height: 1.25 * down)
    shadow.set()
    look.ring.withAlphaComponent(look.ring.alphaComponent * fade).setFill()
    ring.fill()
    NSGraphicsContext.restoreGraphicsState()
    NSGraphicsContext.saveGraphicsState()
    path.addClip()
    look.base.withAlphaComponent(fade).setFill()
    path.fill()
    NSGradient(
      starting: NSColor.white.withAlphaComponent(look.gloss * 0.5 * fade),
      ending: NSColor.white.withAlphaComponent(0))?
      .draw(in: rect, angle: isFlipped ? 90 : -90)
    // Top-only highlight: the path stroked 3pt wide (1.5 inside), clipped to the upper third.
    let top = NSRect(
      x: rect.minX, y: isFlipped ? rect.minY : rect.midY, width: rect.width,
      height: rect.height / 2)
    NSBezierPath(rect: top).addClip()
    NSColor.white.withAlphaComponent(min(1, look.gloss + 0.1) * 0.5 * fade).setStroke()
    path.lineWidth = 3
    path.stroke()
    NSGraphicsContext.restoreGraphicsState()
    if isHighlighted {
      NSColor.black.withAlphaComponent(0.12).setFill()
      path.fill()
    }
    let label = destructive && resolvedTone == .light ? PaxTheme.Color.error : look.label
    drawCapsuleLabel(in: rect, color: label.withAlphaComponent(fade), shadow: look.labelShadow)
  }

  private func drawCapsuleLabel(in rect: NSRect, color: NSColor, shadow: NSColor?) {
    let embossed = NSShadow()
    embossed.shadowColor = shadow
    embossed.shadowBlurRadius = 1.5
    embossed.shadowOffset = NSSize(width: 0, height: isFlipped ? 1 : -1)
    var attrs: [NSAttributedString.Key: Any] = [.font: capsuleFont, .foregroundColor: color]
    if shadow != nil { attrs[.shadow] = embossed }
    let widths = capsuleWidths(attrs)
    let size = (title as NSString).size(withAttributes: attrs)
    let textWidth = min(
      widths.text,
      max(0, rect.width - 2 * Self.capsulePadding - widths.glyph - widths.tail))
    let total = widths.glyph + textWidth + widths.tail
    var x = leadingAligned ? rect.minX + Self.capsulePadding : rect.midX - total / 2
    if let image {
      let side = PaxTheme.Dashboard.iconGlyph
      image.draw(
        in: NSRect(x: x, y: rect.midY - side / 2, width: side, height: side),
        from: .zero, operation: .sourceOver,
        fraction: isEnabled ? 1 : PaxTheme.Control.disabledOpacity,
        respectFlipped: true, hints: nil)
      x += widths.glyph
    }
    let paragraph = NSMutableParagraphStyle()
    paragraph.lineBreakMode = .byTruncatingTail
    attrs[.paragraphStyle] = paragraph
    let textRect = NSRect(
      x: x, y: rect.midY - size.height / 2, width: textWidth, height: size.height)
    NSGraphicsContext.saveGraphicsState()
    NSBezierPath(rect: textRect).addClip()
    (title as NSString).draw(in: textRect, withAttributes: attrs)
    NSGraphicsContext.restoreGraphicsState()
    if let trailingImage {
      drawTrailingImage(
        trailingImage, color: color,
        rect: NSRect(
          x: rect.maxX - Self.capsulePadding - trailingImage.size.width,
          y: rect.midY - trailingImage.size.height / 2,
          width: trailingImage.size.width, height: trailingImage.size.height))
    } else if let trailingGlyph {
      let glyphWidth = widths.tail - PaxTheme.Spacing.tight
      (trailingGlyph as NSString).draw(
        at: NSPoint(
          x: rect.maxX - Self.capsulePadding - glyphWidth, y: rect.midY - size.height / 2),
        withAttributes: attrs)
    }
  }
}
