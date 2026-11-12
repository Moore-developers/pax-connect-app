import AppKit
import PaxUI

/// Reflows paired log values using their measured widths, preserving the table and filters.
@MainActor
final class AIUsageLogPairView: NSView {
  private let first: NSView
  private let second: NSView
  private let firstSize: NSSize
  private let secondSize: NSSize
  private let trailing: Bool
  private let centered: Bool
  private(set) var isSingleLine = false

  init(_ first: NSView, _ second: NSView, trailing: Bool = false, centered: Bool = false) {
    self.first = first
    self.second = second
    // Centered text measures slightly wider, so set the alignment before measuring.
    if centered {
      for case let field as NSTextField in [first, second] { field.alignment = .center }
    }
    firstSize = first.fittingSize
    secondSize = second.fittingSize
    self.trailing = trailing
    self.centered = centered
    super.init(frame: .zero)
    for child in [first, second] {
      child.translatesAutoresizingMaskIntoConstraints = true
      addSubview(child)
    }
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  override var intrinsicContentSize: NSSize {
    .init(width: NSView.noIntrinsicMetric, height: PaxTheme.Dashboard.rowHeight)
  }

  override func layout() {
    super.layout()
    let gap = PaxTheme.Spacing.tight
    let total = firstSize.width + gap + secondSize.width
    isSingleLine = total <= bounds.width
    if isSingleLine {
      let x = centered ? (bounds.width - total) / 2 : trailing ? bounds.width - total : 0
      first.frame = .init(
        x: x, y: (bounds.height - firstSize.height) / 2,
        width: firstSize.width, height: firstSize.height)
      second.frame = .init(
        x: x + firstSize.width + gap, y: (bounds.height - secondSize.height) / 2,
        width: secondSize.width, height: secondSize.height)
    } else {
      let height = firstSize.height + PaxTheme.Spacing.xs + secondSize.height
      let bottom = (bounds.height - height) / 2
      first.frame = .init(
        x: 0, y: bottom + secondSize.height + PaxTheme.Spacing.xs,
        width: bounds.width, height: firstSize.height)
      second.frame = .init(
        x: 0, y: bottom, width: bounds.width, height: secondSize.height)
    }
  }
}
