import Foundation
import PaxCore
import PaxUI

/// 模型用量两卡的 App 端聚合：来自 `QueryAIUsageBuckets` 的小时桶，按本地自然日、模型、CLI 汇总。
/// 费用 = 桶的 `costs` 合计（1× 官方原价），Token = 输入 + 输出（输入已含缓存），口径同 P07 / 采集 §3。
enum DashboardUsageProjection {
  static let dayCount = 10
  /// 近 10 日（含今日）的汇总输入。
  struct Day {
    var start: Date
    var totals: AIUsageTotals
    var hasData: Bool { totals.count > 0 }
  }

  static func clientName(_ client: AIClient) -> String {
    client == .harness ? "DSH" : client.rawValue
  }

  static func tokens(_ totals: AIUsageTotals) -> Int { totals.input + totals.output }

  /// 柱顶金额：取整，不留小数；不足 $0.5 写「<$1」，避免有用量却显示 $0。
  static func wholeMoney(_ value: Double) -> String {
    value > 0 && value < 0.5
      ? "<$1" : String(format: "$%.0f", locale: Locale(identifier: "en_US_POSIX"), value)
  }

  /// 柱顶 Token 数：取整到 K / M / B。
  static func wholeTokens(_ value: Double) -> String {
    for (scale, suffix) in [(1_000_000_000.0, "B"), (1_000_000.0, "M"), (1_000.0, "K")]
    where value >= scale {
      return String(
        format: "%.0f%@", locale: Locale(identifier: "en_US_POSIX"), value / scale, suffix)
    }
    return String(Int(value.rounded()))
  }

  static func money(_ value: Double) -> String {
    AIUsageFormatting.cost(value, precision: 2)
  }

  static func days(_ buckets: [AIUsageBucket], now: Date, calendar: Calendar) -> [Day] {
    let today = calendar.startOfDay(for: now)
    let starts = ((1 - dayCount)...0).compactMap {
      calendar.date(byAdding: .day, value: $0, to: today)
    }
    var days = starts.map { Day(start: $0, totals: AIUsageTotals()) }
    for bucket in buckets {
      guard let index = days.indices.last(where: { days[$0].start <= bucket.at }),
        let end = calendar.date(byAdding: .day, value: 1, to: days[index].start), bucket.at < end
      else { continue }
      days[index].totals.add(bucket.totals)
    }
    return days
  }

  /// 缺采集 / 读取失败 / 需重建时的整卡空态标题；正常返回 nil。
  static func unavailable(
    state: AIUsageCollectionState?, failed: Bool, context: PresentationContext = .chinese
  ) -> String? {
    if failed { return context.text("dashboard_value.usage_failed") }
    if state?.rebuildRequired == true { return context.text("dashboard_value.rebuild") }
    if let code = state?.errorCode, !code.isEmpty {
      return context.text("dashboard_value.collection_unavailable")
    }
    return nil
  }

  /// 一个口径的单日数值与按天柱标签：费用取桶的原价分项合计，Token 取输入 + 输出。
  private struct Metric {
    let value: (Day) -> Double
    let label: (Double) -> String
    /// 柱顶金额：不留小数。
    let whole: (Double) -> String
    /// 输入 / 输出 / 缓存三段，与柱内分色、图例同序。
    let parts: (Day) -> [Double]
  }

  private static var costMetric: Metric {
    Metric(
      value: { $0.totals.costs.reduce(0, +) }, label: money, whole: wholeMoney,
      parts: { day in
        let costs = day.totals.costs
        return [costs[0], costs[3], costs[1] + costs[2]]
      })
  }

  private static var tokenMetric: Metric {
    Metric(
      value: { Double(tokens($0.totals)) }, label: { AIUsageFormatting.tokens(Int($0.rounded())) },
      whole: wholeTokens,
      parts: { day in
        let t = day.totals
        // 输入已含缓存读写：按「非缓存输入 / 输出 / 缓存」拆，三项相加等于合计。
        let cache = t.read + t.write
        return [Double(max(0, t.input - cache)), Double(t.output), Double(cache)]
      })
  }

  private static func series(
    _ metric: Metric, days: [Day], facts: [PaxModelTrendModel.Fact], calendar: Calendar,
    context: PresentationContext = .chinese
  ) -> PaxModelTrendModel.Series {
    let today = days[dayCount - 1]
    let yesterday = days[dayCount - 2]
    var result = PaxModelTrendModel.Series(facts: facts)
    result.days = days.map { day in
      let parts = calendar.dateComponents([.month, .day], from: day.start)
      let label = String(format: "%02d-%02d", parts.month ?? 0, parts.day ?? 0)
      let number = metric.value(day)
      let split = metric.parts(day)
      var sample = PaxChartSample(
        day.start == today.start ? context.text("dashboard_value.today") : label,
        values: day.hasData ? split : [], at: day.start)
      sample.amountLabel =
        day.hasData ? (day.start == today.start ? metric.label(number) : metric.whole(number)) : "—"
      sample.readout =
        day.hasData
        ? [label]
          + zip(
            [
              context.text("dashboard_value.input"), context.text("dashboard_value.output"),
              context.text("dashboard_value.cache"),
            ], split
          ).map { $0 + " " + metric.label($1) }
          + [context.text("dashboard_value.total_prefix") + metric.label(number)]
        : [label, context.text("dashboard_value.missing_sample")]
      return sample
    }
    result.value = today.hasData ? metric.label(metric.value(today)) : "—"
    result.yesterday = yesterday.hasData ? metric.label(metric.value(yesterday)) : "—"
    let measured = days.filter(\.hasData)
    result.average = metric.label(
      measured.reduce(0) { $0 + metric.value($1) } / Double(measured.count))
    return result
  }

  static func trend(
    buckets: [AIUsageBucket], state: AIUsageCollectionState?, failed: Bool = false,
    updatedAt: Date?, now: Date, filter: PaxUsageFilter = .all, calendar: Calendar = .current,
    context: PresentationContext = .chinese
  ) -> PaxModelTrendModel {
    var model = PaxModelTrendModel(selection: filter)
    let clock = updatedAt.map { DashboardFormat.clock($0, calendar: calendar) } ?? "—"
    model.updated = context.text("dashboard_value.as_of") + clock
    if let text = unavailable(state: state, failed: failed, context: context) {
      model.empty = text
      return model
    }
    let allDays = days(buckets, now: now, calendar: calendar)
    guard allDays.contains(where: \.hasData) else {
      model.empty = context.text("dashboard_value.no_week_requests")
      return model
    }
    model.groups = filterGroups(
      buckets, now: now, calendar: calendar, selection: filter, context: context)
    let filtered = buckets.filter {
      AIUsageTrendFilter.matches(
        filter, profileID: $0.profileID, client: $0.client, modelID: $0.modelID,
        serviceSource: $0.serviceSource)
    }
    let days = filter == .all ? allDays : days(filtered, now: now, calendar: calendar)
    guard days.contains(where: \.hasData) else {
      model.selectionEmpty =
        filter.modelID == nil
        ? context.text("dashboard_value.profile_no_requests")
        : context.text("dashboard_value.model_no_requests")
      return model
    }
    let today = days[dayCount - 1]
    // 附加行保留今日值与有效天数；主力 CLI 以整个十日窗口汇总。
    func facts(other: PaxModelTrendModel.Fact) -> [PaxModelTrendModel.Fact] {
      let main = topClient(buckets: filtered, day: days[0], calendar: calendar, dayCount: dayCount)
      guard today.hasData else {
        var rows: [PaxModelTrendModel.Fact] = [
          .init(
            context.text("dashboard_value.today_requests"), context.text("dashboard_value.none")),
          .init(
            context.text("dashboard_value.has_data"),
            context.text(
              "dashboard_value.days_with_data", ["count": days.filter(\.hasData).count.formatted()])
          ),
        ]
        if let main { rows.append(.init(main.name, "\(main.percent)%")) }
        return rows
      }
      let count =
        AIUsageFormatting.count(today.totals.count) + context.text("dashboard_value.request_suffix")
      var rows = [
        PaxModelTrendModel.Fact(context.text("dashboard_value.today_requests"), count), other,
      ]
      if let top = main {
        rows.append(
          .init(top.name, "\(top.percent)%"))
      }
      return rows
    }
    model.cost = series(
      costMetric, days: days,
      facts: facts(
        other: .init(
          context.text("dashboard_value.today_tokens"),
          AIUsageFormatting.tokens(tokens(today.totals)))),
      calendar: calendar, context: context)
    model.tokens = series(
      tokenMetric, days: days,
      facts: facts(
        other: .init(
          context.text("dashboard_value.today_cost"), money(today.totals.costs.reduce(0, +)))),
      calendar: calendar, context: context)
    model.currentIndex = dayCount - 1
    return model
  }

  /// 近 10 日有请求的 Profile 与模型分组；Profile 名取桶里的名称。
  private static func filterGroups(
    _ buckets: [AIUsageBucket], now: Date, calendar: Calendar, selection: PaxUsageFilter,
    context: PresentationContext
  ) -> [PaxUsageFilterGroup] {
    let today = calendar.startOfDay(for: now)
    guard let start = calendar.date(byAdding: .day, value: 1 - dayCount, to: today),
      let end = calendar.date(byAdding: .day, value: 1, to: today)
    else { return [] }
    let rows = buckets.filter { $0.at >= start && $0.at < end && !$0.profileID.isEmpty }.map {
      AIUsageTrendFilter.Row(
        profileID: $0.profileID, client: $0.client, profileName: $0.profileName,
        modelID: $0.modelID, count: $0.totals.count, cost: $0.totals.costs.reduce(0, +),
        tokens: tokens($0.totals), serviceSource: $0.serviceSource)
    }
    return AIUsageTrendFilter.groups(
      rows: rows, selection: selection, context: context, profileName: { _ in nil })
  }

  /// 指定天数内占比最高的 CLI：按费用，费用全为 0 时按请求数。
  private static func topClient(
    buckets: [AIUsageBucket], day: Day, calendar: Calendar, dayCount: Int = 1
  ) -> (name: String, percent: Int)? {
    guard let end = calendar.date(byAdding: .day, value: dayCount, to: day.start) else {
      return nil
    }
    var byCost: [AIClient: Double] = [:]
    var byCount: [AIClient: Int] = [:]
    for b in buckets where b.at >= day.start && b.at < end {
      byCost[b.client, default: 0] += b.totals.costs.reduce(0, +)
      byCount[b.client, default: 0] += b.totals.count
    }
    let useCost = byCost.values.reduce(0, +) > 0
    let weights = AIClient.allCases.map {
      ($0, useCost ? byCost[$0] ?? 0 : Double(byCount[$0] ?? 0))
    }
    let total = weights.reduce(0) { $0 + $1.1 }
    guard total > 0, let best = weights.max(by: { $0.1 < $1.1 }) else { return nil }
    return (clientName(best.0), Int((best.1 / total * 100).rounded()))
  }

  struct ModelRow {
    var model: String
    var cost = 0.0
    var tokens = 0
    var count = 0
    var clients: Set<AIClient> = []
  }

  /// 含今日的指定天数按模型汇总，跨 CLI 合并；费用降序，同费用按 Token 降序、再按名称。
  static func models(
    _ buckets: [AIUsageBucket], now: Date, calendar: Calendar, dayCount: Int = 1
  ) -> [ModelRow] {
    let today = calendar.startOfDay(for: now)
    guard let start = calendar.date(byAdding: .day, value: 1 - dayCount, to: today),
      let end = calendar.date(byAdding: .day, value: 1, to: today)
    else { return [] }
    var rows: [String: ModelRow] = [:]
    for b in buckets
    where b.at >= start && b.at < end && b.totals.count > 0
      && !AIUsageFormatting.isSyntheticModel(b.modelID)
    {
      var row = rows[b.modelID] ?? ModelRow(model: b.modelID)
      row.cost += b.totals.costs.reduce(0, +)
      row.tokens += tokens(b.totals)
      row.count += b.totals.count
      row.clients.insert(b.client)
      rows[b.modelID] = row
    }
    return rows.values.sorted {
      ($0.cost, $0.tokens, $1.model) > ($1.cost, $1.tokens, $0.model)
    }
  }

  static func rank(
    buckets: [AIUsageBucket], state: AIUsageCollectionState?, failed: Bool = false, now: Date,
    filter: PaxUsageFilter = .all, calendar: Calendar = .current,
    context: PresentationContext = .chinese
  ) -> PaxModelRankModel {
    var model = PaxModelRankModel()
    if let text = unavailable(state: state, failed: failed, context: context) {
      model.empty = text
      return model
    }
    let filtered = buckets.filter {
      AIUsageTrendFilter.matches(
        filter, profileID: $0.profileID, client: $0.client, modelID: $0.modelID,
        serviceSource: $0.serviceSource)
    }
    let rows = models(filtered, now: now, calendar: calendar, dayCount: dayCount)
    guard !rows.isEmpty else {
      model.empty = context.text("dashboard_value.no_week_requests")
      return model
    }
    let totalCost = rows.reduce(0) { $0 + $1.cost }
    let totalTokens = rows.reduce(0) { $0 + $1.tokens }
    model.cost = money(totalCost)
    model.caption = context.text(
      "dashboard_value.models_caption",
      ["tokens": AIUsageFormatting.tokens(totalTokens), "count": rows.count.formatted()])
    func projected(byCost: Bool) -> [PaxModelRankModel.Row] {
      let sorted =
        byCost
        ? rows
        : rows.sorted {
          ($0.tokens, $0.cost, $1.model) > ($1.tokens, $1.cost, $0.model)
        }
      let whole = byCost ? totalCost : Double(totalTokens)
      return sorted.prefix(PaxModelRankModel.rowLimit).map { row in
        let fraction = whole > 0 ? (byCost ? row.cost : Double(row.tokens)) / whole : 0
        let clients = AIClient.allCases.filter { row.clients.contains($0) }
        return PaxModelRankModel.Row(
          name: row.model, clients: clients.map(clientName).joined(separator: " / "),
          tokens: AIUsageFormatting.tokens(row.tokens), cost: money(row.cost),
          percent: String(
            format: "%.1f%%", locale: Locale(identifier: "en_US_POSIX"), fraction * 100),
          fraction: fraction)
      }
    }
    model.rows = projected(byCost: true)
    model.tokenRows = projected(byCost: false)
    return model
  }
}
