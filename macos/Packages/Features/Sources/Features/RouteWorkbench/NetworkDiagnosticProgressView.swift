import AppKit
import PaxCore
import PaxUI

/// The open report owns display timing; saved evidence keeps its own timestamps.
@MainActor final class NetworkDiagnosticProgressView: NSStackView {
  private typealias Theme = PaxTheme.NetworkDiagnostics
  var onResize: (() -> Void)?
  private let symbol = NetworkDiagnosticProgressSymbol()
  private let title = PaxRouteText.label(
    "", font: PaxTheme.Typography.label(size: Theme.progressTitleFont, weight: .medium))
  private let badge = PaxRouteText.label(
    "", font: PaxTheme.Typography.label(size: Theme.progressBadgeFont), secondary: true)
  private let time = PaxRouteText.label("", font: PaxTheme.Typography.time, secondary: true)
  private let note = PaxRouteText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private let countdown = PaxRouteText.label("", font: PaxTheme.Typography.time, secondary: true)
  private let history = PaxRouteText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private let compactText = PaxRouteText.label(
    "", font: PaxTheme.Typography.caption(), secondary: true, wrapping: true)
  private let compactSymbol = NetworkDiagnosticProgressSymbol()
  private let rowStack = NSStackView()
  private let panel = NetworkDiagnosticProgressSurface()
  private let compact = NetworkDiagnosticProgressSurface()
  private var rows: [NetworkDiagnosticProgressRow] = []
  private var activities: [NetworkDiagnosticActivity] = []
  private var startedAt: Date?
  private var endedAt: Date?
  private var rowEnds: [Date?] = []
  private var isRunning = false
  private var collapsed = true
  private var remaining: Int?
  private var reading = false
  private var clockTask: Task<Void, Never>?
  private lazy var collapse = PaxRouteButton(
    context.text("native_diagnostic.afd4b78353"), symbol: "chevron.up", style: .quiet, target: self,
    action: #selector(collapseProgress))
  private lazy var expand = PaxRouteButton(
    context.text("native_diagnostic.4bf0fb2140"), symbol: "chevron.down", style: .quiet,
    target: self,
    action: #selector(expandProgress))

  let context: PresentationContext
  init(context: PresentationContext = .chinese) {
    self.context = context
    super.init(frame: .zero)
    orientation = .vertical
    alignment = .leading
    spacing = PaxTheme.Spacing.xs
    setAccessibilityIdentifier("network-diagnostic-progress")
    countdown.font = .monospacedDigitSystemFont(ofSize: Theme.progressBadgeFont, weight: .regular)
    note.font = PaxTheme.Typography.label(size: Theme.progressBadgeFont)
    collapse.font = PaxTheme.Typography.label(size: Theme.progressBadgeFont)
    collapse.controlSize = .small
    collapse.setAccessibilityIdentifier("diagnostic-progress-collapse")
    let pill = NSView()
    pill.wantsLayer = true
    pill.layer?.backgroundColor = PaxTheme.Routes.selectedBlue.cgColor
    pill.layer?.cornerRadius = PaxTheme.Radius.control / 2
    badge.textColor = PaxTheme.Color.onSolid
    badge.translatesAutoresizingMaskIntoConstraints = false
    pill.addSubview(badge)
    NSLayoutConstraint.activate([
      badge.leadingAnchor.constraint(equalTo: pill.leadingAnchor, constant: PaxTheme.Spacing.tight),
      badge.trailingAnchor.constraint(
        equalTo: pill.trailingAnchor, constant: -PaxTheme.Spacing.tight),
      badge.topAnchor.constraint(equalTo: pill.topAnchor, constant: PaxTheme.Spacing.xs / 2),
      badge.bottomAnchor.constraint(equalTo: pill.bottomAnchor, constant: -PaxTheme.Spacing.xs / 2),
    ])
    let heading = horizontal([symbol, title, pill, NSView(), collapse])
    rowStack.orientation = .vertical
    rowStack.alignment = .leading
    rowStack.spacing = 0
    let metadata = horizontal([NSView(), time])
    metadata.arrangedSubviews.first?.widthAnchor.constraint(
      equalToConstant: Theme.progressIcon
    ).isActive = true
    let foot = horizontal([note, NSView(), countdown])
    panel.setContent(
      vertical([heading, metadata, line(), rowStack, line(), foot]),
      verticalInset: Theme.expandedProgressVerticalInset)
    expand.font = PaxTheme.Typography.label(size: Theme.progressBadgeFont)
    expand.controlSize = .small
    expand.setAccessibilityIdentifier("diagnostic-progress-expand")
    compact.setContent(
      horizontal([compactSymbol, compactText, NSView(), expand]), verticalInset: Theme.progressInset
    )
    compactText.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    compactText.setAccessibilityIdentifier("diagnostic-progress-last-time")
    history.setAccessibilityIdentifier("diagnostic-progress-history-time")
    for view in [panel, compact, history] {
      addArrangedSubview(view)
      view.widthAnchor.constraint(equalTo: widthAnchor).isActive = true
    }
    panel.isHidden = true
    compact.isHidden = true
    history.isHidden = true
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  func apply(_ store: NetworkDiagnosticStore) {
    let busy = store.busy || store.currentEnvironment?.busy == true
    if busy, let start = store.startedAt, startedAt != start {
      startedAt = start
      endedAt = nil
      rowEnds = []
      collapsed = false
      remaining = nil
    }
    activities = store.activities(context: context)
    isRunning = busy
    expand.isHidden = startedAt == nil
    while rows.count < activities.count {
      let row = NetworkDiagnosticProgressRow()
      row.setAccessibilityIdentifier("diagnostic-progress-\(rows.count)")
      rowStack.addArrangedSubview(row)
      row.widthAnchor.constraint(equalTo: rowStack.widthAnchor).isActive = true
      rows.append(row)
    }
    while rowEnds.count < activities.count { rowEnds.append(nil) }
    if startedAt != nil {
      for (index, activity) in activities.enumerated() where activity.settled {
        if rowEnds[index] == nil { rowEnds[index] = Date() }
      }
      if !busy, endedAt == nil, activities.allSatisfy(\.settled) {
        endedAt = Date()
        let hasFailure = activities.contains { $0.state == .failed || $0.state == .partial }
        remaining = hasFailure ? nil : Theme.progressCollapseSeconds
      }
    } else if let at = store.report?.at ?? store.currentEnvironment?.report?.at {
      compactText.stringValue = context.text(
        "native_diagnostic.9bfb31c74f", ["arg0": String(describing: MeasurementTimestamp.text(at))])
      compactSymbol.apply(.idle)
    }
    let hasHistory = store.stale && store.report != nil
    history.isHidden = !hasHistory || !busy && !activities.contains { $0.state == .stopped }
    if hasHistory {
      history.stringValue = context.text(
        "native_diagnostic.098acef136",
        ["arg0": String(describing: MeasurementTimestamp.text(store.report?.at))])
    }
    isHidden = startedAt == nil && store.report == nil && store.currentEnvironment?.report == nil
    render()
    startClockIfNeeded()
  }

  func invalidate() {
    clockTask?.cancel()
    clockTask = nil
    symbol.stop()
    for row in rows { row.stop() }
  }
  override func viewDidMoveToWindow() {
    super.viewDidMoveToWindow()
    if window == nil { invalidate() } else { startClockIfNeeded() }
  }
  override func updateTrackingAreas() {
    super.updateTrackingAreas()
    for area in trackingAreas { removeTrackingArea(area) }
    addTrackingArea(
      NSTrackingArea(
        rect: bounds, options: [.mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect],
        owner: self))
  }
  override func mouseEntered(with event: NSEvent) { reading = true }
  override func mouseExited(with event: NSEvent) { reading = false }

  private func render() {
    let wasExpanded = !panel.isHidden
    panel.isHidden = collapsed || startedAt == nil
    compact.isHidden = !panel.isHidden || isHidden
    if let start = startedAt {
      let stopped = activities.contains { $0.state == .stopped }
      let hasIssues = activities.contains { $0.state == .failed || $0.state == .partial }
      let state: NetworkDiagnosticActivity.State =
        isRunning ? .running : stopped ? .stopped : hasIssues ? .partial : .finished
      symbol.apply(state)
      compactSymbol.apply(state)
      title.stringValue =
        isRunning
        ? context.text("native_diagnostic.2b5941013c")
        : stopped
          ? context.text("native_diagnostic.86ba986bcb")
          : context.text("native_diagnostic.f7bdd9c96d")
      let settled = activities.filter(\.settled).count
      badge.stringValue =
        isRunning
        ? context.text(
          "native_diagnostic.21caad5454",
          ["arg0": String(describing: settled), "arg1": String(describing: activities.count)])
        : stopped
          ? context.text("native_diagnostic.b390ab1f6d")
          : hasIssues
            ? context.text("native_diagnostic.0de8544ea3")
            : context.text(
              "native_diagnostic.fc316655c4", ["arg0": String(describing: activities.count)])
      let elapsed = seconds(until: endedAt ?? Date())
      time.stringValue =
        context.text(
          "native_diagnostic.2dbaf76500",
          [
            "arg0": MeasurementTimestamp.secondsText(start),
            "arg1": isRunning
              ? context.text("native_diagnostic.937683da32")
              : context.text("native_diagnostic.e77e3d58b0"), "arg2": String(elapsed),
          ])
      compactText.stringValue =
        context.text(
          "native_diagnostic.a4a2cbb9d2",
          [
            "arg0": stopped
              ? context.text("native_diagnostic.94ae067ab5")
              : context.text("native_diagnostic.f48ea0733c"),
            "arg1": MeasurementTimestamp.text(start), "arg2": String(elapsed),
          ])
        + (isRunning
          ? context.text("native_diagnostic.20f99990c7")
          : stopped
            ? context.text("native_diagnostic.f006455e3b")
            : hasIssues
              ? context.text("native_diagnostic.3bfb98d16f")
              : context.text("native_diagnostic.f28461bb49"))
      for (index, activity) in activities.enumerated() {
        rows[index].apply(
          activity, elapsed: seconds(until: rowEnds[index] ?? Date()), context: context)
      }
      collapse.isHidden = isRunning
      note.stringValue =
        isRunning
        ? context.text("native_diagnostic.d9cd6f4d5a")
        : stopped
          ? context.text("native_diagnostic.0dda291efb")
          : hasIssues
            ? context.text("diagnostic.progress.incomplete_details")
            : context.text("native_diagnostic.0ff91a01fc")
      countdown.stringValue =
        remaining.map {
          context.text(
            "native_diagnostic.27c1346251",
            [
              "arg0": isReading ? context.text("native_diagnostic.aaa253f901") : "",
              "arg1": String($0),
            ])
        } ?? ""
      countdown.isHidden = remaining == nil
      setAccessibilityLabel(title.stringValue + "，" + badge.stringValue)
    }
    if wasExpanded != !panel.isHidden { onResize?() }
  }
  private func seconds(until date: Date) -> Int {
    guard let startedAt else { return 0 }
    return max(0, Int(date.timeIntervalSince(startedAt)))
  }
  private var isReading: Bool {
    if reading { return true }
    guard let focused = window?.firstResponder as? NSView else { return false }
    return focused === self || focused.isDescendant(of: self)
  }
  private func startClockIfNeeded() {
    guard clockTask == nil, window != nil, isRunning || remaining != nil else { return }
    clockTask = Task { [weak self] in
      while !Task.isCancelled {
        do { try await Task.sleep(for: Theme.progressTick) } catch { return }
        guard let self else { return }
        if let remaining, !isReading { self.remaining = remaining - 1 }
        if let remaining, remaining <= 0 {
          collapsed = true
          self.remaining = nil
        }
        render()
        if !isRunning && remaining == nil {
          clockTask = nil
          return
        }
      }
    }
  }
  @objc private func collapseProgress() {
    collapsed = true
    remaining = nil
    render()
  }
  @objc private func expandProgress() {
    guard startedAt != nil else { return }
    collapsed = false
    remaining = nil
    render()
  }
  private func horizontal(_ views: [NSView]) -> NSStackView {
    let stack = NSStackView(views: views)
    stack.spacing = PaxTheme.Spacing.tight
    stack.alignment = .centerY
    return stack
  }
  private func vertical(_ views: [NSView]) -> NSStackView {
    let stack = NSStackView(views: views)
    stack.orientation = .vertical
    stack.alignment = .leading
    stack.spacing = PaxTheme.Spacing.tight
    for view in views { view.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true }
    return stack
  }
  private func line() -> NSView {
    let view = NSBox()
    view.boxType = .separator
    return view
  }
}

@MainActor private final class NetworkDiagnosticProgressSurface: NSView {
  override init(frame: NSRect) {
    super.init(frame: frame)
    wantsLayer = true
    layer?.cornerRadius = PaxTheme.NetworkDiagnostics.progressRadius
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override var wantsUpdateLayer: Bool { true }
  override func updateLayer() {
    layer?.backgroundColor = PaxTheme.NetworkDiagnostics.progressBackground.cgColor
  }
  func setContent(_ content: NSView, verticalInset: CGFloat) {
    let inset = PaxTheme.NetworkDiagnostics.progressInset
    addSubview(content)
    content.translatesAutoresizingMaskIntoConstraints = false
    NSLayoutConstraint.activate([
      content.topAnchor.constraint(equalTo: topAnchor, constant: verticalInset),
      content.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -verticalInset),
      content.leadingAnchor.constraint(equalTo: leadingAnchor, constant: inset),
      content.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -inset),
    ])
  }
}
