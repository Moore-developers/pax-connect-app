import Foundation
import PaxCore
import PaxUI

extension AIUsageStore {
  func usageSummary(
    model: String? = nil, profile: String? = nil, client: AIClient? = nil, lifetime: Bool = false
  ) -> AIUsageSummary {
    if preview {
      return AIUsageSummary(
        requests: (lifetime ? requests : selected).filter {
          (model == nil || $0.modelID == model) && (profile == nil || $0.profileID == profile)
            && (client == nil || $0.client == client)
        })
    }
    let rows = lifetime ? data?.lifetime ?? [] : data?.buckets ?? []
    return AIUsageSummary(
      buckets: rows.filter {
        (model == nil || $0.modelID == model) && (profile == nil || $0.profileID == profile)
          && (client == nil || $0.client == client)
      })
  }
  var trendComponent: Calendar.Component {
    switch period {
    case .year: .month
    case .week, .month: .day
    default: isSingleDay ? .hour : .day
    }
  }
  var trendFillsWidth: Bool {
    period == .week || period == .month || (period == .custom && trendIntervals.count > 2)
  }
  var trendIntervals: [Range<Date>] {
    let range = period.chartInterval(now: now, start: start, end: end, calendar: calendar)
    let component = trendComponent
    var result: [Range<Date>] = []
    var lower = range.lowerBound
    while lower < range.upperBound {
      guard let next = calendar.date(byAdding: component, value: 1, to: lower) else { break }
      let upper = min(next, range.upperBound)
      result.append(lower..<upper)
      lower = upper
    }
    return result
  }
  func trendBuckets(filter: PaxUsageFilter = .all) -> [(Date, AIUsageSummary)] {
    let requests =
      preview
      ? selected.filter {
        AIUsageTrendFilter.matches(
          filter, profileID: $0.profileID, client: $0.client, modelID: $0.modelID)
      } : []
    let rows = (data?.buckets ?? []).filter {
      AIUsageTrendFilter.matches(
        filter, profileID: $0.profileID, client: $0.client, modelID: $0.modelID)
    }
    return trendIntervals.map { range in
      (
        range.lowerBound,
        preview
          ? AIUsageSummary(requests: requests.filter { range.contains($0.at) })
          : AIUsageSummary(buckets: rows.filter { range.contains($0.at) })
      )
    }
  }
  /// Ratio of totals in each displayed time bucket, in USD per million input tokens, including cache.
  var trendUnitCosts: [Double?] {
    trendBuckets(filter: trendFilter).map { $0.1.inputUnitCost }
  }
  /// Totals of the comparison period; nil when it has no requests.
  var previousSummary: AIUsageSummary? {
    let range = previousInterval
    let sum =
      preview
      ? AIUsageSummary(requests: previewRequests.filter { range.contains($0.at) })
      : AIUsageSummary(buckets: data?.previous ?? [])
    return sum.count > 0 ? sum : nil
  }
  func syncUsage() {
    guard !preview, let data else { return }
    let now = self.now
    let range = period.interval(now: now, start: start, end: end, calendar: calendar)
    data.sessions?.select(
      range: range,
      comparison: period.sessionComparisonInterval(
        now: now, start: start, end: end, calendar: calendar))
    var filter = AIUsageFilter()
    filter.minOfficialCostMicro = minimumCost
    filter.maxOfficialCostMicro = maximumCost
    filter.result = logResult
    filter.profile = logProfileID ?? ""
    filter.model = logModelID ?? ""
    filter.cli = logClient?.code ?? ""
    filter.page = logPage
    data.select(
      range: range, granularity: isSingleDay ? "hour" : "day", filter: filter,
      previous: previousInterval)
  }
  func setUsagePart(_ part: AIUsageDataStore.Part?, fetchNow: Bool) {
    syncUsage()
    data?.setActive(part, fetchNow: fetchNow)
  }
  func setLogPage(_ value: Int) {
    logPage = max(0, value)
    syncUsage()
  }
  var costText: String { summary.costLabel }
}
