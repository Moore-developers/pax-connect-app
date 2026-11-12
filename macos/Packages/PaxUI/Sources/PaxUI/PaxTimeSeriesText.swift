import AppKit

/// What a hover / pin / arrow key landed on. Hosts use it to mirror the readout elsewhere.
public struct PaxTimeSeriesSelection: Equatable {
  public let timeText: String
  /// Primary value (total for stacked series); nil for an unobserved bucket.
  public let value: Double?
  public let isGap: Bool
  public let note: String
}

/// Series naming, colors and number formatting. Colors come from existing PaxTheme tokens.
@MainActor
public struct PaxTimeSeriesStyle {
  public let title: String
  /// One color: a single series. Two colors: first is the base, second is stacked on top.
  public let colors: [NSColor]
  public let names: [String]
  public let minimumCeiling: Double
  public let formatValue: (Double) -> String
  /// Axis tick text. Plain numbers when the unit caption carries the unit.
  public let formatTick: (Double) -> String
  /// Optional scale caption, e.g. "MB/15分钟" for per-bucket traffic.
  public let unit: String

  public init(
    title: String, colors: [NSColor], names: [String], minimumCeiling: Double,
    unit: String = "", formatTick: ((Double) -> String)? = nil,
    formatValue: @escaping (Double) -> String
  ) {
    self.title = title
    self.colors = colors
    self.names = names
    self.minimumCeiling = minimumCeiling
    self.unit = unit
    self.formatValue = formatValue
    self.formatTick = formatTick ?? formatValue
  }

  /// Milliseconds on the axis: ticks are plain integers, the single "ms" caption names the unit.
  public static func latency(
    title: String = "响应延迟", color: NSColor? = nil, formatValue: @escaping (Double) -> String
  ) -> PaxTimeSeriesStyle {
    .init(
      title: title, colors: [color ?? PaxTheme.DashboardColor.green], names: [title],
      minimumCeiling: PaxTheme.TimeSeries.latencyMinimumCeiling, unit: "ms",
      formatTick: { Int($0.rounded()).formatted(.number.grouping(.never)) },
      formatValue: formatValue)
  }

  /// Decimal MB per bucket: ticks are plain MB numbers, `unit` names the bucket width.
  public static func traffic(
    unit: String, formatValue: @escaping (Double) -> String
  ) -> PaxTimeSeriesStyle {
    .init(
      title: "流量趋势",
      colors: [PaxTheme.DashboardColor.amber, PaxTheme.DashboardColor.green],
      names: ["上传", "下载"], minimumCeiling: PaxTheme.TimeSeries.trafficMinimumCeiling,
      unit: unit,
      formatTick: { $0.formatted(.number.precision(.fractionLength(0...3))) },
      formatValue: formatValue)
  }

  var isStacked: Bool { colors.count > 1 }
}

extension PaxTimeSeriesLayout {
  /// Bucket range ("12:00–12:15") or the exact sample time ("12:00:07") for raw series.
  func timeText(_ date: Date, calendar: Calendar = .current) -> String {
    if axis == .samples {
      let p = calendar.dateComponents([.year, .month, .day, .hour, .minute, .second], from: date)
      return String(
        format: "%04d-%02d-%02d %02d:%02d:%02d", p.year ?? 0, p.month ?? 0, p.day ?? 0, p.hour ?? 0,
        p.minute ?? 0, p.second ?? 0)
    }
    let multiDay = domain.span > 86400 || !calendar.isDate(domain.start, inSameDayAs: domain.end)
    func stamp(_ value: Date, seconds: Bool = false) -> String {
      let clock = Self.clock(value, calendar: calendar, seconds: seconds)
      guard multiDay else { return clock }
      let parts = calendar.dateComponents([.month, .day], from: value)
      return "\(parts.month ?? 0)/\(parts.day ?? 0) " + clock
    }
    guard let bucket = domain.bucket else { return stamp(date, seconds: true) }
    let end = min(date.addingTimeInterval(bucket), domain.end)
    return stamp(date) + "–" + Self.clock(end, calendar: calendar)
  }

  /// Readout for the tooltip and host mirrors. Segments are joined by " · " because the
  /// shared tooltip coordinator renders each such part on its own line.
  @MainActor
  func selection(for hit: Hit, style: PaxTimeSeriesStyle) -> PaxTimeSeriesSelection {
    let time = timeText(hit.date)
    guard let index = hit.index, points.indices.contains(index), let total = points[index].total
    else {
      return PaxTimeSeriesSelection(timeText: time, value: nil, isGap: true, note: "未采集")
    }
    if axis == .samples {
      let milliseconds = total.formatted(.number.grouping(.never).precision(.fractionLength(0...3)))
      return PaxTimeSeriesSelection(
        timeText: time, value: total, isGap: false, note: time + " · " + milliseconds + " ms")
    }
    let point = points[index]
    var parts = [time]
    if point.partial { parts.append("部分采集") }
    if style.isStacked, let base = point.value {
      let names = style.names
      parts.append(names[0] + " " + style.formatValue(base))
      parts.append((names.count > 1 ? names[1] : "") + " " + style.formatValue(point.stacked ?? 0))
      parts.append("合计 " + style.formatValue(total))
    } else {
      parts.append(style.formatValue(total))
    }
    if !point.note.isEmpty { parts.append(point.note) }
    return PaxTimeSeriesSelection(
      timeText: time, value: total, isGap: false, note: parts.joined(separator: " · "))
  }

  /// Traffic-list tooltip: just the point's value, "未采集" for a gap. No time range, flags or
  /// sampling note; the accessibility value keeps the full `selection(for:style:)` note.
  @MainActor
  func compactReadout(for hit: Hit, style: PaxTimeSeriesStyle) -> String {
    guard let index = hit.index, points.indices.contains(index), let total = points[index].total
    else { return "未采集" }
    return style.formatValue(total)
  }

  /// One summary instead of every sample: range, latest, max and unobserved bucket count.
  @MainActor
  func summary(style: PaxTimeSeriesStyle) -> String {
    var parts =
      axis == .samples
      ? [style.title, "最近 \(points.count) 个有效样本"] : [style.title, "范围 " + domain.rangeText()]
    if let note = domain.coverageNote() { parts.append(note) }
    guard let latest, let value = latest.total else {
      return parts.joined(separator: "，") + "，暂无数据"
    }
    parts.append("最新 " + style.formatValue(value))
    if let peak = maximum { parts.append("最大 " + style.formatValue(peak)) }
    if gapCount > 0 { parts.append("未采集 \(gapCount) 个时段") }
    return parts.joined(separator: "，")
  }
}

extension PaxTimeDomain {
  /// Header text such as "00:00–14:32": the inherited range's real start and end.
  /// Ranges that cross midnight carry the month/day.
  public func rangeText(calendar: Calendar = .current) -> String {
    let sameDay = calendar.isDate(start, inSameDayAs: end)
    func stamp(_ date: Date) -> String {
      let clock = PaxTimeSeriesLayout.clock(date, calendar: calendar, seconds: span <= 120)
      guard !sameDay else { return clock }
      let parts = calendar.dateComponents([.month, .day], from: date)
      return "\(parts.month ?? 0)/\(parts.day ?? 0) " + clock
    }
    return stamp(start) + "–" + stamp(end)
  }
}

enum PaxTimeSeriesText {
  /// Width of the shared readout for `note` (parts joined by " · " are one line each).
  @MainActor
  static func readoutWidth(_ note: String) -> CGFloat {
    let attrs: [NSAttributedString.Key: Any] = [
      .font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyFont, weight: .medium)
    ]
    let widest =
      note.components(separatedBy: " · ").map { ($0 as NSString).size(withAttributes: attrs).width }
      .max() ?? 0
    return ceil(widest) + PaxTheme.Spacing.medium * 2 + PaxTheme.Spacing.regular
  }
}
