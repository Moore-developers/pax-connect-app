import PaxCore

extension AIUsagePeriod {
  func label(context: PresentationContext) -> String {
    let names: [Self: String] = [
      .today: "today", .yesterday: "yesterday", .week: "week", .month: "month", .year: "year",
      .custom: "custom",
    ]
    return context.text("ai_period." + (names[self] ?? "custom"))
  }
  func previousName(context: PresentationContext) -> String {
    let names: [Self: String] = [
      .today: "yesterday", .yesterday: "day_before", .week: "last_week", .month: "last_month",
      .year: "last_year", .custom: "previous",
    ]
    return context.text("ai_comparison." + (names[self] ?? "previous"))
  }
}
