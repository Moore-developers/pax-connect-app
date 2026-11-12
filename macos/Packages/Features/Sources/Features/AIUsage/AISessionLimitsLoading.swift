import AppKit
import PaxUI

/// A section keeps its place while loading, without showing the previous session's values.
@MainActor final class AILimitLoadingSlot: NSView {
  private let content: NSView
  private let placeholder: NSView
  private var realHeight: NSLayoutConstraint?
  private var reservedHeight: NSLayoutConstraint?
  private var loading = false

  init(_ content: NSView, placeholder: NSView, height: CGFloat) {
    self.content = content
    self.placeholder = placeholder
    super.init(frame: .zero)
    for child in [content, placeholder] {
      child.translatesAutoresizingMaskIntoConstraints = false
      addSubview(child)
      NSLayoutConstraint.activate([
        child.topAnchor.constraint(equalTo: topAnchor),
        child.leadingAnchor.constraint(equalTo: leadingAnchor),
        child.trailingAnchor.constraint(equalTo: trailingAnchor),
      ])
    }
    placeholder.bottomAnchor.constraint(equalTo: bottomAnchor).isActive = true
    placeholder.isHidden = true
    realHeight = heightAnchor.constraint(equalTo: content.heightAnchor)
    reservedHeight = heightAnchor.constraint(equalToConstant: height)
    realHeight?.isActive = true
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  func setLoading(_ value: Bool, height: CGFloat? = nil) {
    if let height { reservedHeight?.constant = height }
    guard loading != value else { return }
    loading = value
    if value {
      realHeight?.isActive = false
      reservedHeight?.isActive = true
    } else {
      reservedHeight?.isActive = false
      realHeight?.isActive = true
    }
    content.isHidden = value
    placeholder.isHidden = !value
  }

  static func summary() -> NSView {
    let cards = (0..<3).map { _ in AIUsageLayout.card(NSView()) }
    let row = AIUsageLayout.stack(cards, vertical: false)
    row.spacing = PaxTheme.Spacing.regular
    for card in cards { card.heightAnchor.constraint(equalTo: row.heightAnchor).isActive = true }
    NSLayoutConstraint.activate([
      cards[0].widthAnchor.constraint(equalTo: cards[1].widthAnchor, constant: 24),
      cards[2].widthAnchor.constraint(equalTo: cards[1].widthAnchor, multiplier: 1.25),
    ])
    row.setAccessibilityElement(true)
    row.setAccessibilityLabel("会话汇总正在读取")
    return row
  }

  static func trend() -> NSView {
    let title = AIUsageLayout.heading("趋势分析")
    title.heightAnchor.constraint(equalToConstant: PaxTheme.Dashboard.tabHeight).isActive = true
    let column = AIUsageLayout.stack([
      title, AIUsageLayout.card(NSView()),
    ])
    column.setAccessibilityElement(true)
    column.setAccessibilityLabel("会话趋势正在读取")
    return column
  }
}
