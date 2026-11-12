import AppKit
import PaxCore
import PaxUI

@MainActor
final class AIUsageActions: NSObject {
  private var handlers: [String: () -> Void] = [:]
  func button(
    _ title: String, primary: Bool = false, enabled: Bool = true, id: String? = nil,
    _ handler: @escaping () -> Void
  ) -> PaxCompactButton {
    let key = UUID().uuidString
    handlers[key] = handler
    let button = PaxCompactButton(
      title, style: primary ? .primary : .bordered, target: self, action: #selector(activate(_:)))
    button.capsule = true
    button.identifier = .init(key)
    button.isEnabled = enabled
    if let id { button.setAccessibilityIdentifier(id) }
    return button
  }
  @objc private func activate(_ sender: NSButton) {
    guard let id = sender.identifier?.rawValue else { return }
    handlers[id]?()
  }
  func overlay(label: String, id: String? = nil, _ handler: @escaping () -> Void) -> NSButton {
    let button = self.button("", id: id, handler)
    button.setAccessibilityLabel(label)
    button.toolTip = label
    button.isTransparent = true
    return button
  }
  func reset() { handlers.removeAll() }
}

/// Second line of every card header; both analysis cards use it so icon, title and control centers align.
let cardSubHeight: CGFloat = 18

@MainActor
enum AIUsageLayout {
  private static let factTitleColumn: CGFloat = 94
  static func label(
    _ text: String, secondary: Bool = false, metric: Bool = false, numeric: Bool = false
  ) -> NSTextField {
    PaxText.label(
      text,
      font: metric
        ? PaxTheme.Typography.secondaryMetric
        : numeric
          ? PaxTheme.Typography.time
          : PaxTheme.Typography.label(
            size: secondary ? PaxTheme.Dashboard.captionFont : PaxTheme.Dashboard.bodyFont),
      secondary: secondary, wrapping: false)
  }
  static func stack(_ views: [NSView], vertical: Bool = true, equal: Bool = false) -> NSStackView {
    let stack = NSStackView(views: views)
    stack.orientation = vertical ? .vertical : .horizontal
    stack.alignment = vertical ? .leading : .centerY
    stack.spacing = vertical ? PaxTheme.Spacing.tight : PaxTheme.Spacing.cardGap
    if equal { stack.distribution = .fillEqually }
    if vertical {
      for view in views { view.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true }
    }
    return stack
  }
  static func card(
    _ content: NSView, height: CGFloat? = nil, verticalInset: CGFloat? = nil,
    inset: CGFloat = PaxTheme.Spacing.card
  )
    -> PaxCard
  {
    let card = PaxCard(
      insets: .init(
        top: verticalInset ?? inset, left: inset, bottom: verticalInset ?? inset, right: inset),
    )
    card.contentView = content
    if let height {
      card.heightAnchor.constraint(equalToConstant: height).isActive = true
    } else {
      card.heightAnchor.constraint(
        equalTo: content.heightAnchor, constant: inset * 2
      ).isActive = true
    }
    return card
  }
  static func heading(_ text: String) -> NSTextField {
    PaxText.label(
      text, font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyTitle, weight: .medium))
  }
  /// Card head: icon tile, title with an optional second line, then trailing controls.
  static func cardHeader(
    symbol: String, title: String, sub: NSView? = nil, trailing: [NSView] = []
  ) -> NSStackView {
    let tile = PaxIconTile(symbol)
    tile.translatesAutoresizingMaskIntoConstraints = false
    tile.widthAnchor.constraint(equalToConstant: PaxTheme.Dashboard.icon).isActive = true
    tile.heightAnchor.constraint(equalToConstant: PaxTheme.Dashboard.icon).isActive = true
    sub?.heightAnchor.constraint(equalToConstant: cardSubHeight).isActive = true
    let title = heading(title)
    let text = NSStackView(views: [title] + (sub.map { [$0] } ?? []))
    text.orientation = .vertical
    text.alignment = .leading
    text.spacing = PaxTheme.Spacing.xs
    let row = stack([tile, text, NSView()] + trailing, vertical: false)
    row.spacing = PaxTheme.Spacing.medium
    if sub == nil {
      // A single-line title still takes the two-line header height, so icons line up across cards.
      let height = title.intrinsicContentSize.height + PaxTheme.Spacing.xs + cardSubHeight
      row.heightAnchor.constraint(greaterThanOrEqualToConstant: height).isActive = true
    }
    return row
  }
  static func facts(_ values: [(String, String)]) -> NSView {
    let grid = NSGridView(
      views: values.map { [label($0.0, secondary: true), label($0.1, numeric: true)] })
    grid.columnSpacing = PaxTheme.Spacing.regular
    grid.rowSpacing = PaxTheme.Spacing.tight
    grid.column(at: 0).width = Self.factTitleColumn
    return grid
  }
  static func composition(
    _ labels: [String], values: [Double], center: String, stacked: Bool = false,
    format: (Double) -> String
  ) -> NSView {
    let colors = PaxTheme.Chart.categorySeries
    let donut = PaxDonutChart(
      segments: zip(labels, values).map { .init($0, value: $1) }, center: center, colors: colors)
    donut.widthAnchor.constraint(equalToConstant: PaxTheme.Chart.donutSize).isActive = true
    donut.heightAnchor.constraint(equalTo: donut.widthAnchor).isActive = true
    let total = values.reduce(0, +)
    let rows = zip(labels, values).enumerated().map { index, part -> NSView in
      let name = label(part.0)
      let value = label(format(part.1), numeric: true)
      let dot = PaxLegendDot(color: colors[index % colors.count])
      return stack(
        [
          dot, name, NSView(), value,
          label(
            AIUsageFormatting.percent(total > 0 ? part.1 / total : nil), secondary: true,
            numeric: true),
        ], vertical: false)
    }
    let holder = NSView()
    donut.translatesAutoresizingMaskIntoConstraints = false
    holder.addSubview(donut)
    NSLayoutConstraint.activate([
      holder.heightAnchor.constraint(equalToConstant: PaxTheme.Chart.donutSize),
      donut.centerXAnchor.constraint(equalTo: holder.centerXAnchor),
      donut.centerYAnchor.constraint(equalTo: holder.centerYAnchor),
    ])
    if !stacked {
      holder.widthAnchor.constraint(equalToConstant: PaxTheme.Chart.donutSize).isActive = true
    }
    return stack([holder, stack(rows)], vertical: stacked)
  }
  static func wrap(_ child: NSView, width: CGFloat? = nil, height: CGFloat? = nil) -> NSView {
    let holder = NSView()
    child.translatesAutoresizingMaskIntoConstraints = false
    holder.addSubview(child)
    var constraints = [
      child.centerYAnchor.constraint(equalTo: holder.centerYAnchor),
      child.leadingAnchor.constraint(equalTo: holder.leadingAnchor),
    ]
    if let width { constraints.append(holder.widthAnchor.constraint(equalToConstant: width)) }
    if let height {
      constraints.append(holder.heightAnchor.constraint(equalToConstant: height))
    } else {
      constraints.append(holder.heightAnchor.constraint(equalTo: child.heightAnchor))
    }
    NSLayoutConstraint.activate(constraints)
    return holder
  }
  static func wrapCentered(_ child: NSView) -> NSView {
    let holder = NSView()
    child.translatesAutoresizingMaskIntoConstraints = false
    holder.addSubview(child)
    NSLayoutConstraint.activate([
      child.centerXAnchor.constraint(equalTo: holder.centerXAnchor),
      child.centerYAnchor.constraint(equalTo: holder.centerYAnchor),
      child.widthAnchor.constraint(lessThanOrEqualTo: holder.widthAnchor),
      holder.heightAnchor.constraint(equalTo: child.heightAnchor),
    ])
    return holder
  }
  static func separator() -> NSView {
    let line = NSBox()
    line.boxType = .separator
    return line
  }
  /// One standard card of equal-width segments separated by vertical dividers.
  static func segmentedCard(_ segments: [NSView]) -> PaxCard {
    var views: [NSView] = []
    for (index, segment) in segments.enumerated() {
      if index > 0 { views.append(separator()) }
      views.append(segment)
    }
    let row = NSStackView(views: views)
    row.orientation = .horizontal
    row.alignment = .top
    row.distribution = .fill
    row.spacing = PaxTheme.Spacing.regular
    for view in views {
      if segments.contains(where: { $0 === view }) {
        view.widthAnchor.constraint(equalTo: segments[0].widthAnchor).isActive = true
      } else {
        view.widthAnchor.constraint(equalToConstant: PaxTheme.Layout.cardBorder).isActive = true
        view.heightAnchor.constraint(equalTo: row.heightAnchor).isActive = true
      }
    }
    return card(row)
  }
  static func equalRow(_ views: [NSView]) -> NSView { stack(views, vertical: false, equal: true) }
}
