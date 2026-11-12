import AppKit
import PaxCore
import PaxUI

@MainActor final class RoutingDNSSummary: NSStackView {
  private let heading = PaxText.label(
    "域名解析", font: PaxTheme.Typography.label(size: PaxTheme.Table.font))
  private let value = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private let scope = PaxText.label(
    "全部解析记录，不受连接去向筛选", font: PaxTheme.Typography.caption(), secondary: true)
  private lazy var open = PaxRouteButton(
    "查看查询", style: .quiet, target: self, action: #selector(show))
  var onOpen: (() -> Void)?
  override init(frame: NSRect) {
    super.init(frame: frame)
    orientation = .horizontal
    spacing = PaxTheme.Spacing.medium
    alignment = .centerY
    let words = NSStackView(views: [heading, value, scope])
    words.orientation = .vertical
    words.alignment = .leading
    words.spacing = PaxTheme.Spacing.xs
    addArrangedSubview(words)
    addArrangedSubview(NSView())
    addArrangedSubview(open)
    setAccessibilityIdentifier("routing-dns-summary")
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }
  func update(_ row: TrafficRow?, unavailable: String? = nil) {
    if let row {
      value.stringValue =
        "查询 \(row.connections) 次 · 失败 \(row.failedCount) 次 · 最近解析耗时 "
        + TrafficFormatting.milliseconds(TrafficFormatting.latestLatency(row))
      open.isEnabled = true
    } else {
      value.stringValue = unavailable ?? "当前时间与搜索范围未记录DNS查询"
      open.isEnabled = false
    }
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
