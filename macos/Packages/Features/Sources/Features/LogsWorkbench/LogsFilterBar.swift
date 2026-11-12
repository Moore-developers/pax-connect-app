import AppKit
import PaxUI

/// One centered filter row; overflow scrolls horizontally in narrow inspectors.
@MainActor final class LogsFilterBar: NSScrollView {
  private let controls: [NSView]
  private let content = NSView()
  init(level: PaxTabs, range: PaxRoutePicker) {
    controls = [level, range]
    super.init(frame: .zero)
    drawsBackground = false
    hasHorizontalScroller = true
    autohidesScrollers = true
    documentView = content
    for v in controls { content.addSubview(v) }
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override func layout() {
    super.layout()
    let gap = PaxTheme.Spacing.tight
    let sizes = controls.map(\.intrinsicContentSize)
    let widths = [sizes[0].width, max(136, sizes[1].width)]
    let width = max(contentSize.width, widths.reduce(0, +) + gap)
    content.frame = NSRect(x: 0, y: 0, width: width, height: PaxTheme.Logs.toolsHeight)
    for (i, v) in controls.enumerated() {
      let x: CGFloat = i == 0 ? 0 : width - widths[1]
      v.frame = NSRect(
        x: x, y: (PaxTheme.Logs.toolsHeight - sizes[i].height) / 2, width: widths[i],
        height: sizes[i].height)
    }
  }
}
