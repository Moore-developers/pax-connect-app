import Foundation
import PaxCore
import PaxUI

/// Pure overview projections: no view code, so the segment, client and empty-state rules stay testable.
struct AIChangePill: Equatable {
  /// Direction drives the colour: `up` green, `down` amber, `flat` grey; `none` has no previous data.
  enum Tone: Equatable { case up, down, flat, none }
  let text: String
  let tone: Tone
  let tooltip: String

  /// "↓59%" / "↑3pt" against the previous period.
  /// A missing or zero base reads "—"; `previousText` is the formatted previous value for the tooltip.
  static func make(
    current: Double, previous: Double?, previousText: String, periodName: String,
    points: Bool = false
  ) -> AIChangePill {
    guard let previous, points || previous > 0 else {
      return .init(text: "—", tone: .none, tooltip: "上一时段无数据")
    }
    let delta = points ? (current - previous) * 100 : (current - previous) / previous * 100
    let rounded = Int(delta.rounded())
    let tip = "较" + periodName + " " + previousText
    guard rounded != 0 else { return .init(text: "持平", tone: .flat, tooltip: tip) }
    return .init(
      text: (rounded > 0 ? "↑" : "↓") + "\(abs(rounded))" + (points ? "pt" : "%"),
      tone: rounded > 0 ? .up : .down, tooltip: tip)
  }
}

struct AIOverviewSegment: Equatable {
  enum Kind: String { case cost, tokens, requests, cache }
  let kind: Kind
  let title: String
  let symbol: String
  let value: String
  let unit: String
  /// Bar segment fractions (0...1) in colour order; empty while there is no usage.
  let bar: [Double]
  /// Legend text per segment, e.g. the three percentages or amounts.
  let legend: [String]
  let pill: AIChangePill
  /// Name per legend item for the layered bars (cost, Token); empty for single-meaning legends.
  var legendNames: [String] {
    kind == .cost || kind == .tokens ? ["输入", "输出", "缓存"] : []
  }
  /// Legend items with their names, e.g. "输入 14%"; the full form for tooltips and wide cards.
  var namedLegend: [String] {
    legendNames.isEmpty ? legend : zip(legendNames, legend).map { $0 + " " + $1 }
  }
  var accessibilityID: String { "ai-summary-" + (kind == .tokens ? "Token 用量" : title) }
}

struct AIClientUsageRow: Equatable {
  let client: AIClient
  let serviceSource: AIServiceSource
  var configurationText: String {
    profileName
      + ([AIServiceSource.officialAPI, .relayAPI].contains(serviceSource)
        && profileName != serviceSource.label ? " · " + serviceSource.label : "")
      + (multiplier.map { " · " + $0 } ?? "")
  }
  let profileName: String
  let multiplier: String?
  /// stateLabel of any binding that is not 配置已写入; nil when applied or the service is unavailable.
  let state: String?
  /// stateLabel for the strip (always present while the service is available).
  let stateText: String
  /// True for the states that need action (see `AIUsageStore.bindingIssues`).
  let needsAction: Bool
  /// No Profile bound, or stateLabel 未配置: the only case where the Profile page offers 去配置.
  let unconfigured: Bool
  let value: String
  let billedCostText: String
  let shareText: String
  let requestsText: String
  let amount: Double
}

extension AIUsageSummary {
  /// The CLI with the most requests; ties resolve to the first CLI in `AIClient.allCases`.
  var topClient: AIClient? {
    var counts: [AIClient: Int] = [:]
    for request in requests { counts[request.client, default: 0] += 1 }
    for b in aggregateBuckets { counts[b.client, default: 0] += b.totals.count }
    var best: AIClient?
    for client in AIClient.allCases {
      guard let count = counts[client] else { continue }
      if count > best.flatMap({ counts[$0] }) ?? 0 { best = client }
    }
    return best
  }
}

extension AIUsageStore {
  enum OverviewPhase: Equatable { case loading, ready }

  /// Loading is only the first read: once a workspace exists, refreshes keep the content in place.
  var overviewPhase: OverviewPhase {
    if !preview { return data?.bucketsLoaded == false ? .loading : .ready }
    return Self.phase(
      loading: configuration.loading, hasWorkspace: !profiles.isEmpty || !bindings.isEmpty)
  }
  static func phase(loading: Bool, hasWorkspace: Bool) -> OverviewPhase {
    loading && !hasWorkspace ? .loading : .ready
  }
  var hasUsage: Bool { summary.count > 0 }
  /// Why there is nothing to chart or list; nil when usage exists.
  var usageEmptyReason: String? {
    if hasUsage { return nil }
    return preview ? "该时段无请求" : "该时段没有本地请求记录"
  }
  var isSingleDay: Bool {
    calendar.isDate(interval.lowerBound, inSameDayAs: interval.upperBound.addingTimeInterval(-1))
  }
  var trendMeta: String {
    let unit = trendComponent == .month ? "按月" : trendComponent == .hour ? "按小时" : "按天"
    return period.rawValue + " · " + unit
  }

  /// Current-period Profile / model groups; a retained selection stays available after its requests roll out.
  var trendGroups: [PaxUsageFilterGroup] {
    let rows: [AIUsageTrendFilter.Row] =
      preview
      ? selected.map {
        .init(
          profileID: $0.profileID, client: $0.client, profileName: $0.profileName,
          modelID: $0.modelID, count: 1, cost: $0.originalCost, tokens: $0.tokens,
          serviceSource: $0.serviceSource)
      }
      : (data?.buckets ?? []).map {
        .init(
          profileID: $0.profileID, client: $0.client, profileName: $0.profileName,
          modelID: $0.modelID, count: $0.totals.count, cost: $0.totals.costs.reduce(0, +),
          tokens: $0.totals.input + $0.totals.output, serviceSource: $0.serviceSource)
      }
    let named = Dictionary(
      profiles.map { ($0.id, $0.name) }, uniquingKeysWith: { first, _ in first })
    return AIUsageTrendFilter.groups(
      rows: rows, selection: trendFilter, profileName: { named[$0] },
      modelName: { [self] in model($0) })
  }

  var trendEmptyReason: String? {
    if trendFilter != .all, trendBuckets(filter: trendFilter).allSatisfy({ $0.1.count == 0 }) {
      return trendFilter.modelID == nil ? "该 Profile 在此时段无请求" : "该模型在此时段无请求"
    }
    return usageEmptyReason
  }

  var overviewSegments: [AIOverviewSegment] {
    let s = summary
    let usage = hasUsage
    let none = "—"
    let before = previousSummary
    let name = period.previousName
    func pill(
      _ now: Double, _ old: (AIUsageSummary) -> Double?, _ text: (Double) -> String,
      points: Bool = false
    ) -> AIChangePill {
      guard usage else { return .init(text: none, tone: .none, tooltip: "") }
      let previous = before.flatMap(old)
      return .make(
        current: now, previous: previous, previousText: previous.map(text) ?? "",
        periodName: name, points: points)
    }
    func split(_ text: String) -> (String, String) {
      guard let last = text.last, last.isLetter || last == "%" else { return (text, "") }
      return (String(text.dropLast()), String(last))
    }
    func shares(_ parts: [Double]) -> [Double] {
      let total = parts.reduce(0, +)
      return total > 0 ? parts.map { $0 / total } : []
    }
    let costs = s.officialCosts
    let costBar = usage ? shares([costs[0], costs[3], costs[1] + costs[2]]) : []
    let tokenParts = [s.ordinary, s.output, s.read].map(Double.init)
    let tokenValue = split(AIUsageFormatting.tokens(s.tokens))
    let cacheValue = split(AIUsageFormatting.percent(s.cache))
    return [
      .init(
        kind: .cost, title: "估算费用", symbol: "dollarsign.circle",
        value: usage ? s.officialCostLabel : none, unit: "", bar: costBar,
        legend: costBar.map { String(format: "%.0f%%", $0 * 100) },
        pill: pill(s.originalCost, { $0.originalCost }, { AIUsageFormatting.cost($0) })),
      .init(
        kind: .tokens, title: "Token", symbol: "square.stack.3d.up",
        value: usage ? tokenValue.0 : none, unit: usage ? tokenValue.1 : "",
        bar: usage ? shares(tokenParts) : [],
        legend: usage ? tokenParts.map { AIUsageFormatting.tokens(Int($0)) } : [],
        pill: pill(
          Double(s.tokens), { Double($0.tokens) }, { AIUsageFormatting.tokens(Int($0)) })),
      .init(
        kind: .requests, title: "请求数", symbol: "arrow.up.arrow.down",
        value: usage ? String(s.count) : none, unit: "",
        bar: usage ? shares([Double(s.success), Double(s.failed)]) : [],
        legend: usage ? ["成功 \(s.success) · 失败 \(s.failed)"] : [],
        pill: pill(Double(s.count), { Double($0.count) }, { String(Int($0)) })),
      .init(
        kind: .cache, title: "缓存命中率", symbol: "internaldrive",
        value: usage ? cacheValue.0 : none, unit: usage ? cacheValue.1 : "",
        bar: usage ? [s.cache ?? 0] : [],
        legend: usage
          ? ["读取 \(AIUsageFormatting.tokens(s.read)) / 输入 \(AIUsageFormatting.tokens(s.input))"]
          : [],
        pill: pill(
          s.cache ?? 0, { $0.cache }, { AIUsageFormatting.percent($0) }, points: true)),
    ]
  }

  /// Labels of states that need user action; every other non-applied label is only informational.
  static let actionLabels: Set<String> = ["配置需检查", "需要恢复"]
  /// Non-applied state label per CLI; empty while the configuration service is unavailable.
  var bindingStates: [AIClient: String] {
    guard configuration.available else { return [:] }
    var result: [AIClient: String] = [:]
    for client in configurableClients {
      let label = AIProfilesStore.stateLabel(bindings[client])
      if label != "配置已写入" { result[client] = label }
    }
    return result
  }
  /// Only broken or interrupted configuration needs action; absent Profiles are informational.
  var bindingIssues: [(client: AIClient, label: String)] {
    let states = bindingStates
    return AIClient.allCases.compactMap { client in
      states[client].flatMap { Self.actionLabels.contains($0) ? (client, $0) : nil }
    }
  }

  /// Latest request of a CLI over all retained history; nil when it was never used.
  func lastUsed(_ client: AIClient) -> Date? {
    preview
      ? previewRequests.filter { $0.client == client }.map(\.at).max() : data?.lastUsed[client]
  }
  static func idleText(lastUsed: Date?) -> String {
    "本时段无请求" + (lastUsed.map { " · 最近使用 " + AIUsageFormatting.monthDay($0) } ?? "")
  }

  func clientRows(cost: Bool) -> [AIClientUsageRow] {
    let sums = AIClient.allCases.map { client in
      (client, usageSummary(client: client))
    }
    let amounts = sums.map { cost ? $0.1.originalCost : Double($0.1.tokens) }
    let total = amounts.reduce(0, +)
    let states = bindingStates
    return zip(sums, amounts).map { pair, amount in
      let (client, sum) = pair
      let binding = bindings[client]
      let profile = profiles.first { $0.id == binding?.profileID }
      let source =
        binding?.serviceSource == .unrecorded
        ? profile?.serviceSource ?? .unknown : binding?.serviceSource ?? .unknown
      let used = sum.count > 0
      return AIClientUsageRow(
        client: client, serviceSource: source,
        profileName: client == .harness
          ? "只读用量"
          : source == .officialAccount
            ? "官方账号"
            : profile?.name ?? source.label,
        multiplier: source == .officialAccount
          ? nil
          : client == .harness
            ? AIUsageFormatting.multiplier(1)
            : profile.map { AIUsageFormatting.multiplier($0.multiplier) },
        state: states[client],
        stateText: client == .harness
          ? "只读" : configuration.available ? AIProfilesStore.stateLabel(binding) : "配置服务不可用",
        needsAction: states[client].map(Self.actionLabels.contains) ?? false,
        unconfigured: client != .harness
          && binding?.state == "missing",
        value: used
          ? (cost
            ? (sum.officialCostLabel + (sum.accountOnly ? "（API 等值）" : "（官方）"))
            : AIUsageFormatting.tokens(sum.tokens)) : "—",
        billedCostText: used ? sum.costLabel : "—",
        shareText: used && total > 0 ? AIUsageFormatting.percent(amount / total) : "",
        requestsText: used
          ? "\(sum.count) 请求 · " + AIUsageFormatting.percent(sum.successRate)
          : Self.idleText(lastUsed: lastUsed(client)),
        amount: used ? amount : 0)
    }
  }

  /// Input / output / cache layers per bucket, in the legend's order. Cache is read + write.
  /// Tokens are scaled to K or M so the shared chart's two-decimal readout stays legible.
  func trendSeries(cost: Bool) -> (samples: [PaxChartSample], unit: String) {
    let rows = trendBuckets(filter: trendFilter)
    func layers(_ sum: AIUsageSummary) -> [Double] {
      if cost {
        let c = sum.officialCosts
        return [c[0], c[3], c[1] + c[2]]
      }
      return [Double(sum.ordinary), Double(sum.output), Double(sum.read + sum.write)]
    }
    let peak = rows.map { layers($0.1).reduce(0, +) }.max() ?? 0
    let scale = cost ? 1 : peak >= 1_000_000 ? 1_000_000.0 : peak >= 1_000 ? 1_000.0 : 1
    let unit = cost ? "USD" : scale == 1_000_000 ? "M Token" : scale == 1_000 ? "K Token" : "Token"
    let ranges = trendIntervals
    let samples = rows.enumerated().map { index, row in
      let (date, sum) = row
      let dateLabel =
        trendFillsWidth
        ? String(
          format: "%02d/%02d", calendar.component(.month, from: date),
          calendar.component(.day, from: date))
        : AIUsageFormatting.date(date)
      var sample = PaxChartSample(
        period == .year
          ? "\(calendar.component(.month, from: date))月"
          : trendComponent == .hour
            ? String(format: "%02d:00", calendar.component(.hour, from: date))
            : dateLabel,
        values: layers(sum).map { $0 / scale }, at: date)
      let range = ranges[index]
      if period == .year {
        let last = min(range.upperBound, interval.upperBound).addingTimeInterval(-1)
        let dates =
          range.lowerBound >= interval.upperBound
          ? "尚未开始"
          : AIUsageFormatting.date(date) + "–" + AIUsageFormatting.date(last)
        sample.readout = ["\(calendar.component(.year, from: date))年\(sample.label) · \(dates)"]
      } else {
        sample.readout = [trendComponent == .day ? AIUsageFormatting.date(date) : sample.label]
      }
      let values = sample.values.map { $0.formatted(.number.precision(.fractionLength(2))) }
      sample.readout? += zip(["输入", "输出", "缓存"], values).map { "\($0.0) \($0.1) \(unit)" }
      let total = sample.values.reduce(0, +).formatted(.number.precision(.fractionLength(2)))
      sample.readout?.append("合计 \(total) \(unit)")
      return sample
    }
    return (samples, unit)
  }
}
