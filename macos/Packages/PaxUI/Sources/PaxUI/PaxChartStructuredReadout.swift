import AppKit

extension PaxAnalysisChart.Hover {
  /// The structured card and keyboard readout expose identical facts to accessibility.
  var accessibilityText: String {
    ([title, subtitle] + badges.map(\.title) + (tag.map { [$0.text] } ?? [])
      + summary.map { $0.label + " " + $0.value + " " + summarySuffix }
      + breakdown.map { $0.name + " " + $0.amount + " " + $0.share }
      + details.map { $0.label + " " + $0.value } + [context])
      .filter { !$0.isEmpty }.joined(separator: " · ")
  }
}

/// Hosts the existing H1 renderer inside the shared non-interactive chart popover.
@MainActor
final class PaxChartStructuredReadout: NSView {
  private var hover: PaxAnalysisChart.Hover?
  override var isFlipped: Bool { true }

  func apply(_ hover: PaxAnalysisChart.Hover) {
    self.hover = hover
    setFrameSize(
      .init(
        width: PaxTheme.Chart.analysisHoverWidth + PaxTheme.Spacing.medium,
        height: PaxAnalysisHoverCard.height(of: hover) + PaxTheme.Spacing.medium))
    setAccessibilityElement(true)
    setAccessibilityRole(.group)
    setAccessibilityLabel(hover.accessibilityText)
    needsDisplay = true
  }

  override func draw(_ dirtyRect: NSRect) {
    guard let hover else { return }
    PaxAnalysisHoverCard.draw(hover, anchorX: 0, anchorY: bounds.midY, in: bounds)
  }
}
