import AppKit

/// 单日格的绘制状态，由选择器统一计算。
struct PaxCalendarDayState {
  var isEdge = false
  var isInRange = false
  var isToday = false
  var isEnabled = true
}

/// 一个月的日期网格：周标题加最多 6 行日期格。本月之外的日期只作灰色占位，不可点。
@MainActor
final class PaxCalendarMonthView: NSView {
  static let cellSize = NSSize(width: 34, height: 30)
  private static let rows = 6
  private static let headerHeight: CGFloat = 28

  let month: Date
  private let calendar: Calendar
  private var cells: [PaxCalendarDayCell] = []

  var onTap: ((Date) -> Void)?
  var onHover: ((Date?) -> Void)?
  var stateOf: (Date) -> PaxCalendarDayState = { _ in .init() } {
    didSet { refresh() }
  }

  init(month: Date, calendar: Calendar) {
    self.calendar = calendar
    self.month = calendar.dateInterval(of: .month, for: month)?.start ?? month
    super.init(frame: .zero)
    translatesAutoresizingMaskIntoConstraints = false
    build()
  }

  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  override var isFlipped: Bool { true }
  override var intrinsicContentSize: NSSize {
    NSSize(
      width: Self.cellSize.width * 7,
      height: Self.headerHeight + Self.cellSize.height * CGFloat(Self.rows))
  }

  func refresh() {
    for cell in cells { cell.state = stateOf(cell.day) }
  }

  private func build() {
    let symbols = ["日", "一", "二", "三", "四", "五", "六"]
    let first = calendar.firstWeekday - 1
    for column in 0..<7 {
      let label = PaxText.label(
        symbols[(first + column) % 7], font: PaxTheme.Typography.caption(), secondary: true)
      label.alignment = .center
      label.frame = NSRect(
        x: Self.cellSize.width * CGFloat(column), y: 0, width: Self.cellSize.width,
        height: Self.headerHeight)
      label.frame.origin.y = (Self.headerHeight - label.intrinsicContentSize.height) / 2
      addSubview(label)
    }
    let weekday = calendar.component(.weekday, from: month)
    let lead = (weekday - calendar.firstWeekday + 7) % 7
    guard let gridStart = calendar.date(byAdding: .day, value: -lead, to: month) else { return }
    for index in 0..<(7 * Self.rows) {
      guard let day = calendar.date(byAdding: .day, value: index, to: gridStart) else { continue }
      let inMonth = calendar.isDate(day, equalTo: month, toGranularity: .month)
      let cell = PaxCalendarDayCell(day: day, number: calendar.component(.day, from: day))
      cell.isOutside = !inMonth
      cell.frame = NSRect(
        x: Self.cellSize.width * CGFloat(index % 7),
        y: Self.headerHeight + Self.cellSize.height * CGFloat(index / 7),
        width: Self.cellSize.width, height: Self.cellSize.height)
      cell.onTap = { [weak self] in self?.onTap?($0) }
      cell.onHover = { [weak self] in self?.onHover?($0) }
      cell.setAccessibilityLabel(
        day.formatted(
          Date.FormatStyle(calendar: calendar).month(.wide).day().weekday(.abbreviated)))
      cells.append(cell)
      addSubview(cell)
    }
    refresh()
  }
}

/// 单日格：整格底色相连组成区间带，起止日用实心圆角块，今天描边。
@MainActor
final class PaxCalendarDayCell: NSView {
  let day: Date
  private let number: Int
  var isOutside = false { didSet { needsDisplay = true } }
  var state = PaxCalendarDayState() {
    didSet {
      guard
        state.isEdge != oldValue.isEdge || state.isInRange != oldValue.isInRange
          || state.isToday != oldValue.isToday || state.isEnabled != oldValue.isEnabled
      else { return }
      needsDisplay = true
      setAccessibilitySelected(state.isEdge || state.isInRange)
    }
  }
  var onTap: ((Date) -> Void)?
  var onHover: ((Date?) -> Void)?

  init(day: Date, number: Int) {
    self.day = day
    self.number = number
    super.init(frame: .zero)
    setAccessibilityElement(true)
    setAccessibilityRole(.button)
  }

  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  override var isFlipped: Bool { true }
  private var interactive: Bool { !isOutside && state.isEnabled }

  override func updateTrackingAreas() {
    super.updateTrackingAreas()
    for area in trackingAreas { removeTrackingArea(area) }
    addTrackingArea(
      NSTrackingArea(
        rect: bounds, options: [.mouseEnteredAndExited, .activeInActiveApp], owner: self))
  }

  override func mouseEntered(with event: NSEvent) { if interactive { onHover?(day) } }
  override func mouseExited(with event: NSEvent) { onHover?(nil) }
  override func mouseDown(with event: NSEvent) { if interactive { onTap?(day) } }
  override func accessibilityPerformPress() -> Bool {
    guard interactive else { return false }
    onTap?(day)
    return true
  }

  override func draw(_ dirtyRect: NSRect) {
    guard !isOutside else {
      drawNumber(PaxTheme.Color.textDisabled)
      return
    }
    let inset = PaxTheme.DateRange.cellInset
    if state.isInRange && !state.isEdge {
      PaxTheme.Routes.menuFill.setFill()
      NSRect(x: 0, y: inset, width: bounds.width, height: bounds.height - inset * 2).fill()
    }
    let block = bounds.insetBy(dx: inset, dy: inset)
    let shape = NSBezierPath(
      roundedRect: block, xRadius: PaxTheme.Radius.control, yRadius: PaxTheme.Radius.control)
    if state.isEdge {
      PaxTheme.Routes.blue.setFill()
      shape.fill()
    } else if state.isToday {
      PaxTheme.Routes.blue.setStroke()
      shape.lineWidth = PaxTheme.Layout.cardBorder
      shape.stroke()
    }
    drawNumber(
      state.isEdge
        ? .white : state.isEnabled ? PaxTheme.Color.text : PaxTheme.Color.textDisabled)
  }

  private func drawNumber(_ color: NSColor) {
    let text = NSAttributedString(
      string: String(number),
      attributes: [.font: PaxTheme.Typography.time, .foregroundColor: color])
    let size = text.size()
    text.draw(
      at: NSPoint(x: (bounds.width - size.width) / 2, y: (bounds.height - size.height) / 2))
  }
}
