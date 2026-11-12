import AppKit
import PaxCore
import PaxUI

/// Limits section of P07d-ai-session-limits §4.2: title, counts and tabs above the card, then one
/// row per session, model, main-line model or sub-agent. The session list folds after 10 rows.
@MainActor final class AILimitListCard {
  static let foldedRows = 10
  let view = AIUsageLayout.stack([])
  private let readout: AILimitReadout
  private let counts = NSStackView()
  var onSelect: ((String) -> Void)?
  var onKind: ((Int) -> Void)?
  var onToggle: (() -> Void)?
  var onBack: (() -> Void)?
  var onSearch: (() -> Void)?
  let loadingStatus = PaxText.label(
    "正在读取会话…", font: PaxTheme.Typography.caption(), secondary: true)
  private let title = AIUsageLayout.heading("会话开销")
  private let titleArea = NSView()
  private let hint = PaxFeatureTag("点击会话查看详情", tone: .neutral)
  private var fullTitle = ""
  private var titleIdentity = ""
  private var compactTitle: NSLayoutConstraint?
  private var hintConstraints: [NSLayoutConstraint] = []
  private var loading = false
  private lazy var titleHost = AILimitReadoutHost(title, readout: readout) { [weak self] in
    guard let self, selected else { return nil }
    return AILimitReadout.explanation("会话名称", fullTitle + "\n" + titleIdentity)
  }
  private lazy var body = AILimitLoadingSlot(
    card, placeholder: AIUsageLayout.card(NSView()), height: PaxTheme.Layout.tableRow * 3)
  private let search = PaxRouteSearchField()
  private lazy var back = PaxCompactButton(
    "返回", style: .bordered, target: self, action: #selector(goBack))
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

  init(readout: AILimitReadout) {
    self.readout = readout
    let inset = PaxTheme.Spacing.card
    card = PaxCard(insets: .init(top: inset, left: inset, bottom: 0, right: inset))
    counts.spacing = PaxTheme.Spacing.tight
    counts.setAccessibilityIdentifier("ai-session-limits-counts")
    title.lineBreakMode = .byTruncatingTail
    title.maximumNumberOfLines = 1
    title.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
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
    back.setAccessibilityLabel("返回会话开销")
    back.setContentCompressionResistancePriority(.required, for: .horizontal)
    back.setAccessibilityIdentifier("ai-session-limits-back")
    back.isHidden = true
    loadingStatus.isHidden = true
    loadingStatus.setAccessibilityIdentifier("ai-session-loading")
    search.placeholderString = "搜索名称或会话 ID"
    search.setAccessibilityLabel("搜索会话")
    search.setAccessibilityIdentifier("ai-session-list-search")
    search.sendsSearchStringImmediately = true
    search.target = self
    search.action = #selector(searchChanged)
    search.widthAnchor.constraint(equalToConstant: PaxTheme.Layout.searchWidth).isActive = true
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

  func setNavigation(session: String?, identity: String?) {
    if identity != nil, !selected { search.abortEditing() }
    if titleIdentity != (identity ?? "") { readout.close(from: titleHost) }
    selected = identity != nil
    fullTitle = session ?? "会话开销"
    titleIdentity = identity ?? ""
    back.isHidden = !selected
    updateTitleLayout()
    title.stringValue = fullTitle
    titleHost.setAccessibilityHelp(selected ? fullTitle + "\n" + titleIdentity : "会话开销")
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
    if loading { applyCounts(["请求 —", "轮次 —"]) }
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
        ? ["请求 —", "轮次 —"]
        : [
          "请求 \(AIUsageFormatting.count(summary.requests))",
          "轮次 \(summary.turns)" + (summary.turnsComplete ? "" : "*"),
        ])
    counts.toolTip = summary.turnsComplete ? "轮次按原生轮次去重" : "* 部分请求缺少轮次记录，只计已识别的轮次"
    // Tabs are rebuilt only when their titles change, so a click never replaces its own control.
    let counts = scope.lists.map { !loading && $0.kind == .subagents ? $0.rows.count : nil }
    let key = scope.lists.map { $0.kind.title } + counts.map { $0.map(String.init) ?? "" }
    if key != tabsKey {
      tabsKey = key
      for view in switchSlot.arrangedSubviews {
        switchSlot.removeArrangedSubview(view)
        view.removeFromSuperview()
      }
      let tabs = PaxTabs(scope.lists.map { $0.kind.title }, counts: counts)
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
      content.emptyMessage = "没有匹配的会话"
    }
    guard rendered != content || renderedExpanded != expanded else { return false }
    readout.close()
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
      empty.stringValue = content.emptyMessage
      return reconcile([empty])
    }
    let labels = legendLabels(content)
    let key = [content.kind.title] + labels
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
        view = AILimitRowView(row: row, readout: readout)
        view.onSelect = { [weak self] id in self?.onSelect?(id) }
      }
      view.showsDivider = index < shown.count - 1
      retained[row.id] = view
      desired.append(view)
    }
    rowsByID = retained
    if folds {
      let title = expanded ? "收起" : "显示全部 \(content.rows.count) 个"
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
    return uniform ? AILimitStyle.edges(first) : AILimitStyle.bandNames
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
    legend.toolTip = "按输入费用计算占比；各模型的具体阈值见行悬停详情"
    let bars = AIUsageLayout.stack([head("三段比例"), legend, NSView()], vertical: false)
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
          kind == .sessions ? "会话" : kind == .subagents ? "子代理" : "模型",
          width: AILimitRowView.nameWidth),
        bars,
        metricHead(
          "费用倍率", width: AILimitRowView.multipleWidth, text: AILimitStyle.multipleExplanation),
        metricHead("超限增费", width: AILimitRowView.spendWidth, text: AILimitStyle.spendExplanation),
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
