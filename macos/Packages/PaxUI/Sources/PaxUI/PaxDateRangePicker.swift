import AppKit

/// 通用日期选择器：点一次选单日，继续点选组成区间；页面经 `PaxDateRangeOptions` 决定预设、上下界与最长跨度，
/// 组件只负责选择与校验，不含页面规则。`onApply` 返回错误文案时留在弹层内显示。
@MainActor
public final class PaxDateRangePicker: NSViewController {
  public struct Range {
    public let start: Date
    public let end: Date
    public init(start: Date, end: Date) {
      self.start = start
      self.end = end
    }
  }

  public var onApply: ((Range) -> String?)?
  public var onCancel: (() -> Void)?

  private let calendar: Calendar
  private let now: Date
  private let options: PaxDateRangeOptions
  private var selection: PaxDateSelection
  private var hover: Date?
  private var leftMonth: Date
  private var months: [PaxCalendarMonthView] = []

  private let monthHost = NSStackView()
  private let titles = [PaxText.label(""), PaxText.label("")]
  private let errorLabel = PaxText.label("", font: PaxTheme.Typography.caption())
  private lazy var apply = PaxCompactButton(
    "应用", style: .primary, target: self, action: #selector(applyTapped))
  private var presetButtons: [(PaxDatePreset, PaxCompactButton)] = []

  public init(
    start: Date, end: Date, calendar: Calendar = .current, now: Date = Date(),
    options: PaxDateRangeOptions = .init()
  ) {
    self.calendar = calendar
    self.now = now
    self.options = options
    selection = PaxDateSelection(start: start, end: end, calendar: calendar, options: options)
    leftMonth = Self.leftMonth(showing: selection, calendar: calendar)
    super.init(nibName: nil, bundle: nil)
  }

  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }

  public override func loadView() {
    errorLabel.textColor = PaxTheme.Color.error
    errorLabel.isHidden = true
    monthHost.orientation = .horizontal
    monthHost.alignment = .top
    monthHost.spacing = PaxTheme.DateRange.monthGap
    monthHost.distribution = .fillEqually

    let footer = NSStackView(views: [presetRow(), NSView(), actionRow()])
    footer.orientation = .horizontal
    footer.alignment = .centerY
    footer.spacing = PaxTheme.Spacing.tight

    let spanHint = options.maxSpanDays.map { "（含首尾最多\($0)天）" } ?? ""
    let hint = PaxText.label(
      "点选一天后可直接应用，或再选一天组成区间" + spanHint, font: PaxTheme.Typography.caption(), secondary: true)
    let content = NSStackView(views: [monthHost, hint, errorLabel, footer])
    content.orientation = .vertical
    content.alignment = .leading
    content.spacing = PaxTheme.Spacing.regular
    for row in [monthHost, footer] {
      row.widthAnchor.constraint(equalTo: content.widthAnchor).isActive = true
    }

    let card = PaxCard(insets: NSEdgeInsets(top: 16, left: 16, bottom: 16, right: 16))
    card.contentView = content
    card.translatesAutoresizingMaskIntoConstraints = false
    view = card
    rebuildMonths()
    let inset = PaxTheme.Spacing.regular * 2
    preferredContentSize = NSSize(
      width: PaxCalendarMonthView.cellSize.width * 14 + PaxTheme.DateRange.monthGap + inset,
      height: card.fittingSize.height)
  }

  // MARK: Layout pieces

  private func presetRow() -> NSView {
    let row = NSStackView()
    row.orientation = .horizontal
    row.alignment = .centerY
    row.spacing = PaxTheme.Spacing.xs
    for (index, preset) in options.presets.enumerated() {
      let button = PaxCompactButton(
        preset.title, style: .text, target: self, action: #selector(presetTapped(_:)))
      button.tag = index
      button.setAccessibilityLabel("选择" + preset.title)
      presetButtons.append((preset, button))
      row.addArrangedSubview(button)
    }
    return row
  }

  private func actionRow() -> NSView {
    let cancel = PaxCompactButton("取消", style: .text, target: self, action: #selector(cancelTapped))
    let row = NSStackView(views: [cancel, apply])
    row.orientation = .horizontal
    row.alignment = .centerY
    row.spacing = PaxTheme.Spacing.tight
    return row
  }

  private func navButtons(_ symbols: [(String, Int)]) -> NSStackView {
    let buttons = symbols.map { symbol, step -> NSButton in
      let button = NSButton(
        image: NSImage(systemSymbolName: symbol, accessibilityDescription: nil) ?? NSImage(),
        target: self, action: #selector(navTapped(_:)))
      button.isBordered = false
      button.contentTintColor = PaxTheme.Color.textSecondary
      button.tag = step
      button.setAccessibilityLabel(
        abs(step) == 12 ? (step < 0 ? "上一年" : "下一年") : (step < 0 ? "上个月" : "下个月"))
      return button
    }
    let row = NSStackView(views: buttons)
    row.spacing = PaxTheme.Spacing.xs
    return row
  }

  private func monthColumn(_ month: PaxCalendarMonthView, title: NSTextField, left: Bool)
    -> NSView
  {
    title.stringValue =
      "\(calendar.component(.year, from: month.month))年 "
      + "\(calendar.component(.month, from: month.month))月"
    title.font = PaxTheme.Typography.label(weight: .semibold)
    let nav =
      left
      ? navButtons([("chevron.left.2", -12), ("chevron.left", -1)])
      : navButtons([("chevron.right", 1), ("chevron.right.2", 12)])
    let bar = NSView()
    bar.heightAnchor.constraint(equalToConstant: PaxTheme.Control.height).isActive = true
    for item in [title, nav] {
      item.translatesAutoresizingMaskIntoConstraints = false
      bar.addSubview(item)
    }
    NSLayoutConstraint.activate([
      title.centerXAnchor.constraint(equalTo: bar.centerXAnchor),
      title.centerYAnchor.constraint(equalTo: bar.centerYAnchor),
      nav.centerYAnchor.constraint(equalTo: bar.centerYAnchor),
      left
        ? nav.leadingAnchor.constraint(equalTo: bar.leadingAnchor)
        : nav.trailingAnchor.constraint(equalTo: bar.trailingAnchor),
    ])
    let column = NSStackView(views: [bar, month])
    column.orientation = .vertical
    column.alignment = .leading
    column.spacing = PaxTheme.Spacing.xs
    bar.widthAnchor.constraint(equalTo: column.widthAnchor).isActive = true
    return column
  }

  // MARK: State

  private static func leftMonth(showing selection: PaxDateSelection, calendar: Calendar) -> Date {
    func first(_ date: Date) -> Date { calendar.dateInterval(of: .month, for: date)?.start ?? date }
    guard let start = selection.start else { return first(Date()) }
    guard let end = selection.end, let before = calendar.date(byAdding: .month, value: -1, to: end)
    else { return first(start) }
    return max(first(start), first(before))
  }

  private func rebuildMonths() {
    for item in monthHost.arrangedSubviews { item.removeFromSuperview() }
    let right = calendar.date(byAdding: .month, value: 1, to: leftMonth) ?? leftMonth
    months = [leftMonth, right].map { PaxCalendarMonthView(month: $0, calendar: calendar) }
    for (index, month) in months.enumerated() {
      month.onTap = { [weak self] in self?.tap($0) }
      month.onHover = { [weak self] in
        self?.hover = $0
        self?.refresh()
      }
      monthHost.addArrangedSubview(monthColumn(month, title: titles[index], left: index == 0))
    }
    refresh()
  }

  private func refresh() {
    let range = selection.visibleRange(hover: hover)
    let today = calendar.startOfDay(for: now)
    for month in months {
      month.stateOf = { [selection] day in
        let day = selection.calendar.startOfDay(for: day)
        return PaxCalendarDayState(
          isEdge: day == range?.lowerBound || day == range?.upperBound,
          isInRange: range.map { $0.contains(day) } ?? false, isToday: day == today,
          isEnabled: selection.isEnabled(day))
      }
    }
    apply.isEnabled = selection.canApply
    let matched = selection.matchingPreset(now: now)
    for (preset, button) in presetButtons {
      button.compactStyle = preset == matched ? .selected : .text
    }
  }

  private func tap(_ day: Date) {
    selection.tap(day)
    hover = nil
    errorLabel.isHidden = true
    refresh()
  }

  // MARK: Actions

  @objc private func navTapped(_ sender: NSButton) {
    leftMonth = calendar.date(byAdding: .month, value: sender.tag, to: leftMonth) ?? leftMonth
    rebuildMonths()
  }

  @objc private func presetTapped(_ sender: NSButton) {
    guard presetButtons.indices.contains(sender.tag) else { return }
    let days = presetButtons[sender.tag].0.days(now: now, calendar: calendar)
    selection = PaxDateSelection(
      start: days.start, end: days.end, calendar: calendar, options: options)
    errorLabel.isHidden = true
    leftMonth = Self.leftMonth(showing: selection, calendar: calendar)
    rebuildMonths()
  }

  @objc private func applyTapped() {
    guard let start = selection.start else { return }
    let end = selection.end ?? start
    if let error = onApply?(Range(start: start, end: end)) {
      errorLabel.stringValue = error
      errorLabel.isHidden = error.isEmpty
    }
  }

  @objc private func cancelTapped() { onCancel?() }
}

extension PaxTheme {
  public enum DateRange {
    public static let monthGap: CGFloat = 24
    public static let cellInset: CGFloat = 2
    public static let chevronSize: CGFloat = PaxTheme.Dashboard.bodyFont + 1
  }
}
