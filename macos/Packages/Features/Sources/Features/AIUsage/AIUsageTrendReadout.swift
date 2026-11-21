import AppKit
import PaxCore
import PaxUI

extension AIUsageStore {
  var trendHoverDetails: [PaxAnalysisChart.Hover] { trendHoverDetails(context: .chinese) }
  func trendHoverDetails(context: PresentationContext) -> [PaxAnalysisChart.Hover] {
    let rows = trendBuckets(filter: trendFilter)
    let ranges = trendIntervals
    let filter = PaxUsageFilterMenu.displayTitle(
      trendFilter, groups: trendGroups(context: context), allTitle: context.text("common.all"))
    var previous: (title: String, value: Double)?
    return rows.enumerated().map { index, row in
      let (date, sum) = row
      let range = ranges[index]
      let title: String
      let subtitle: String
      switch trendComponent {
      case .hour:
        title = AIUsageFormatting.monthDay(date) + " " + hour(date) + "–" + hour(range.upperBound)
        subtitle = context.text("detail_controls.24eb5e379e")
      case .month:
        title =
          context.text(
            "detail_controls.5d8c412f6f",
            [
              "arg0": String(describing: calendar.component(.year, from: date)),
              "arg1": String(describing: calendar.component(.month, from: date)),
            ])
        subtitle = context.text("detail_controls.ca3c684d88")
      default:
        title = AIUsageFormatting.date(date)
        subtitle = context.text("detail_controls.e12978bbcb")
      }
      let value = sum.inputUnitCost
      var tag: PaxAnalysisChart.Hover.Tag?
      var comparison = context.text("detail_controls.fbf3e9f054")
      if let value, let base = previous {
        comparison = context.text(
          "detail_controls.80333e9831", ["arg0": String(describing: base.title)])
        if base.value > 0 {
          let delta = ((value / base.value - 1) * 1000).rounded() / 10
          let percent = abs(delta).formatted(.number.precision(.fractionLength(0...1)))
          tag = .init(
            delta == 0
              ? context.text("detail_controls.a9105fc058")
              : (delta < 0
                ? context.text("detail_controls.ee94eb33f2")
                : context.text("detail_controls.86f79ba47b")) + percent + "%",
            color: delta == 0
              ? PaxTheme.Color.textSecondary
              : delta < 0 ? PaxTheme.DashboardColor.green : PaxTheme.DashboardColor.red)
        } else {
          comparison += context.text("detail_controls.3679b94b72")
        }
      }
      if let value { previous = (title, value) }
      let future = date > now
      let state =
        future
        ? context.text("detail_controls.41fb61fd28")
        : sum.input == 0 ? context.text("detail_controls.c620463c14") : comparison
      var details: [(label: String, value: String)] = [
        (state, ""),
        (context.text("detail_controls.3c94b4c759"), AIUsageFormatting.tokens(sum.input)),
        (context.text("detail_controls.2b9c682303"), price(sum.originalCost)),
      ]
      if trendComponent == .month, !future {
        let last = min(range.upperBound, interval.upperBound).addingTimeInterval(-1)
        details.append(
          (
            context.text("detail_controls.878000ecad"),
            AIUsageFormatting.monthDay(date) + "–" + AIUsageFormatting.monthDay(last)
          ))
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
