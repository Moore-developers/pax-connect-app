import Foundation
import PaxCore

/// Usage values remain separate from native CLI configuration and never contain credentials.
struct AIModel: Identifiable, Equatable {
  let id: String
  let name: String
  let clients: Set<AIClient>
  let inputPrice: Double?
  let outputPrice: Double?
}

typealias AIRequest = AIUsageRequest

struct AIUsageSummary {
  let requests: [AIRequest]
  var aggregate: AIUsageTotals?
  var aggregateBuckets: [AIUsageBucket] = []
  init(requests: [AIRequest]) { self.requests = requests }
  init(buckets: [AIUsageBucket]) {
    requests = []
    aggregateBuckets = buckets
    aggregate = buckets.reduce(into: AIUsageTotals()) { $0.add($1.totals) }
  }
  init(totals: AIUsageTotals) {
    requests = []
    aggregate = totals
  }
  var costLabel: String { AIUsageFormatting.cost(cost) }
  var officialCostLabel: String { AIUsageFormatting.cost(originalCost) }
  var officialCosts: [Double] {
    aggregate?.costs
      ?? (0..<4).map { index in requests.reduce(0) { $0 + $1.amounts[index] } }
  }
  var count: Int { (aggregate.map { $0.count }) ?? (requests.count) }
  var success: Int { (aggregate.map { $0.success }) ?? (requests.filter(\.succeeded).count) }
  var failed: Int { count - success }
  var tokens: Int {
    (aggregate.map { $0.input + $0.output }) ?? (requests.reduce(0) { $0 + $1.tokens })
  }
  /// Four-part official total cost per million input tokens; cache is already included in input.
  var inputUnitCost: Double? { input > 0 ? originalCost / Double(input) * 1_000_000 : nil }
  var input: Int { (aggregate.map { $0.input }) ?? (requests.reduce(0) { $0 + $1.input }) }
  var read: Int { (aggregate.map { $0.read }) ?? (requests.reduce(0) { $0 + $1.read }) }
  var write: Int { (aggregate.map { $0.write }) ?? (requests.reduce(0) { $0 + $1.write }) }
  var output: Int { (aggregate.map { $0.output }) ?? (requests.reduce(0) { $0 + $1.output }) }
  var ordinary: Int {
    (aggregate.map { max(0, $0.input - $0.read - $0.write) })
      ?? (requests.reduce(0) { $0 + $1.ordinary })
  }
  var cost: Double { (aggregate.map { $0.cost }) ?? (requests.reduce(0) { $0 + $1.cost }) }
  var originalCost: Double {
    (aggregate.map { $0.costs.reduce(0, +) }) ?? (requests.reduce(0) { $0 + $1.originalCost })
  }
  var costs: [Double] {
    aggregate?.billedCosts
      ?? (0..<4).map { index in requests.reduce(0) { $0 + $1.amounts[index] * $1.multiplier } }
  }
  var cache: Double? { input > 0 ? Double(read) / Double(input) : nil }
  var successRate: Double? { count > 0 ? Double(success) / Double(count) : nil }
  /// Mean of the measured samples; `nil` means none.
  func mean(_ key: KeyPath<AIRequest, Double?>) -> Double? {
    if let a = aggregate {
      let isTPS = key == \AIRequest.tps
      let (sum, count) = isTPS ? (a.tpsSum, a.tpsCount) : (a.elapsedSum, a.elapsedCount)
      return count > 0 ? sum / Double(count) : nil
    }
    let values = requests.filter(\.succeeded).compactMap { $0[keyPath: key] }
    return values.isEmpty ? nil : values.reduce(0, +) / Double(values.count)
  }
  func timingText(_ key: KeyPath<AIRequest, Double?>, _ format: String) -> String? {
    mean(key).map { String(format: format, $0) }
  }
}

enum AIUsagePeriod: String, CaseIterable {
  case today = "今日"
  case yesterday = "昨日"
  case week = "本周"
  case month = "本月"
  case year = "本年"
  case custom = "自定义"
  /// Name of the period it is compared against, for the change pill tooltip.
  var previousName: String {
    switch self {
    case .today: "昨日"
    case .yesterday: "前日"
    case .week: "上周"
    case .month: "上月"
    case .year: "去年同期"
    case .custom: "上一时段"
    }
  }
  func interval(now: Date, start: Date, end: Date, calendar: Calendar) -> Range<Date> {
    let today = calendar.startOfDay(for: now)
    let lower: Date
    let upper: Date
    switch self {
    case .today:
      lower = today
      upper = calendar.date(byAdding: .day, value: 1, to: today) ?? today
    case .yesterday:
      lower = calendar.date(byAdding: .day, value: -1, to: today) ?? today
      upper = today
    case .week, .month, .year:
      lower = calendar.dateInterval(of: calendarComponent, for: now)?.start ?? today
      upper = calendar.date(byAdding: .day, value: 1, to: today) ?? today
    case .custom:
      lower = calendar.startOfDay(for: start)
      upper = calendar.date(byAdding: .day, value: 1, to: calendar.startOfDay(for: end)) ?? end
    }
    return lower..<max(lower, upper)
  }
  var calendarComponent: Calendar.Component {
    switch self {
    case .week: .weekOfYear
    case .month: .month
    case .year: .year
    default: .day
    }
  }
  /// Full chart window; statistics and queries remain limited to the selected elapsed period.
  func chartInterval(now: Date, start: Date, end: Date, calendar: Calendar) -> Range<Date> {
    let selected = interval(now: now, start: start, end: end, calendar: calendar)
    switch self {
    case .week, .month, .year:
      let full = calendar.dateInterval(of: calendarComponent, for: now)
      return (full?.start ?? selected.lowerBound)..<(full?.end ?? selected.upperBound)
    default: return selected
    }
  }
  /// Full preceding calendar period; the daemon clips it to the current elapsed day/time.
  func sessionComparisonInterval(now: Date, start: Date, end: Date, calendar: Calendar)
    -> Range<Date>?
  {
    let current = interval(now: now, start: start, end: end, calendar: calendar)
    switch self {
    case .week, .month, .year:
      guard let full = calendar.dateInterval(of: calendarComponent, for: now),
        let previousStart = calendar.date(byAdding: calendarComponent, value: -1, to: full.start),
        let previous = calendar.dateInterval(of: calendarComponent, for: previousStart)
      else { return nil }
      return previous.start..<previous.end
    case .today, .yesterday, .custom:
      guard
        let days = calendar.dateComponents(
          [.day], from: current.lowerBound, to: current.upperBound
        ).day, days > 0,
        let lower = calendar.date(byAdding: .day, value: -days, to: current.lowerBound)
      else { return nil }
      return lower..<current.lowerBound
    }
  }
  /// The period compared against: today -> yesterday, yesterday -> the day before, week -> last
  /// week, month -> last month, custom -> the preceding span of the same length (one day -> the
  /// previous day). A running week or month is cut to the same elapsed length so it is a fair base.
  func previousInterval(now: Date, start: Date, end: Date, calendar: Calendar) -> Range<Date> {
    let current = interval(now: now, start: start, end: end, calendar: calendar)
    func shift(_ date: Date, _ component: Calendar.Component, _ value: Int) -> Date {
      calendar.date(byAdding: component, value: value, to: date) ?? date
    }
    let lower: Date
    switch self {
    case .today, .yesterday: lower = shift(current.lowerBound, .day, -1)
    case .week: lower = shift(current.lowerBound, .weekOfYear, -1)
    case .month: lower = shift(current.lowerBound, .month, -1)
    case .year: lower = shift(current.lowerBound, .year, -1)
    case .custom:
      let days =
        calendar.dateComponents([.day], from: current.lowerBound, to: current.upperBound)
        .day ?? 1
      lower = shift(current.lowerBound, .day, -max(1, days))
    }
    switch self {
    case .today, .yesterday, .custom: return lower..<current.lowerBound
    case .week, .month, .year:
      let limit = shift(lower, calendarComponent, 1)
      if self == .year { return lower..<min(limit, shift(current.upperBound, .year, -1)) }
      let span = current.upperBound.timeIntervalSince(current.lowerBound)
      return lower..<min(limit, lower.addingTimeInterval(span))
    }
  }
}

extension AIUsageRequest {
  var officialCostLabel: String {
    AIUsageFormatting.cost(
      originalCost, precision: originalCost > 0 && originalCost < 0.0005 ? 4 : 3)
  }
  /// 3 decimals, 4 when a non-zero cost would otherwise round to "$0.000".
  var costLabel: String {
    AIUsageFormatting.cost(cost, precision: cost > 0 && cost < 0.0005 ? 4 : 3)
  }
}
