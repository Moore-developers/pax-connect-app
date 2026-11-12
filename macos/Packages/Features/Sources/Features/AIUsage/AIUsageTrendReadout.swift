import AppKit
import PaxCore
import PaxUI

extension AIUsageStore {
  var trendHoverDetails: [PaxAnalysisChart.Hover] {
    let rows = trendBuckets(filter: trendFilter)
    let ranges = trendIntervals
    let filter = PaxUsageFilterMenu.displayTitle(trendFilter, groups: trendGroups)
    var previous: (title: String, value: Double)?
    return rows.enumerated().map { index, row in
      let (date, sum) = row
      let range = ranges[index]
      let title: String
      let subtitle: String
      switch trendComponent {
      case .hour:
        title = AIUsageFormatting.monthDay(date) + " " + hour(date) + "–" + hour(range.upperBound)
        subtitle = "按小时"
      case .month:
        title =
          "\(calendar.component(.year, from: date))年\(calendar.component(.month, from: date))月"
        subtitle = "按月"
      default:
        title = AIUsageFormatting.date(date)
        subtitle = "按天"
      }
      let value = sum.inputUnitCost
      var tag: PaxAnalysisChart.Hover.Tag?
      var comparison = "无可比基准"
      if let value, let base = previous {
        comparison = "较 \(base.title)"
        if base.value > 0 {
          let delta = ((value / base.value - 1) * 1000).rounded() / 10
          let percent = abs(delta).formatted(.number.precision(.fractionLength(0...1)))
          tag = .init(
            delta == 0 ? "持平" : (delta < 0 ? "下降 " : "增长 ") + percent + "%",
            color: delta == 0
              ? PaxTheme.Color.textSecondary
              : delta < 0 ? PaxTheme.DashboardColor.green : PaxTheme.DashboardColor.red)
        } else {
          comparison += "，基准为零，无法计算百分比"
        }
      }
      if let value { previous = (title, value) }
      let future = date > now
      let state = future ? "尚未发生" : sum.input == 0 ? "无输入，单位成本 —" : comparison
      var details: [(label: String, value: String)] = [
        (state, ""),
        ("输入 Token", AIUsageFormatting.tokens(sum.input)),
        ("输入费用", price(sum.originalCost)),
      ]
      if trendComponent == .month, !future {
        let last = min(range.upperBound, interval.upperBound).addingTimeInterval(-1)
        details.append(
          ("统计范围", AIUsageFormatting.monthDay(date) + "–" + AIUsageFormatting.monthDay(last)))
      }
      return .init(
        title: title, subtitle: subtitle,
        summary: [("", value.map(price) ?? "—")],
        proportion: nil, proportionLabel: "", proportionValue: "",
        details: details, context: "",
        badges: trendFilter == .all ? [] : [.init(filter, color: PaxTheme.Color.textSecondary)],
        tag: tag, summarySuffix: "USD / M", summaryTagInline: true,
        summaryColor: PaxTheme.DashboardColor.red)
    }
  }

  private func hour(_ date: Date) -> String {
    String(format: "%02d:00", calendar.component(.hour, from: date))
  }

  private func price(_ value: Double) -> String {
    AIUsageFormatting.cost(value, precision: value > 0 && value < 0.0005 ? 4 : 3)
  }
}
