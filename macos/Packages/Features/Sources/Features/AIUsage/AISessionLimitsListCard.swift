import AppKit
import PaxCore
import PaxUI

/// Limits section of P07d-ai-session-limits §4.2: title, counts and tabs above the card, then one
/// row per session, model, main-line model or sub-agent. The session list folds after 10 rows.
@MainActor final class AILimitListCard {
  let interfaceCopy: InterfaceCopyBindings
  var context: PresentationContext { interfaceCopy.context }

  static let foldedRows = 10
  let view = AIUsageLayout.stack([])
  private let readout: AILimitReadout
  private let counts = NSStackView()
  var onSelect: ((String) -> Void)?
  var onKind: ((Int) -> Void)?
  var onToggle: (() -> Void)?
  var onBack: (() -> Void)?
  var onSearch: (() -> Void)?
  lazy var loadingStatus = PaxText.label(
    context.text("session_limits.c4de4ad42c"), font: PaxTheme.Typography.caption(), secondary: true)
  private lazy var title = interfaceCopy.usageHeading("session_limits.8eb6779f0a")
  private let titleArea = NSView()
  private lazy var hint = interfaceCopy.bind(
    PaxFeatureTag(context.text("session_limits.30a95e35ca"), tone: .neutral),
    key: "session_limits.30a95e35ca"
  ) { $0.applyTitle($1) }
  private var fullTitle = ""
  private var titleIdentity = ""
  private var compactTitle: NSLayoutConstraint?
  private var hintConstraints: [NSLayoutConstraint] = []
  private var loading = false
  private lazy var titleHost = PaxHoverReadoutHost(title, readout: readout.hover) { [weak self] in
    guard let self, selected else { return nil }
    return AILimitReadout.explanation(
      context.text("session_limits.e0c412d97f"), fullTitle + "\n" + titleIdentity)
  }
  private lazy var body = AILimitLoadingSlot(
    card, placeholder: AIUsageLayout.card(NSView()), height: PaxTheme.Layout.tableRow * 3)
  private let search = PaxRouteSearchField()
  private lazy var back = PaxCompactButton(
    context.text("session_limits.572cf45ba4"), style: .bordered, target: self,
    action: #selector(goBack))
  private let card: PaxCard
  private var selected = false
  private var kind = 0
  private let switchSlot = NSStackView()
  private let list = NSStackView()
  private var tabs: PaxTabs?
  private var tabsKey: [String] = []
  private var countTexts: [String] = []
  private var rendered: AILimitList?
  private var renderedExpanded = false
  private var rowsByID: [String: AILimitRowView] = [:]
  private var headerKey: [String] = []
  private var columnHeader: NSView?
  private let empty = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private lazy var fold = PaxCompactButton(
    "", style: .text, target: self, action: #selector(toggle))
  private lazy var foldRow = AIUsageLayout.stack([fold, NSView()], vertical: false)

  init(readout: AILimitReadout, interfaceCopy: InterfaceCopyBindings = InterfaceCopyBindings()) {
    self.interfaceCopy = interfaceCopy
    let context = interfaceCopy.context

    self.readout = readout
    let inset = PaxTheme.Spacing.card
    card = PaxCard(insets: .init(top: inset, left: inset, bottom: 0, right: inset))
    counts.spacing = PaxTheme.Spacing.tight
    counts.setAccessibilityIdentifier("ai-session-limits-counts")
    title.lineBreakMode = .byTruncatingTail
    title.maximumNumberOfLines = 1
    title.setContentCompressionResistancePriority(.defaultHigh, for: .horizontal)
    titleHost.translatesAutoresizingMaskIntoConstraints = false
    hint.translatesAutoresizingMaskIntoConstraints = false
    titleArea.addSubview(titleHost)
    titleArea.addSubview(hint)
    NSLayoutConstraint.activate([
      titleArea.heightAnchor.constraint(equalToConstant: PaxTheme.Dashboard.tabHeight),
      titleHost.leadingAnchor.constraint(equalTo: titleArea.leadingAnchor),
      titleHost.centerYAnchor.constraint(equalTo: titleArea.centerYAnchor),
      titleHost.heightAnchor.constraint(equalTo: title.heightAnchor),
      titleHost.trailingAnchor.constraint(lessThanOrEqualTo: titleArea.trailingAnchor),
      hint.centerYAnchor.constraint(equalTo: titleArea.centerYAnchor),
    ])
    compactTitle = titleHost.widthAnchor.constraint(equalTo: titleArea.widthAnchor, multiplier: 0.5)
    hintConstraints = [
      hint.leadingAnchor.constraint(
        equalTo: titleHost.trailingAnchor, constant: PaxTheme.Spacing.tight),
      hint.trailingAnchor.constraint(lessThanOrEqualTo: titleArea.trailingAnchor),
    ]
    NSLayoutConstraint.activate(hintConstraints)
    back.capsule = true
    back.capsuleHeight = PaxTheme.Dashboard.tabHeight
    back.image = Self.backIcon()
    back.setAccessibilityLabel(context.text("session_limits.b53d741c28"))
    back.setContentCompressionResistancePriority(.required, for: .horizontal)
    back.setAccessibilityIdentifier("ai-session-limits-back")
    back.isHidden = true
    loadingStatus.isHidden = true
    loadingStatus.setAccessibilityIdentifier("ai-session-loading")
    search.placeholderString = context.text("session_limits.7da1a64804")
    search.setAccessibilityLabel(context.text("session_limits.feb95554de"))
    search.setAccessibilityIdentifier("ai-session-list-search")
    search.sendsSearchStringImmediately = true
    search.target = self
    search.action = #selector(searchChanged)
    let searchWidth = search.widthAnchor.constraint(equalToConstant: PaxTheme.Layout.searchWidth)
    searchWidth.priority = .defaultLow
    searchWidth.isActive = true
    search.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    let header = AIUsageLayout.stack(
      [back, titleArea, counts, loadingStatus, search, switchSlot], vertical: false)
    // Cross-view constraints require the common ancestor installed by the header stack.
    search.heightAnchor.constraint(equalTo: switchSlot.heightAnchor).isActive = true
    list.orientation = .vertical
    list.alignment = .leading
    list.spacing = 0
    // The final row owns its bottom padding, just as the first row owns its gap below the header.
    card.contentView = list
    card.heightAnchor.constraint(equalTo: list.heightAnchor, constant: inset).isActive = true
    for child in [header, body] {
      view.addArrangedSubview(child)
      child.widthAnchor.constraint(equalTo: view.widthAnchor).isActive = true
    }
    view.setAccessibilityIdentifier("ai-session-limits-list")
  }

  // The shared capsule draws a 17pt image slot; pad a 12pt symbol inside it instead
  // of letting that slot enlarge a narrow chevron across its entire square.
  private static func backIcon() -> NSImage? {
    let size = PaxTheme.Spacing.medium
    guard let symbol = PaxIcon.image("arrow.uturn.backward", size: size) else { return nil }
    let side = PaxTheme.Dashboard.iconGlyph
    let image = NSImage(size: .init(width: side, height: side), flipped: false) { bounds in
      symbol.draw(
        in: .init(
          x: bounds.midX - size / 2, y: bounds.midY - size / 2, width: size, height: size))
      return true
    }
    image.isTemplate = true
    return image
  }

  func invalidateLanguage() {
    rendered = nil
    headerKey = []
    tabsKey = []
    countTexts = []
    loadingStatus.stringValue = context.text("session_limits.c4de4ad42c")
    back.title = context.text("session_limits.572cf45ba4")
    back.setAccessibilityLabel(context.text("session_limits.b53d741c28"))
    search.placeholderString = context.text("session_limits.7da1a64804")
    search.setAccessibilityLabel(context.text("session_limits.feb95554de"))
    for row in rowsByID.values { row.invalidateLanguage() }
  }
  func setNavigation(session: String?, identity: String?) {
    if identity != nil, !selected { search.abortEditing() }
    if titleIdentity != (identity ?? "") { readout.hover.close(from: titleHost) }
    let isDetail = identity != nil
    if selected != isDetail {
      selected = isDetail
      // A detail name must yield before AppKit considers changing the window's size.
      title.setContentCompressionResistancePriority(
        isDetail ? .defaultLow : .defaultHigh, for: .horizontal)
    }
    fullTitle = session ?? context.text("session_limits.8eb6779f0a")
    titleIdentity = identity ?? ""
    back.isHidden = !selected
    updateTitleLayout()
    title.stringValue = fullTitle
    titleHost.setAccessibilityHelp(
      selected ? fullTitle + "\n" + titleIdentity : context.text("session_limits.8eb6779f0a"))
    search.isHidden = selected || kind != 0
    setLoading(loading)
  }

  private func updateTitleLayout() {
    hint.isHidden = selected || kind != 0
    if selected {
      NSLayoutConstraint.deactivate(hintConstraints)
      compactTitle?.isActive = true
    } else {
      compactTitle?.isActive = false
      if kind == 0 {
        NSLayoutConstraint.activate(hintConstraints)
      } else {
        NSLayoutConstraint.deactivate(hintConstraints)
      }
    }
  }

  func setLoading(_ loading: Bool) {
    self.loading = loading
    let count = selected ? 1 : 3
    let rowHeight = PaxTheme.Layout.tableRow + PaxTheme.Spacing.medium * 2
    body.setLoading(
      loading, height: rowHeight * CGFloat(count) + PaxTheme.Spacing.card * 3)
    tabs?.isEnabled = !loading
    if loading {
      applyCounts([
        context.text("session_limits.461c9cbb4c"), context.text("session_limits.7bb49a72b6"),
      ])
    }
  }

  private func applyCounts(_ texts: [String]) {
    guard texts != countTexts else { return }
    countTexts = texts
    for child in counts.arrangedSubviews {
      counts.removeArrangedSubview(child)
      child.removeFromSuperview()
    }
    for (text, tone) in zip(texts, [PaxFeatureTag.Tone.blue, .purple]) {
      counts.addArrangedSubview(PaxFeatureTag(text, tone: tone))
    }
  }

  /// Returns whether the arranged structure changed, requiring scroll-bound reconciliation.
  func apply(_ scope: AILimitScope, kind: Int, expanded: Bool) -> Bool {
    self.kind = kind
    updateTitleLayout()
    search.isHidden = selected || scope.lists[kind].kind != .sessions
    let summary = scope.summary
    applyCounts(
      loading
        ? [context.text("session_limits.461c9cbb4c"), context.text("session_limits.7bb49a72b6")]
        : [
          context.text(
            "session_limits.a6f7359681",
            ["arg0": String(describing: AIUsageFormatting.count(summary.requests))]),
          context.text("session_limits.d58e4678dd", ["arg0": String(describing: summary.turns)])
            + (summary.turnsComplete ? "" : "*"),
        ])
    counts.toolTip =
      summary.turnsComplete
      ? context.text("session_limits.8cc0524ea8") : context.text("session_limits.1f69884863")
    // Tabs are rebuilt only when their titles change, so a click never replaces its own control.
    let counts = scope.lists.map { !loading && $0.kind == .subagents ? $0.rows.count : nil }
    let key =
      scope.lists.map { $0.kind.title(context: context) } + counts.map { $0.map(String.init) ?? "" }
    if key != tabsKey {
      tabsKey = key
      for view in switchSlot.arrangedSubviews {
        switchSlot.removeArrangedSubview(view)
        view.removeFromSuperview()
      }
      let tabs = PaxTabs(scope.lists.map { $0.kind.title(context: context) }, counts: counts)
      tabs.setAccessibilityIdentifier("ai-session-limits-kind")
      tabs.onSelect = { [weak self] index in self?.onKind?(index) }
      switchSlot.addArrangedSubview(tabs)
      self.tabs = tabs
    }
    tabs?.isEnabled = !loading
    tabs?.selectedIndex = kind
    var content = scope.lists[kind]
    let query = search.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
    if content.kind == .sessions, !query.isEmpty {
      content.rows = content.rows.filter {
        $0.title.localizedStandardContains(query)
          || ($0.sessionID ?? $0.id).localizedCaseInsensitiveContains(query)
      }
      content.emptyMessage = context.text("session_limits.43a937d227")
    }
    guard rendered != content || renderedExpanded != expanded else { return false }
    readout.hover.close()
    rendered = content
    renderedExpanded = expanded
    return renderRows(content, expanded: expanded)
  }

  private func renderRows(_ content: AILimitList, expanded: Bool) -> Bool {
    let folds = content.kind == .sessions && content.rows.count > Self.foldedRows
    let bottom = content.rows.isEmpty || folds ? PaxTheme.Spacing.card : 0
    if list.edgeInsets.bottom != bottom { list.edgeInsets.bottom = bottom }
    if content.rows.isEmpty {
      rowsByID = [:]
      empty.stringValue = content.emptyText(context: context)
      return reconcile([empty])
    }
    let labels = legendLabels(content)
    let key = [content.kind.title(context: context)] + labels
    if key != headerKey {
      headerKey = key
      columnHeader = header(kind: content.kind, labels: labels)
    }
    var desired = columnHeader.map { [$0] } ?? []
    let shown = folds && !expanded ? Array(content.rows.prefix(Self.foldedRows)) : content.rows
    var retained: [String: AILimitRowView] = [:]
    for (index, row) in shown.enumerated() {
      let view: AILimitRowView
      if let existing = rowsByID[row.id] {
        view = existing
        view.apply(row)
      } else {
        view = AILimitRowView(row: row, readout: readout, interfaceCopy: interfaceCopy)
        view.onSelect = { [weak self] id in self?.onSelect?(id) }
      }
      view.showsDivider = index < shown.count - 1
      retained[row.id] = view
      desired.append(view)
    }
    rowsByID = retained
    if folds {
      lazy var title =
        expanded
        ? context.text("session_limits.afd4b78353")
        : context.text(
          "session_limits.40ae9eba51", ["arg0": String(describing: content.rows.count)])
      if fold.title != title { fold.title = title }
      fold.trailingGlyph = expanded ? "▴" : "▾"
      fold.setAccessibilityIdentifier("ai-session-limits-fold")
      fold.setAccessibilityLabel(title)
      desired.append(foldRow)
    }
    return reconcile(desired)
  }

  /// Leave existing views and constraints alone unless their membership/order changed.
  private func reconcile(_ desired: [NSView]) -> Bool {
    guard list.arrangedSubviews != desired else { return false }
    let retained = Set(desired.map(ObjectIdentifier.init))
    for child in list.arrangedSubviews where !retained.contains(ObjectIdentifier(child)) {
      list.removeArrangedSubview(child)
      child.removeFromSuperview()
    }
    for (index, child) in desired.enumerated() {
      if list.arrangedSubviews.indices.contains(index), list.arrangedSubviews[index] === child {
        continue
      }
      if child.superview != nil {
        list.removeArrangedSubview(child)
        child.removeFromSuperview()
      }
      list.insertArrangedSubview(child, at: index)
      child.widthAnchor.constraint(equalTo: list.widthAnchor).isActive = true
    }
    return true
  }

  private func legendLabels(_ content: AILimitList) -> [String] {
    let configured = content.rows.flatMap { row in
      row.modelLines.isEmpty ? [row.lines] : row.modelLines.map(\.limit)
    }
    let first = configured.first ?? nil
    let uniform =
      first != nil
      && configured.allSatisfy {
        $0?.warn == first?.warn && $0?.limit == first?.limit
      }
    return uniform
      ? AILimitStyle.edges(first, context: context) : AILimitStyle.bandNames(context: context)
  }

  /// Column heads share the widths of `AILimitRowView`; the bar column takes the rest.
  private func header(kind: AILimitListKind, labels: [String]) -> NSView {
    func head(_ text: String, width: CGFloat? = nil, right: Bool = false) -> NSTextField {
      let label = PaxText.label(text, font: PaxTheme.Typography.caption(), secondary: true)
      label.alignment = right ? .right : .left
      if let width { label.widthAnchor.constraint(equalToConstant: width).isActive = true }
      return label
    }
    let legend = PaxSeriesLegend()
    legend.items = labels.enumerated().map {
      .init($0.element, color: AILimitStyle.bandColors[$0.offset])
    }
    legend.toolTip = context.text("session_limits.83e7c863b6")
    let bars = AIUsageLayout.stack(
      [head(context.text("session_limits.e5a1e13b87")), legend, NSView()], vertical: false)
    bars.spacing = PaxTheme.Spacing.tight
    bars.distribution = .fill
    bars.setContentHuggingPriority(.init(1), for: .horizontal)
    bars.setContentCompressionResistancePriority(.init(1), for: .horizontal)
    let spacer = NSView()
    spacer.widthAnchor.constraint(equalToConstant: AILimitRowView.iconWidth).isActive = true
    let stack = AIUsageLayout.stack(
      [
        spacer,
        head(
          kind == .sessions
            ? context.text("session_limits.a63280253f")
            : kind == .subagents
              ? context.text("session_limits.acd37b4c79")
              : context.text("session_limits.c98e118e0a"),
          width: AILimitRowView.nameWidth),
        bars,
        metricHead(
          context.text("session_limits.76c84b1e3a"), width: AILimitRowView.multipleWidth,
          text: AILimitStyle.multipleExplanation(context: context)),
        metricHead(
          context.text("session_limits.2f7cdaa366"), width: AILimitRowView.spendWidth,
          text: AILimitStyle.spendExplanation(context: context)),
      ], vertical: false)
    stack.spacing = AILimitRowView.spacing
    stack.distribution = .fill
    let holder = NSView()
    stack.translatesAutoresizingMaskIntoConstraints = false
    holder.addSubview(stack)
    let line = AIUsageLayout.separator()
    line.translatesAutoresizingMaskIntoConstraints = false
    holder.addSubview(line)
    NSLayoutConstraint.activate([
      stack.leadingAnchor.constraint(equalTo: holder.leadingAnchor, constant: AILimitRowView.inset),
      stack.trailingAnchor.constraint(
        equalTo: holder.trailingAnchor, constant: -AILimitRowView.inset),
      stack.topAnchor.constraint(equalTo: holder.topAnchor),
      stack.bottomAnchor.constraint(equalTo: line.topAnchor, constant: -PaxTheme.Spacing.tight),
      line.leadingAnchor.constraint(equalTo: holder.leadingAnchor),
      line.trailingAnchor.constraint(equalTo: holder.trailingAnchor),
      line.bottomAnchor.constraint(equalTo: holder.bottomAnchor),
    ])
    return holder
  }

  private func metricHead(_ title: String, width: CGFloat, text: String) -> NSView {
    let row = AIUsageLayout.stack(
      [
        PaxText.label(title, font: PaxTheme.Typography.caption(), secondary: true),
        readout.help(title, text),
      ], vertical: false)
    row.spacing = PaxTheme.Spacing.xs
    let holder = NSView()
    row.translatesAutoresizingMaskIntoConstraints = false
    holder.addSubview(row)
    NSLayoutConstraint.activate([
      holder.widthAnchor.constraint(equalToConstant: width),
      row.centerXAnchor.constraint(equalTo: holder.centerXAnchor),
      row.topAnchor.constraint(equalTo: holder.topAnchor),
      row.bottomAnchor.constraint(equalTo: holder.bottomAnchor),
    ])
    return holder
  }

  @objc private func toggle() { onToggle?() }
  @objc private func goBack() { onBack?() }
  @objc private func searchChanged() { onSearch?() }
}
