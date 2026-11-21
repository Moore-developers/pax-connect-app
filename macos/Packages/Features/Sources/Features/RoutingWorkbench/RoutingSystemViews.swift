import AppKit
import PaxCore
import PaxUI

@MainActor final class RoutingDNSSummary: NSStackView {
  private let heading = PaxText.label(
    "", font: PaxTheme.Typography.label(size: PaxTheme.Table.font))
  private let value = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private lazy var open = PaxRouteButton(
    "", style: .quiet, target: self, action: #selector(show))
  var onOpen: (() -> Void)?
  override init(frame: NSRect) {
    super.init(frame: frame)
    orientation = .horizontal
    spacing = PaxTheme.Spacing.tight
    alignment = .centerY
    value.lineBreakMode = .byTruncatingTail
    value.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    addArrangedSubview(heading)
    addArrangedSubview(value)
    addArrangedSubview(open)
    open.setAccessibilityIdentifier("routing-dns-queries")
    setAccessibilityIdentifier("routing-dns-summary")
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }
  func update(_ row: TrafficRow?, unavailable: String?, context: PresentationContext) {
    heading.stringValue = context.text("traffic_details.dns_summary_title")
    let scope = context.text("traffic_details.dns_summary_scope")
    let title = context.text("traffic_details.dns_summary_open")
    if open.title != title { open.title = title }
    let text: String
    if let row {
      text = context.text(
        "traffic_details.dns_summary_value",
        [
          "queries": String(row.connections), "failed": String(row.failedCount),
          "time": TrafficFormatting.milliseconds(TrafficFormatting.latestLatency(row)),
        ])
    } else {
      text = unavailable ?? context.text("traffic_details.dns_summary_empty")
    }
    if value.stringValue != text { value.stringValue = text }
    value.toolTip = [text, scope].joined(separator: "\n")
    open.toolTip = scope
    open.setAccessibilityHelp(scope)
    if open.isEnabled != (row != nil) { open.isEnabled = row != nil }
  }
  @objc private func show() { onOpen?() }
}

/// Only one mutually exclusive table participates in layout. Keep the table
/// instances and scroll state; avoid creating old cells before applying a new page.
@MainActor final class RoutingTrafficHost: NSView {
  private var childConstraints: [NSLayoutConstraint] = []
  func show(_ active: NSView) {
    guard active.superview !== self else { return }
    NSLayoutConstraint.deactivate(childConstraints)
    for child in subviews { child.removeFromSuperview() }
    active.isHidden = true
    active.translatesAutoresizingMaskIntoConstraints = false
    addSubview(active)
    childConstraints = [
      active.leadingAnchor.constraint(equalTo: leadingAnchor),
      active.trailingAnchor.constraint(equalTo: trailingAnchor),
      active.topAnchor.constraint(equalTo: topAnchor),
      active.bottomAnchor.constraint(equalTo: bottomAnchor),
    ]
    NSLayoutConstraint.activate(childConstraints)
  }
}
