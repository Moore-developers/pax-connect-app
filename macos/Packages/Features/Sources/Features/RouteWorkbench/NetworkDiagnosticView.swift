import AppKit
import PaxCore
import PaxUI

@MainActor final class NetworkDiagnosticView: NSStackView {
  typealias P = NetworkDiagnosticPresentation
  let store: NetworkDiagnosticStore
  weak var connection: ConnectionStore?
  var onResize: (() -> Void)?
  private var callbacks: [() -> Void] = []
  private var signature = ""
  var report: NetworkDiagnosticReport? { store.report }
  var intelligenceReport: NetworkDiagnosticReport? { store.intelligenceReport }
  var groups: [String: [NetworkDiagnosticFact]] {
    Dictionary(grouping: intelligenceReport?.facts ?? [], by: \.group)
  }

  init(store: NetworkDiagnosticStore, connection: ConnectionStore?) {
    self.store = store
    self.connection = connection
    super.init(frame: .zero)
    orientation = .vertical
    alignment = .leading
    spacing = PaxTheme.NetworkDiagnostics.cardGap
    setAccessibilityIdentifier("network-diagnostic-workbench")
    setAccessibilityElement(true)
    setAccessibilityRole(.group)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  func apply() {
    let route = report.flatMap { value in
      connection?.routesStore?.routes.first { $0.id == value.targetRouteID }
    }
    let stamp =
      "\(store.currentEnvironment?.revision ?? 0)|\(store.generation)|\(store.busy)|\(store.message)|\(store.stale)|\(store.onlyDifferences)|\(store.selectedSource ?? -1)|\(store.preview)|\(store.expanded.sorted())|\(report?.at.timeIntervalSince1970 ?? 0)|\(store.services?.at.timeIntervalSince1970 ?? 0)|\(route?.downloadSummary?.at.timeIntervalSince1970 ?? 0)|\(connection?.routesStore?.downloadOperationID ?? "")|\(connection?.trafficPathStore?.report?.at.timeIntervalSince1970 ?? 0)|\(connection?.routesStore?.history[route?.id ?? ""]?.count ?? 0)"
    guard signature != stamp else { return }
    signature = stamp
    callbacks = []
    for view in arrangedSubviews {
      removeArrangedSubview(view)
      view.removeFromSuperview()
    }
    if store.preview {
      showPreview()
    } else {
      showEnvironmentBlock()
      showIPBlock()
    }
    onResize?()
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
    view.layer?.backgroundColor = PaxTheme.Routes.line.cgColor
    view.heightAnchor.constraint(equalToConstant: PaxTheme.Layout.cardBorder).isActive = true
    return view
  }
  func card(_ content: NSView, summary: Bool = false, flexibleHeight: Bool = false) -> NSView {
    let insets =
      summary
      ? PaxTheme.NetworkDiagnostics.summaryCardInsets
      : PaxTheme.NetworkDiagnostics.environmentCardInsets
    let card = PaxCard(insets: insets, style: .routesSection)
    card.setAccessibilityElement(true)
    card.setAccessibilityRole(.group)
    card.setAccessibilityChildren([content])
    card.contentView = content
    let height =
      flexibleHeight
      ? card.heightAnchor.constraint(
        greaterThanOrEqualTo: content.heightAnchor, constant: insets.top + insets.bottom)
      : card.heightAnchor.constraint(
        equalTo: content.heightAnchor, constant: insets.top + insets.bottom)
    height.isActive = true
    return card
  }
  func append(_ view: NSView) {
    addArrangedSubview(view)
    view.widthAnchor.constraint(equalTo: widthAnchor).isActive = true
  }
  func section(_ title: String, detail: String = "", views: [NSView], accessory: NSView? = nil) {
    let heading = text(title)
    heading.font = PaxTheme.Typography.label(size: PaxTheme.Routes.nameFont, weight: .medium)
    let headingRow = horizontal([heading, NSView()] + (accessory.map { [$0] } ?? []))
    let titleBlock = vertical(
      [headingRow] + (detail.isEmpty ? [] : [text(detail, secondary: true)]))
    append(card(vertical([titleBlock] + views, spacing: PaxTheme.Spacing.medium)))
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
  func heading(_ title: String, cardTitle: Bool = false) -> NSView {
    let label = text(title)
    label.font = PaxTheme.Typography.label(weight: .medium)
    return padded(
      label, top: cardTitle ? PaxTheme.Spacing.tight / 2 : PaxTheme.Spacing.tight,
      bottom: PaxTheme.Spacing.tight)
  }
  func twoColumns(
    _ left: NSView, _ right: NSView, ratio: CGFloat,
    spacing: CGFloat = PaxTheme.NetworkDiagnostics.cardGap
  ) -> NSView {
    let columns = horizontal([left, right], spacing: spacing)
    columns.alignment = .top
    left.widthAnchor.constraint(equalTo: right.widthAnchor, multiplier: ratio / (1 - ratio))
      .isActive = true
    return columns
  }
  func showPreview() {
    let field = text(reportText())
    field.isSelectable = true
    field.setAccessibilityIdentifier("network-diagnostic-report-preview")
    section(
      "报告预览", detail: "默认脱敏；包含系统语言、时区、已取得的证据及未测项目。",
      views: [field],
      accessory: action("返回诊断", quiet: false) { [weak self] in self?.store.preview = false })
  }
}
