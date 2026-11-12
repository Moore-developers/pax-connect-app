import Foundation

/// 预设周期；页面按场景决定出现哪几个（见 `PaxDateRangeOptions.presets`）。
public enum PaxDatePreset: CaseIterable {
  case today, yesterday, week, month

  public var title: String {
    switch self {
    case .today: "今日"
    case .yesterday: "昨日"
    case .week: "本周"
    case .month: "本月"
    }
  }

  /// 首尾两天都含；本周、本月截至今天。
  public func days(now: Date, calendar: Calendar) -> (start: Date, end: Date) {
    let today = calendar.startOfDay(for: now)
    switch self {
    case .today: return (today, today)
    case .yesterday:
      let day = calendar.date(byAdding: .day, value: -1, to: today) ?? today
      return (day, day)
    case .week:
      return (calendar.dateInterval(of: .weekOfYear, for: now)?.start ?? today, today)
    case .month:
      return (calendar.dateInterval(of: .month, for: now)?.start ?? today, today)
    }
  }
}

/// 场景差异全在这里：预设、可选日期上下界、最长跨度。组件本身不含任何页面规则。
public struct PaxDateRangeOptions {
  public var presets: [PaxDatePreset]
  public var minDate: Date?
  public var maxDate: Date?
  /// 含首尾的最多天数。
  public var maxSpanDays: Int?

  public init(
    presets: [PaxDatePreset] = PaxDatePreset.allCases, minDate: Date? = nil,
    maxDate: Date? = nil, maxSpanDays: Int? = nil
  ) {
    self.presets = presets
    self.minDate = minDate
    self.maxDate = maxDate
    self.maxSpanDays = maxSpanDays
  }
}

/// 第一次点击即可应用单日；第二次定终点（早于起点则互换），完成区间后再点重新开始。
struct PaxDateSelection {
  private(set) var start: Date?
  private(set) var end: Date?
  let calendar: Calendar
  let options: PaxDateRangeOptions

  init(start: Date?, end: Date?, calendar: Calendar, options: PaxDateRangeOptions) {
    self.calendar = calendar
    self.options = options
    guard let start else { return }
    let first = calendar.startOfDay(for: start)
    let last = calendar.startOfDay(for: end ?? start)
    self.start = min(first, last)
    self.end = max(first, last)
  }

  var canApply: Bool { start != nil }
  var isPicking: Bool { start != nil && end == nil }

  func isEnabled(_ day: Date) -> Bool {
    let day = calendar.startOfDay(for: day)
    if let minDate = options.minDate, day < calendar.startOfDay(for: minDate) { return false }
    if let maxDate = options.maxDate, day > calendar.startOfDay(for: maxDate) { return false }
    if isPicking, let start, let span = options.maxSpanDays {
      let gap = calendar.dateComponents([.day], from: start, to: day).day ?? 0
      return abs(gap) < span
    }
    return true
  }

  mutating func tap(_ day: Date) {
    let day = calendar.startOfDay(for: day)
    guard isEnabled(day) else { return }
    if let first = start, end == nil {
      start = min(first, day)
      end = max(first, day)
    } else {
      start = day
      end = nil
    }
  }

  /// 已选范围；选到一半时用悬停日预览终点。
  func visibleRange(hover: Date?) -> ClosedRange<Date>? {
    guard let start else { return nil }
    if let end { return start...end }
    guard let hover, isEnabled(hover) else { return start...start }
    let day = calendar.startOfDay(for: hover)
    return min(start, day)...max(start, day)
  }

  func matchingPreset(now: Date) -> PaxDatePreset? {
    guard let start, let end else { return nil }
    return options.presets.first {
      let days = $0.days(now: now, calendar: calendar)
      return days.start == start && days.end == end
    }
  }
}
