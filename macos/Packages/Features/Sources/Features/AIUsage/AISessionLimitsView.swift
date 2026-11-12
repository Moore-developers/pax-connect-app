import AppKit
import PaxUI

/// The page owns list reading state; the Store owns the selected session and its data.
@MainActor final class AISessionLimitsView: NSView {
  private(set) var model: AISessionLimitsModel
  let options: AISessionOptions
  private var selection: String?
  private var listKind = 0
  private var detailKind = 0
  private var expanded = false
  private var listOrigin: NSPoint?
  private var pendingOrigin: NSPoint?
  private var loading = false
  private var renderedSummary: AILimitSummary?
  private var renderedTrend: AILimitTrend?
  var onSelect: ((String?) -> Void)?
  var loadingStatus: NSTextField { list.loadingStatus }
  private let summary = AILimitSummaryStrip()
  private let list: AILimitListCard
  private let trend: AILimitTrendCard
  private lazy var summarySlot = AILimitLoadingSlot(
    summary.view, placeholder: AILimitLoadingSlot.summary(), height: AILimitKpiCard.height)
  private lazy var trendSlot = AILimitLoadingSlot(
    trend.view, placeholder: AILimitLoadingSlot.trend(), height: trend.view.fittingSize.height)

  init(model: AISessionLimitsModel = AISessionLimitsFixture.model, options: AISessionOptions) {
    list = AILimitListCard(readout: summary.readout)
    self.model = model
    self.options = options
    trend = AILimitTrendCard(options: options)
    super.init(frame: .zero)
    setAccessibilityIdentifier("ai-session-limits")
    list.onSelect = { [weak self] id in self?.choose(id) }
    list.onBack = { [weak self] in self?.choose(nil) }
    list.onSearch = { [weak self] in self?.refresh() }
    list.onKind = { [weak self] index in
      guard let self else { return }
      if selection == nil { listKind = index } else { detailKind = index }
      refresh()
    }
    list.onToggle = { [weak self] in
      self?.expanded.toggle()
      self?.refresh()
    }
    let body = AIUsageLayout.stack([summarySlot, list.view, trendSlot])
    body.spacing = PaxTheme.Spacing.regular
    body.translatesAutoresizingMaskIntoConstraints = false
    addSubview(body)
    NSLayoutConstraint.activate([
      body.leadingAnchor.constraint(equalTo: leadingAnchor),
      body.trailingAnchor.constraint(equalTo: trailingAnchor),
      body.topAnchor.constraint(equalTo: topAnchor),
      body.bottomAnchor.constraint(equalTo: bottomAnchor),
    ])
    refresh()
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  private func choose(_ id: String?) {
    guard selection != id else { return }
    if selection == nil { listOrigin = enclosingScrollView?.contentView.bounds.origin }
    if let onSelect {
      onSelect(id)
    } else {
      changeSelection(id)
      refresh()
    }
  }

  private func changeSelection(_ id: String?) {
    guard selection != id else { return }
    if selection == nil, listOrigin == nil {
      listOrigin = enclosingScrollView?.contentView.bounds.origin
    }
    selection = id
    detailKind = 0
    pendingOrigin = id == nil ? listOrigin ?? .zero : .zero
  }

  /// A different date range has its own reading position; search and list tabs remain selected.
  func resetListPosition() {
    listOrigin = nil
    pendingOrigin = .zero
  }

  func refresh() {
    let scroll = enclosingScrollView
    let origin = scroll?.contentView.bounds.origin
    let head = selection.flatMap { id in model.sessions.first { $0.id == id } }
    list.setNavigation(session: head?.title, identity: selection)
    let scope = selection.flatMap { model.single[$0] } ?? model.all
    if !loading, renderedSummary != scope.summary {
      renderedSummary = scope.summary
      summary.apply(scope.summary)
    }
    let kind = selection == nil ? listKind : detailKind
    let structureChanged = list.apply(
      scope, kind: min(kind, scope.lists.count - 1), expanded: selection == nil && expanded)
    let trendHidden = selection == nil || (!loading && scope.trend == nil)
    let visibilityChanged = trendSlot.isHidden != trendHidden
    trendSlot.isHidden = trendHidden
    if !loading, renderedTrend != scope.trend {
      renderedTrend = scope.trend
      if let value = scope.trend { trend.apply(value) }
    }
    // Reserved loading sections are stable enough to position immediately on entry.
    // Ordinary value refreshes keep the existing hierarchy and reading position.
    if structureChanged || visibilityChanged || pendingOrigin != nil,
      let scroll, let target = pendingOrigin ?? origin
    {
      scroll.documentView?.layoutSubtreeIfNeeded()
      let clip = scroll.contentView
      let proposed = NSRect(origin: target, size: clip.bounds.size)
      clip.scroll(to: clip.constrainBoundsRect(proposed).origin)
      scroll.reflectScrolledClipView(clip)
      pendingOrigin = nil
    }
  }

  func apply(model: AISessionLimitsModel, selection: String?) {
    self.model = model
    changeSelection(selection)
    refresh()
  }

  /// Keep each section in place while masking values from the previous session.
  func setLoading(_ loading: Bool) {
    self.loading = loading
    summarySlot.setLoading(loading)
    list.setLoading(loading)
    trendSlot.setLoading(loading)
  }
}
