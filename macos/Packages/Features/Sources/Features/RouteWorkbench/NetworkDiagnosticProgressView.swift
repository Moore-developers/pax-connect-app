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
    "收起", symbol: "chevron.up", style: .quiet, target: self, action: #selector(collapseProgress))
  private lazy var expand = PaxRouteButton(
    "展开详情", symbol: "chevron.down", style: .quiet, target: self,
    action: #selector(expandProgress))

  init() {
    super.init(frame: .zero)
    orientation = .vertical
    alignment = .leading
    spacing = PaxTheme.Spacing.tight
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
    panel.setContent(vertical([heading, metadata, line(), rowStack, line(), foot]))
    expand.font = PaxTheme.Typography.label(size: Theme.progressBadgeFont)
    expand.controlSize = .small
    expand.setAccessibilityIdentifier("diagnostic-progress-expand")
    compact.setContent(horizontal([compactSymbol, compactText, NSView(), expand]))
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
    activities = store.activities
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
      compactText.stringValue = "上次检测：\(MeasurementTimestamp.text(at))"
      compactSymbol.apply(.idle)
    }
    let hasHistory = store.stale && store.report != nil
    history.isHidden = !hasHistory || !busy && !activities.contains { $0.state == .stopped }
    if hasHistory {
      history.stringValue = "上次有效结果 · 检测于 \(MeasurementTimestamp.text(store.report?.at))"
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
      let incomplete = activities.contains { $0.state == .failed || $0.state == .partial }
      let state: NetworkDiagnosticActivity.State =
        isRunning ? .running : incomplete ? .partial : stopped ? .stopped : .finished
      symbol.apply(state)
      compactSymbol.apply(state)
      title.stringValue =
        isRunning ? "正在检测" : incomplete ? "检测结束，部分未完成" : stopped ? "检测已停止" : "检测已完成"
      let settled = activities.filter(\.settled).count
      badge.stringValue =
        isRunning
        ? "已结束 \(settled)/\(activities.count) 路"
        : incomplete ? "查看未完成项" : stopped ? "结果可继续查看" : "\(activities.count) 路均已完成"
      let elapsed = seconds(until: endedAt ?? Date())
      time.stringValue =
        "开始于 \(MeasurementTimestamp.secondsText(start)) · \(isRunning ? "已用" : "耗时") \(elapsed) 秒"
      compactText.stringValue =
        "\(stopped ? "上次尝试" : "上次检测")：\(MeasurementTimestamp.text(start)) · 耗时 \(elapsed) 秒 · "
        + (isRunning ? "检测中" : incomplete ? "部分未完成" : stopped ? "已停止" : "已完成")
      for (index, activity) in activities.enumerated() {
        rows[index].apply(activity, elapsed: seconds(until: rowEnds[index] ?? Date()))
      }
      collapse.isHidden = isRunning
      note.stringValue =
        isRunning
        ? "三路并行检测，结果会陆续更新"
        : incomplete
          ? "未完成原因保留在下方报告中"
          : stopped ? "已完成的结果可继续查看" : "结果已更新，完整报告保留在下方"
      countdown.stringValue = remaining.map { "\(isReading ? "阅读中，暂停收起 · " : "")\($0) 秒后收起" } ?? ""
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
  func setContent(_ content: NSView) {
    let inset = PaxTheme.NetworkDiagnostics.progressInset
    addSubview(content)
    content.translatesAutoresizingMaskIntoConstraints = false
    NSLayoutConstraint.activate([
      content.topAnchor.constraint(equalTo: topAnchor, constant: inset),
      content.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -inset),
      content.leadingAnchor.constraint(equalTo: leadingAnchor, constant: inset),
      content.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -inset),
    ])
  }
}
