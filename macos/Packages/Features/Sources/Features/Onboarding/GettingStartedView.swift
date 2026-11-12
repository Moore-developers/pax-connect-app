import AppKit
import PaxUI

/// One guidance surface shared across the real pages. It owns no product state.
@MainActor final class GettingStartedView: NSView {
  private let title = PaxText.label("", font: PaxTheme.Typography.section)
  private let detail = PaxText.label(
    "", font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  private let primary = PaxButton(.prominent, title: "")
  private let secondary = PaxButton(.secondary, title: "")
  private let skip = PaxButton(.secondary, title: "稍后再说")
  private let host = NSStackView()
  private var rendered: Model?
  private var measuredWidth: CGFloat = -1
  struct Model: Equatable {
    var title: String
    var detail: String
    var primary: String
    var secondary: String?
  }
  var onPrimary: (() -> Void)?
  var onSecondary: (() -> Void)?
  var onSkip: (() -> Void)?
  init() {
    super.init(frame: .zero)
    let card = PaxCard()
    let buttons = NSStackView(views: [primary, secondary, NSView(), skip])
    buttons.spacing = PaxTheme.Spacing.regular
    let body = NSStackView(views: [title, detail, buttons])
    body.orientation = .vertical
    body.alignment = .leading
    body.spacing = PaxTheme.Spacing.tight
    detail.widthAnchor.constraint(equalTo: body.widthAnchor).isActive = true
    buttons.widthAnchor.constraint(equalTo: body.widthAnchor).isActive = true
    card.contentView = body
    body.bottomAnchor.constraint(equalTo: card.bottomAnchor, constant: -PaxTheme.Spacing.card)
      .isActive = true
    body.setContentHuggingPriority(.required, for: .vertical)
    title.setContentHuggingPriority(.required, for: .vertical)
    detail.setContentHuggingPriority(.required, for: .vertical)
    buttons.setContentHuggingPriority(.required, for: .vertical)
    host.orientation = .vertical
    host.edgeInsets = NSEdgeInsets(
      top: PaxTheme.Spacing.regular, left: PaxTheme.Spacing.page, bottom: 0,
      right: PaxTheme.Spacing.page)
    host.addArrangedSubview(card)
    card.widthAnchor.constraint(equalTo: host.widthAnchor, constant: -2 * PaxTheme.Spacing.page)
      .isActive = true
    host.translatesAutoresizingMaskIntoConstraints = false
    addSubview(host)
    NSLayoutConstraint.activate([
      host.leadingAnchor.constraint(equalTo: leadingAnchor),
      host.trailingAnchor.constraint(equalTo: trailingAnchor),
      host.topAnchor.constraint(equalTo: topAnchor),
      host.bottomAnchor.constraint(equalTo: bottomAnchor),
    ])
    for button in [primary, secondary, skip] { button.target = self }
    primary.action = #selector(tapPrimary)
    secondary.action = #selector(tapSecondary)
    skip.action = #selector(tapSkip)
    primary.setAccessibilityIdentifier("onboarding-primary")
    secondary.setAccessibilityIdentifier("onboarding-secondary")
    skip.setAccessibilityIdentifier("onboarding-skip")
    setAccessibilityIdentifier("getting-started")
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  func apply(_ model: Model?) {
    isHidden = model == nil
    guard let model, rendered != model else { return }
    rendered = model
    title.stringValue = model.title
    detail.stringValue = model.detail
    primary.title = model.primary
    secondary.title = model.secondary ?? ""
    secondary.isHidden = model.secondary == nil
    setAccessibilityLabel(model.title + "，" + model.detail)
    invalidateIntrinsicContentSize()
    needsLayout = true
  }
  override var intrinsicContentSize: NSSize {
    NSSize(width: NSView.noIntrinsicMetric, height: host.fittingSize.height)
  }
  override func layout() {
    super.layout()
    if measuredWidth != bounds.width {
      measuredWidth = bounds.width
      invalidateIntrinsicContentSize()
    }
  }
  @objc private func tapPrimary() { onPrimary?() }
  @objc private func tapSecondary() { onSecondary?() }
  @objc private func tapSkip() { onSkip?() }
}
