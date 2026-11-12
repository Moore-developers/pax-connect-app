import AppKit
import PaxCore
import PaxUI

/// The recorded exits remain on one row; overflow scrolls without hiding an exit.
@MainActor
final class WorkbenchTrafficExitTags: NSScrollView {
  init(snapshot: WorkbenchTrafficDetail) {
    super.init(frame: .zero)
    drawsBackground = false
    borderType = .noBorder
    hasVerticalScroller = false
    hasHorizontalScroller = false
    horizontalScrollElasticity = .allowed
    let exits = snapshot.aggregate?.summary.exits ?? []
    var entries: [(String, String)] = exits.map {
      (
        TrafficFormatting.actualConnection(direction: $0.direction, route: $0.routeName),
        $0.direction
      )
    }
    if entries.isEmpty {
      let direction = snapshot.aggregate?.summary.direction ?? snapshot.direction
      let route = snapshot.aggregate?.summary.routeName ?? snapshot.route
      entries = [
        (TrafficFormatting.actualConnection(direction: direction, route: route), direction)
      ]
    }
    if let summary = snapshot.aggregate?.summary, TrafficFormatting.isIPv6LimitedPath(summary) {
      entries = [("已阻断", "blocked")]
    }
    let tags = entries.map { name, direction in
      let style: PaxTag.Style =
        ["proxy", "代理"].contains(direction)
        ? .proxy
        : ["direct", "直连"].contains(direction) ? .direct : .subtle
      let tag = PaxTag(name, style: style)
      tag.toolTip = name
      tag.setAccessibilityLabel(name)
      tag.setContentHuggingPriority(.required, for: .horizontal)
      tag.setContentCompressionResistancePriority(.required, for: .horizontal)
      return tag
    }
    let row = NSStackView(views: tags)
    row.orientation = .horizontal
    row.spacing = PaxTheme.Spacing.tight
    row.alignment = .centerY
    row.frame = NSRect(
      origin: .zero,
      size: NSSize(
        width: tags.reduce(0) { $0 + $1.intrinsicContentSize.width }
          + CGFloat(max(0, tags.count - 1)) * row.spacing,
        height: PaxTheme.Dashboard.tableBadgeHeight))
    documentView = row
    heightAnchor.constraint(equalToConstant: PaxTheme.Dashboard.tableBadgeHeight).isActive = true
    setAccessibilityIdentifier("traffic-detail-exit-tags")
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }
}
