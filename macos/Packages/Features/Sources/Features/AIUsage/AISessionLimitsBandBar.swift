import AppKit
import PaxCore
import PaxUI

/// Annotated three-band bar of P07d-ai-session-limits §4.2: 12pt segments with 3pt gaps; each
/// segment has a 1pt line with a dot at its left edge and its label to the right of the line.
/// The over-limit segment is Coral with 45° stripes (PaxShareBar.stripes). A segment too narrow
/// for its label keeps only the dot and line.
final class AILimitBandBar: NSView {
  struct Segment: Equatable {
    var share: Int
    var text: String
    var fill: NSColor
    var tone: NSColor
    var striped = false
  }
  static let barHeight: CGFloat = 12
  static let gap: CGFloat = 3
  private static let labelHeight: CGFloat = 17
  private static let radius: CGFloat = 4
  private static let font = NSFont.systemFont(ofSize: 10.5, weight: .regular)
  private var segments: [Segment]

  init(segments: [Segment]) {
    self.segments = segments.filter { $0.share > 0 }
    super.init(frame: .zero)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override var isFlipped: Bool { true }
  override var intrinsicContentSize: NSSize {
    .init(width: NSView.noIntrinsicMetric, height: Self.labelHeight + Self.barHeight)
  }
  override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }

  func apply(segments: [Segment]) {
    let visible = segments.filter { $0.share > 0 }
    guard self.segments != visible else { return }
    self.segments = visible
    needsDisplay = true
  }

  /// Segment widths by share, after the gaps are taken out.
  private func frames() -> [NSRect] {
    let total = CGFloat(segments.reduce(0) { $0 + $1.share })
    let room = bounds.width - Self.gap * CGFloat(max(0, segments.count - 1))
    var x = bounds.minX
    return segments.map { segment in
      let width = total > 0 ? room * CGFloat(segment.share) / total : 0
      defer { x += width + Self.gap }
      return NSRect(x: x, y: Self.labelHeight, width: width, height: Self.barHeight)
    }
  }

  override func draw(_ dirtyRect: NSRect) {
    // AppKit can ask unclipped descendants of the long card to draw offscreen.
    guard bounds.intersects(visibleRect) else { return }
    let attributes: [NSAttributedString.Key: Any] = [.font: Self.font]
    for (segment, rect) in zip(segments, frames()) {
      let shape = NSBezierPath(roundedRect: rect, xRadius: Self.radius, yRadius: Self.radius)
      segment.fill.setFill()
      shape.fill()
      if segment.striped {
        NSGraphicsContext.saveGraphicsState()
        shape.addClip()
        PaxShareBar.stripes(in: rect)
        NSGraphicsContext.restoreGraphicsState()
      }
      let top: CGFloat = 2.5
      let line = NSRect(
        x: rect.minX, y: top, width: PaxTheme.Layout.cardBorder, height: rect.minY - top)
      segment.tone.setFill()
      line.fill()
      NSBezierPath(ovalIn: NSRect(x: rect.minX - 2, y: top - 2.5, width: 5, height: 5)).fill()
      let size = (segment.text as NSString).size(withAttributes: attributes)
      guard rect.width >= size.width + 12 else { continue }
      (segment.text as NSString).draw(
        at: .init(x: rect.minX + 6, y: 0),
        withAttributes: attributes.merging([.foregroundColor: segment.tone]) { $1 })
    }
  }
}
