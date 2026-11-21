import PaxCore
import PaxUI

func dateRangeCopy(_ context: PresentationContext) -> PaxDateRangeCopy {
  var copy = PaxDateRangeCopy()
  copy.apply = context.text("calendar.apply")
  copy.cancel = context.text("common.cancel")
  copy.hint = context.text("calendar.hint")
  copy.selectPrefix = context.text("calendar.select")
  copy.spanHint = { context.text("calendar.span", ["count": String($0)]) }
  copy.presetTitles = [
    .today: context.text("ai_period.today"), .yesterday: context.text("ai_period.yesterday"),
    .week: context.text("ai_period.week"), .month: context.text("ai_period.month"),
  ]
  copy.navigation = [
    -12: context.text("calendar.previous_year"), 12: context.text("calendar.next_year"),
    -1: context.text("calendar.previous_month"), 1: context.text("calendar.next_month"),
  ]
  copy.weekdays = (0..<7).map { context.text("calendar.weekday." + String($0)) }
  copy.monthTitle = { context.text("calendar.month", ["year": String($0), "month": String($1)]) }
  return copy
}
