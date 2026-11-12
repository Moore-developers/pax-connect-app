import AppKit
import PaxUI

/// Measures its real controls before choosing one or two rows; chart refreshes retain this view.
@MainActor
final class AIUsageTrendHeader: NSView {
  private let tile = PaxIconTile("chart.bar")
  private let title = AIUsageLayout.heading("用量趋势")
  private let meta: NSTextField
  private let legend: NSView
  private let picker: PaxRoutePicker
  private let measure: PaxTabs
  private var height: NSLayoutConstraint?
  override var isFlipped: Bool { true }

  init(meta: NSTextField, legend: NSView, picker: PaxRoutePicker, measure: PaxTabs) {
    self.meta = meta
    self.legend = legend
    self.picker = picker
    self.measure = measure
    super.init(frame: .zero)
    for view in [tile, title, meta, legend, picker, measure] { addSubview(view) }
    height = heightAnchor.constraint(equalToConstant: singleHeight)
    height?.isActive = true
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  private var gap: CGFloat { PaxTheme.Spacing.medium }
  private var pickerWidth: CGFloat {
    picker.constraints.first { $0.firstAttribute == .width && $0.secondItem == nil }?.constant
      ?? 112
  }
  private var textWidth: CGFloat {
    max(title.intrinsicContentSize.width, meta.intrinsicContentSize.width)
  }
  private var singleHeight: CGFloat {
    max(
      PaxTheme.Dashboard.icon, picker.intrinsicContentSize.height,
      title.intrinsicContentSize.height + PaxTheme.Spacing.xs + cardSubHeight)
  }
  var singleRowWidth: CGFloat {
    PaxTheme.Dashboard.icon + textWidth + legend.fittingSize.width + pickerWidth
      + measure.intrinsicContentSize.width + gap * 4
  }
  var usesTwoRows: Bool { bounds.width < singleRowWidth }

  override func layout() {
    super.layout()
    let icon = PaxTheme.Dashboard.icon
    let tabs = measure.intrinsicContentSize
    let pick = NSSize(width: pickerWidth, height: picker.intrinsicContentSize.height)
    let key = legend.fittingSize
    let first = max(singleHeight, tabs.height)
    let second = max(pick.height, key.height, meta.intrinsicContentSize.height)
    let total = usesTwoRows ? first + PaxTheme.Spacing.tight + second : first
    if height?.constant != total { height?.constant = total }
    func place(_ view: NSView, x: CGFloat, y: CGFloat, size: NSSize, row: CGFloat) {
      view.frame = NSRect(
        x: x, y: y + (row - size.height) / 2, width: size.width, height: size.height)
    }
    place(tile, x: 0, y: 0, size: NSSize(width: icon, height: icon), row: first)
    place(measure, x: bounds.width - tabs.width, y: 0, size: tabs, row: first)
    if usesTwoRows {
      place(title, x: icon + gap, y: 0, size: title.intrinsicContentSize, row: first)
      let y = first + PaxTheme.Spacing.tight
      place(meta, x: 0, y: y, size: meta.intrinsicContentSize, row: second)
      place(legend, x: meta.frame.maxX + PaxTheme.Spacing.tight, y: y, size: key, row: second)
      place(picker, x: bounds.width - pick.width, y: y, size: pick, row: second)
    } else {
      let textHeight =
        title.intrinsicContentSize.height + PaxTheme.Spacing.xs
        + meta.intrinsicContentSize.height
      title.frame = NSRect(
        x: icon + gap, y: (first - textHeight) / 2,
        width: textWidth, height: title.intrinsicContentSize.height)
      meta.frame = NSRect(
        x: icon + gap, y: title.frame.maxY + PaxTheme.Spacing.xs,
        width: textWidth, height: meta.intrinsicContentSize.height)
      place(picker, x: measure.frame.minX - gap - pick.width, y: 0, size: pick, row: first)
      place(legend, x: picker.frame.minX - gap - key.width, y: 0, size: key, row: first)
    }
  }
}
