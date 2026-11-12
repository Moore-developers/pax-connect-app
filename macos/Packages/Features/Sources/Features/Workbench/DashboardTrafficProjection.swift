import Foundation
import PaxCore
import PaxUI

enum DashboardTrafficProjection {
  static let hourCount = 12
  /// Only the overview axis uses whole byte units; hourly values and tooltip precision are unchanged.
  static func axisByteLabel(_ megabytes: Double) -> String {
    let bytes = megabytes * 1_000_000
    let units: [(scale: Double, name: String)] = [
      (1_000_000_000_000_000_000, "EB"), (1_000_000_000_000_000, "PB"),
      (1_000_000_000_000, "TB"), (1_000_000_000, "GB"), (1_000_000, "MB"), (1_000, "KB"),
    ]
    let unit = units.first { bytes >= $0.scale } ?? (scale: 1, name: "B")
    return (bytes / unit.scale).formatted(.number.precision(.fractionLength(0))) + " " + unit.name
  }

  /// 「今日流量」卡。`snapshot` 是 `Snapshot.today_traffic`（大数与占比），`page` 是今日窗口的
  /// 趋势点；`trendPage` 仅在凌晨提供跨日趋势。`page == nil` 表示尚未读到；`failed` 表示读取失败。
  static func card(
    snapshot: TrafficSnapshot?, page: TrafficPage?, trendPage: TrafficPage? = nil,
    failed: Bool = false, now: Date,
    calendar: Calendar = .current
  ) -> PaxTodayTrafficModel {
    var model = PaxTodayTrafficModel()
    // 大数与「截至」优先取今日窗口读到的合计（与流量页同源，代理关闭时仍是本地历史）；
    // 占比取 `Snapshot.today_traffic` 的三项去向。
    let measured = page.flatMap { $0.history.revision > 0 ? $0 : nil }
    let hourly = hours(page: trendPage ?? page, now: now, calendar: calendar)
    let hasStats = page?.trend.contains { !$0.gap } == true
    // 缺统计显示「—」（模型默认值），不把没读到的数据写成 0 流量。
    if let measured {
      if hasStats || measured.upload + measured.download > 0 {
        model.upload = TrafficFormatting.bytes(measured.upload)
        model.download = TrafficFormatting.bytes(measured.download)
      }
    } else if let snapshot {
      let total = snapshot.uploadBytes + snapshot.downloadBytes
      if snapshot.collectionState == "collecting" || total > 0 {
        model.upload = TrafficFormatting.bytes(snapshot.uploadBytes)
        model.download = TrafficFormatting.bytes(snapshot.downloadBytes)
      }
    }
    let asOf = snapshot?.asOf ?? measured.map(\.asOf)
    model.asOf = asOf.map { "截至 " + DashboardFormat.clock($0, calendar: calendar) } ?? ""
    if let snapshot {
      model.shares = [snapshot.proxiedBytes, snapshot.directBytes, snapshot.unclassifiedBytes].map {
        Double($0)
      }
      if model.shares.reduce(0, +) == 0 {
        model.shareEmpty = snapshot.collectionState == "collecting" ? "今日暂无流量" : "今日去向未采集"
      }
    } else {
      model.shareEmpty = "统计尚未读取"
    }
    model.hourly = hourly
    if !hourly.contains(where: { !$0.values.isEmpty }) {
      model.hourly = []
      model.hourlyNote = failed ? "统计暂不可用" : page == nil ? "正在读取统计" : "暂无统计"
    }
    return model
  }

  /// 当前小时及之前11个完整小时；用绝对时间分桶，跨日和夏令时重复小时不会混到同一根柱。
  static func hours(page: TrafficPage?, now: Date, calendar: Calendar) -> [PaxChartSample] {
    guard let page, let current = calendar.dateInterval(of: .hour, for: now)?.start else {
      return []
    }
    let start = current.addingTimeInterval(-Double(hourCount - 1) * 3600)
    var upload = [UInt64](repeating: 0, count: hourCount)
    var download = upload
    var seen = [Bool](repeating: false, count: hourCount)
    for point in page.trend where !point.gap && point.at >= start && point.at <= now {
      let hour = Int(point.at.timeIntervalSince(start) / 3600)
      upload[hour] += point.upload
      download[hour] += point.download
      seen[hour] = true
    }
    let date = DateFormatter()
    date.calendar = calendar
    date.timeZone = calendar.timeZone
    date.dateFormat = "MM-dd HH:mm zzz"
    return (0..<hourCount).map { hour in
      let at = start.addingTimeInterval(Double(hour) * 3600)
      let label = DashboardFormat.clock(at, calendar: calendar)
      let time = date.string(from: at)
      guard seen[hour] else {
        var gap = PaxChartSample(label, values: [], at: at)
        gap.readout = [time, "缺统计 —"]
        return gap
      }
      var sample = PaxChartSample(
        label, values: [Double(upload[hour]) / 1_000_000, Double(download[hour]) / 1_000_000],
        at: at)
      sample.amountLabel = TrafficFormatting.bytes(upload[hour] + download[hour])
      sample.readout = [
        time, "上传 " + TrafficFormatting.bytes(upload[hour]),
        "下载 " + TrafficFormatting.bytes(download[hour]),
        "合计 " + TrafficFormatting.bytes(upload[hour] + download[hour]),
      ]
      return sample
    }
  }
}
