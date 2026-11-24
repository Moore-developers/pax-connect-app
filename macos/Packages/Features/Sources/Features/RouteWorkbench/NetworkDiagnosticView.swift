import AppKit
import PaxCore
import PaxUI

@MainActor final class NetworkDiagnosticView: NSStackView {
  typealias P = NetworkDiagnosticPresentation
  let context: PresentationContext
  let store: NetworkDiagnosticStore
  var onResize: (() -> Void)?
  private var callbacks: [() -> Void] = []
  private var panelCallbackStart = 0
  private lazy var topicPanel = vertical([], spacing: 0)
  private lazy var tabs: PaxTabs = {
    let control = PaxTabs(DiagnosticTopic.allCases.map { context.text($0.titleKey) })
    control.setAccessibilityIdentifier("diagnostic-topic-tabs")
    control.onSelect = { [weak self] index in
      guard let self, store.topic != index else { return }
      store.topic = index
      refreshTopic()
      onResize?()
    }
    return control
  }()
  private var signature = ""
  var report: NetworkDiagnosticReport? { store.report }
  var intelligenceReport: NetworkDiagnosticReport? {
    store.intelligenceReport?.excludingProviderEvidence("ipx.al")
  }
  var groups: [String: [NetworkDiagnosticFact]] {
    Dictionary(grouping: intelligenceReport?.facts ?? [], by: \.group)
  }

  init(
    store: NetworkDiagnosticStore,
    context: PresentationContext = .chinese
  ) {
    self.context = context
    self.store = store
    super.init(frame: .zero)
    orientation = .vertical
    alignment = .leading
    spacing = PaxTheme.Spacing.medium
    setAccessibilityIdentifier("network-diagnostic-workbench")
    setAccessibilityElement(true)
    setAccessibilityRole(.group)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  func apply() {
    let environment = store.currentEnvironment
    let viewStamp =
      "\(environment?.revision ?? 0)|\(environment?.busy == true)|\(environment?.checkingClaudeKeychain == true)|\(environment?.stale == true)|\(store.generation)|\(store.busy)|\(store.stale)|\(store.expanded.sorted())|\(report?.at.timeIntervalSince1970 ?? 0)|\(store.intelligenceReport?.at.timeIntervalSince1970 ?? 0)"
    guard signature != viewStamp else { return }
    signature = viewStamp
    callbacks = []
    for view in arrangedSubviews {
      removeArrangedSubview(view)
      view.removeFromSuperview()
    }
    showEnvironmentBlock()
    append(card(claudeContent()))
    panelCallbackStart = callbacks.count
    tabs.selectedIndex = store.topic
    append(
      card(vertical([horizontal([tabs, NSView()]), topicPanel], spacing: PaxTheme.Spacing.medium)))
    refreshTopic()
    onResize?()
  }
  private func refreshTopic() {
    callbacks.removeSubrange(panelCallbackStart...)
    for view in topicPanel.arrangedSubviews {
      topicPanel.removeArrangedSubview(view)
      view.removeFromSuperview()
    }
    let panel = topicContent(DiagnosticTopic(rawValue: store.topic) ?? .browser)
    topicPanel.addArrangedSubview(panel)
    panel.widthAnchor.constraint(equalTo: topicPanel.widthAnchor).isActive = true
  }
  func text(_ value: String, secondary: Bool = false) -> NSTextField {
    let field = PaxRouteText.label(
      value,
      font: secondary
        ? PaxTheme.Typography.caption() : PaxTheme.Typography.label(size: PaxTheme.Routes.bodyFont),
      secondary: secondary, wrapping: true)
    field.maximumNumberOfLines = 0
    field.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    return field
  }
  func action(_ title: String, quiet: Bool = true, _ perform: @escaping () -> Void)
    -> PaxRouteButton
  {
    let button = PaxRouteButton(
      title, style: quiet ? .quiet : .secondary, target: self, action: #selector(activate(_:)))
    button.tag = callbacks.count
    button.setContentHuggingPriority(.required, for: .horizontal)
    callbacks.append(perform)
    return button
  }
  @objc private func activate(_ sender: NSButton) {
    guard callbacks.indices.contains(sender.tag) else { return }
    callbacks[sender.tag]()
  }
  func vertical(_ views: [NSView], spacing: CGFloat = PaxTheme.Spacing.tight) -> NSStackView {
    let stack = NSStackView(views: views)
    stack.orientation = .vertical
    stack.distribution = .fill
    stack.alignment = .leading
    stack.spacing = spacing
    for view in views { view.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true }
    return stack
  }
  func horizontal(_ views: [NSView], spacing: CGFloat = PaxTheme.Spacing.medium) -> NSStackView {
    let stack = NSStackView(views: views)
    stack.orientation = .horizontal
    stack.distribution = .fill
    stack.alignment = .centerY
    stack.spacing = spacing
    return stack
  }
  func divider() -> NSView {
    let view = NSView()
    view.wantsLayer = true
    view.layer?.backgroundColor = PaxTheme.DashboardColor.divider.cgColor
    view.heightAnchor.constraint(equalToConstant: PaxTheme.Layout.cardBorder).isActive = true
    return view
  }
  func card(_ content: NSView, style: PaxCard.Style = .standard) -> NSView {
    let inset = style == .routesList ? PaxTheme.Table.inset : PaxTheme.Spacing.regular
    let verticalInset = style == .routesList ? PaxTheme.Layout.cardBorder : inset
    let insets = NSEdgeInsets(top: verticalInset, left: inset, bottom: verticalInset, right: inset)
    let card = PaxCard(insets: insets, style: style)
    card.contentView = content
    card.heightAnchor.constraint(equalTo: content.heightAnchor, constant: verticalInset * 2)
      .isActive = true
    card.setAccessibilityElement(true)
    card.setAccessibilityRole(.group)
    card.setAccessibilityChildren([content])
    return card
  }
  func cardHeading(_ key: String, symbol: String, count: Int? = nil) -> NSView {
    let heading = PaxCardHeading(
      context.text(key), symbol: symbol,
      trailing: count.map { context.text("diagnostic.overview.items", ["count": String($0)]) } ?? ""
    )
    heading.heightAnchor.constraint(equalToConstant: PaxTheme.Dashboard.icon).isActive = true
    return heading
  }
  func append(_ view: NSView) {
    addArrangedSubview(view)
    view.widthAnchor.constraint(equalTo: widthAnchor).isActive = true
  }
  func padded(_ view: NSView, vertical inset: CGFloat) -> NSView {
    padded(view, top: inset, bottom: inset)
  }
  func padded(_ view: NSView, top: CGFloat, bottom: CGFloat) -> NSView {
    let holder = NSView()
    view.translatesAutoresizingMaskIntoConstraints = false
    holder.addSubview(view)
    NSLayoutConstraint.activate([
      view.leadingAnchor.constraint(equalTo: holder.leadingAnchor),
      view.trailingAnchor.constraint(equalTo: holder.trailingAnchor),
      view.topAnchor.constraint(equalTo: holder.topAnchor, constant: top),
      view.bottomAnchor.constraint(equalTo: holder.bottomAnchor, constant: -bottom),
    ])
    return holder
  }
  func heading(_ title: String) -> NSView {
    let label = text(title)
    label.font = PaxTheme.Typography.label(weight: .medium)
    return padded(label, vertical: PaxTheme.Spacing.tight)
  }
}
