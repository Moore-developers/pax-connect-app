import Foundation

extension PaxTimeSeriesLayout {
  struct Tick: Equatable {
    let date: Date
    let fraction: Double
    let label: String
    let isEndpoint: Bool
  }

  private static let steps: [TimeInterval] = [
    10, 30, 60, 300, 900, 1800, 3600, 7200, 10800, 21600, 43200,
    86400, 172_800, 432_000, 604_800, 1_209_600,
  ]

  /// Labels aligned to whole hours/days, at least `minimumSpacing` apart, always including
  /// the start and the end of the domain.
  static func ticks(
    domain: PaxTimeDomain, width: CGFloat,
    minimumSpacing: CGFloat = PaxTheme.TimeSeries.minimumTickSpacing,
    calendar: Calendar = .current
  ) -> [Tick] {
    let span = domain.span
    let startTick = Tick(
      date: domain.start, fraction: 0,
      label: label(domain.start, step: 0, domain: domain, calendar: calendar), isEndpoint: true)
    guard span > 0, width > 0 else { return [startTick] }
    let step =
      steps.first { CGFloat($0 / span) * width >= minimumSpacing } ?? steps[steps.count - 1]
    let endTick = Tick(
      date: domain.end, fraction: 1,
      label: label(domain.end, step: 0, domain: domain, calendar: calendar), isEndpoint: true)
    let guardDistance = Double(minimumSpacing / width)
    var middle: [Tick] = []
    for date in aligned(domain, step: step, calendar: calendar) {
      let fraction = date.timeIntervalSince(domain.start) / span
      guard fraction >= guardDistance, fraction <= 1 - guardDistance else { continue }
      middle.append(
        Tick(
          date: date, fraction: fraction,
          label: label(date, step: step, domain: domain, calendar: calendar), isEndpoint: false))
    }
    return thinned(
      [startTick] + middle + [endTick], minimumSpacing: minimumSpacing,
      position: { CGFloat($0.fraction) * width })
  }

  /// One label per text (an interior label equal to another is dropped), then adjacent labels
  /// closer than `minimumSpacing` lose the earlier one; start and end always stay.
  /// `position` is where a label is drawn (its centre), so edge-clamped labels count as drawn.
  static func thinned(
    _ ticks: [Tick], minimumSpacing: CGFloat, position: (Tick) -> CGFloat
  ) -> [Tick] {
    var labels = Set(ticks.filter(\.isEndpoint).map(\.label))
    var unique: [Tick] = []
    for tick in ticks {
      if !tick.isEndpoint {
        guard labels.insert(tick.label).inserted else { continue }
      }
      unique.append(tick)
    }
    var output: [Tick] = []
    for tick in unique {
      while let last = output.last, position(tick) - position(last) < minimumSpacing {
        if last.isEndpoint { break }
        output.removeLast()
      }
      if let last = output.last, last.isEndpoint, !tick.isEndpoint,
        position(tick) - position(last) < minimumSpacing
      {
        continue
      }
      output.append(tick)
    }
    return output
  }

  private static func aligned(
    _ domain: PaxTimeDomain, step: TimeInterval, calendar: Calendar
  ) -> [Date] {
    var output: [Date] = []
    var day = calendar.startOfDay(for: domain.start)
    let reference = calendar.startOfDay(for: Date(timeIntervalSinceReferenceDate: 0))
    while day <= domain.end {
      if step >= 86400 {
        let index = Int((day.timeIntervalSince(reference) / 86400).rounded())
        let days = Int(step / 86400)
        if index % days == 0, day > domain.start, day < domain.end { output.append(day) }
      } else {
        var offset = 0.0
        while offset < 86400 {
          let date = day.addingTimeInterval(offset)
          if date > domain.end { break }
          if date > domain.start { output.append(date) }
          offset += step
        }
      }
      guard let next = calendar.date(byAdding: .day, value: 1, to: day) else { break }
      day = next
    }
    return output
  }

  private static func label(
    _ date: Date, step: TimeInterval, domain: PaxTimeDomain, calendar: Calendar
  ) -> String {
    let parts = calendar.dateComponents([.month, .day, .hour, .minute, .second], from: date)
    let multiDay =
      !calendar.isDate(domain.start, inSameDayAs: domain.end) && domain.span > 86400
    let isMidnight = parts.hour == 0 && parts.minute == 0 && parts.second == 0
    if step >= 86400 || (multiDay && (isMidnight || step == 0 && domain.span > 172_800)) {
      return "\(parts.month ?? 0)/\(parts.day ?? 0)"
    }
    if step > 0 && step < 60 {
      return clock(date, calendar: calendar, seconds: true)
    }
    return clock(date, calendar: calendar, seconds: false)
  }

  static func clock(_ date: Date, calendar: Calendar = .current, seconds: Bool = false) -> String {
    let parts = calendar.dateComponents([.hour, .minute, .second], from: date)
    let base = String(format: "%02d:%02d", parts.hour ?? 0, parts.minute ?? 0)
    return seconds ? base + String(format: ":%02d", parts.second ?? 0) : base
  }
}

enum PaxTimeSeriesCompaction {
  /// Down-sample by time slot, keeping the maximum of each slot. A slot with several
  /// observations is annotated; a slot with none stays an unobserved bucket.
  static func reduce(
    _ series: PaxTimeSeries, maximumSlots: Int = PaxTheme.TimeSeries.compactMaximumSlots
  ) -> PaxTimeSeries {
    let domain = series.domain
    // A list series ending at its latest observation never carries trailing buckets.
    let points =
      domain.endsAtLatestBucket ? series.endingAtLatestObservation().points : series.points
    guard maximumSlots > 0, domain.span > 0 else { return series }
    // A bucketed series is reduced by whole buckets; a raw series by equal time slots.
    let width: TimeInterval
    if let bucket = domain.bucket {
      let count = max(1, Int((domain.span / bucket - 1e-9).rounded(.up)))
      guard count > maximumSlots else { return series }
      width = bucket * Double((Double(count) / Double(maximumSlots)).rounded(.up))
    } else {
      guard points.count > maximumSlots else { return series }
      width = domain.span / Double(maximumSlots)
    }
    let slots = Dictionary(grouping: points) { point -> Int in
      let slot = Int((point.at.timeIntervalSince(domain.start) / width + 1e-9).rounded(.down))
      return domain.bucket == nil ? min(maximumSlots - 1, max(0, slot)) : slot
    }
    var output: [PaxTimeSeriesPoint] = []
    for slot in slots.keys.sorted() {
      let members = slots[slot] ?? []
      let known = members.filter { $0.value != nil }
      guard let best = known.max(by: { ($0.total ?? 0) < ($1.total ?? 0) }) else {
        if domain.bucket != nil {
          output.append(
            PaxTimeSeriesPoint(
              at: domain.start.addingTimeInterval(Double(slot) * width), value: nil))
        }
        continue
      }
      let note = known.count > 1 ? "列表抽样，时段内最大值" : best.note
      let at =
        domain.bucket == nil
        ? best.at : domain.start.addingTimeInterval(Double(slot) * width)
      output.append(
        PaxTimeSeriesPoint(
          at: at, value: best.value, stacked: best.stacked,
          partial: known.contains { $0.partial }, note: note))
    }
    let bucket = domain.bucket == nil ? nil : width
    guard domain.endsAtLatestBucket, let last = output.last else {
      return PaxTimeSeries(
        domain: PaxTimeDomain(
          windowStart: domain.windowStart, coverageStart: domain.start, end: domain.end,
          bucket: bucket),
        points: output)
    }
    // Slots are measured over the new domain; the last observed slot's centre is the new end.
    return PaxTimeSeries(
      domain: PaxTimeDomain(
        windowStart: domain.windowStart, start: domain.start,
        end: last.at.addingTimeInterval(width / 2), bucket: bucket, endsAtLatestBucket: true),
      points: output)
  }
}
