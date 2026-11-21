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
    points: Bool = false, context: PresentationContext = .chinese
  ) -> AIChangePill {
    guard let previous, points || previous > 0 else {
      return .init(text: "—", tone: .none, tooltip: context.text("ai_projection.cb54f74ee9"))
    }
    let delta = points ? (current - previous) * 100 : (current - previous) / previous * 100
    let rounded = Int(delta.rounded())
    let tip = context.text("ai_projection.3c09e00162") + periodName + " " + previousText
    guard rounded != 0 else {
      return .init(text: context.text("ai_projection.a9105fc058"), tone: .flat, tooltip: tip)
    }
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
  var legendNames: [String] = []
  /// Legend items with their names, e.g. "输入 14%"; the full form for tooltips and wide cards.
  var namedLegend: [String] {
    legendNames.isEmpty ? legend : zip(legendNames, legend).map { $0 + " " + $1 }
  }
  var accessibilityID: String {
    "ai-summary-"
      + [Kind.cost: "估算费用", .tokens: "Token 用量", .requests: "请求数", .cache: "缓存命中率"][
        kind, default: kind.rawValue]
  }
}

struct AIClientUsageRow: Equatable {
  let client: AIClient
  let serviceSource: AIServiceSource
  var configurationText: String { configurationText(context: .chinese) }
  func configurationText(context: PresentationContext) -> String {
    profileName
      + ([AIServiceSource.officialAPI, .relayAPI].contains(serviceSource)
        && profileName != serviceSource.label(context: context)
        ? " · " + serviceSource.label(context: context) : "")
      + (multiplier.map { " · " + $0 } ?? "")
  }
  let profileName: String
  let multiplier: String?
  /// stateLabel of any binding that is not 配置已写入; nil when applied or the service is unavailable.
  let state: String?
  /// stateLabel for the strip (always present while the service is available).
  var stateCode = ""
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
  var usageEmptyReason: String? { usageEmptyReason(context: .chinese) }
  func usageEmptyReason(context: PresentationContext) -> String? {
    if hasUsage { return nil }
    return preview
      ? context.text("ai_projection.f2061fcf00") : context.text("ai_projection.390d0608f8")
  }
  var isSingleDay: Bool {
    calendar.isDate(interval.lowerBound, inSameDayAs: interval.upperBound.addingTimeInterval(-1))
  }
  var trendMeta: String { trendMeta(context: .chinese) }
  func trendMeta(context: PresentationContext) -> String {
    let unit =
      trendComponent == .month
      ? context.text("ai_projection.ca3c684d88")
      : trendComponent == .hour
        ? context.text("ai_projection.24eb5e379e") : context.text("ai_projection.e12978bbcb")
    return period.label(context: context) + " · " + unit
  }

  /// Current-period Profile / model groups; a retained selection stays available after its requests roll out.
  func trendGroups(context: PresentationContext) -> [PaxUsageFilterGroup] {
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
      rows: rows, selection: trendFilter, context: context, profileName: { named[$0] },
      modelName: { [self] in model($0) })
  }

  var trendEmptyReason: String? { trendEmptyReason(context: .chinese) }
  func trendEmptyReason(context: PresentationContext) -> String? {
    if trendFilter != .all, trendBuckets(filter: trendFilter).allSatisfy({ $0.1.count == 0 }) {
      return trendFilter.modelID == nil
        ? context.text("ai_projection.b3e35aa89a") : context.text("ai_projection.74f03d203a")
    }
    return usageEmptyReason(context: context)
  }

  var overviewSegments: [AIOverviewSegment] { overviewSegments(context: .chinese) }
  func overviewSegments(context: PresentationContext) -> [AIOverviewSegment] {
    let s = summary
    let usage = hasUsage
    let none = "—"
    let before = previousSummary
    let name = period.previousName(context: context)
    func pill(
      _ now: Double, _ old: (AIUsageSummary) -> Double?, _ text: (Double) -> String,
      points: Bool = false
    ) -> AIChangePill {
      guard usage else { return .init(text: none, tone: .none, tooltip: "") }
      let previous = before.flatMap(old)
      return .make(
        current: now, previous: previous, previousText: previous.map(text) ?? "",
        periodName: name, points: points, context: context)
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
    var segments: [AIOverviewSegment] = [
      .init(
        kind: .cost, title: context.text("ai_projection.dd4e402aba"), symbol: "dollarsign.circle",
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
        kind: .requests, title: context.text("ai_projection.855754c132"),
        symbol: "arrow.up.arrow.down",
        value: usage ? String(s.count) : none, unit: "",
        bar: usage ? shares([Double(s.success), Double(s.failed)]) : [],
        legend: usage
          ? [
            context.text(
              "ai_projection.f2d046a515",
              ["arg0": String(describing: s.success), "arg1": String(describing: s.failed)])
          ] : [],
        pill: pill(Double(s.count), { Double($0.count) }, { String(Int($0)) })),
      .init(
        kind: .cache, title: context.text("ai_projection.13b61c5f69"), symbol: "internaldrive",
        value: usage ? cacheValue.0 : none, unit: usage ? cacheValue.1 : "",
        bar: usage ? [s.cache ?? 0] : [],
        legend: usage
          ? [
            context.text(
              "ai_projection.b413ebc7d2",
              [
                "arg0": String(describing: AIUsageFormatting.tokens(s.read)),
                "arg1": String(describing: AIUsageFormatting.tokens(s.input)),
              ])
          ]
          : [],
        pill: pill(
          s.cache ?? 0, { $0.cache }, { AIUsageFormatting.percent($0) }, points: true)),
    ]
    for index in segments.indices where [.cost, .tokens].contains(segments[index].kind) {
      segments[index].legendNames = ["input", "output", "cache"].map {
        context.text("usage.legend." + $0)
      }
    }
    return segments
  }

  /// Labels of states that need user action; every other non-applied label is only informational.
  static let actionStates: Set<String> = ["invalid", "recovery_required"]
  /// Non-applied state label per CLI; empty while the configuration service is unavailable.
  var bindingStates: [AIClient: String] { bindingStates(context: .chinese) }
  func bindingStates(context: PresentationContext) -> [AIClient: String] {
    guard configuration.available else { return [:] }
    var result: [AIClient: String] = [:]
    for client in configurableClients {
      let label = AIProfilesStore.stateLabel(bindings[client], context: context)
      if bindings[client]?.state != "applied" { result[client] = label }
    }
    return result
  }
  /// Only broken or interrupted configuration needs action; absent Profiles are informational.
  var bindingIssues: [(client: AIClient, label: String)] { bindingIssues(context: .chinese) }
  func bindingIssues(context: PresentationContext) -> [(client: AIClient, label: String)] {
    let states = bindingStates(context: context)
    return AIClient.allCases.compactMap { client in
      states[client].flatMap {
        Self.actionStates.contains(bindings[client]?.state ?? "") ? (client, $0) : nil
      }
    }
  }

  /// Latest request of a CLI over all retained history; nil when it was never used.
  func lastUsed(_ client: AIClient) -> Date? {
    preview
      ? previewRequests.filter { $0.client == client }.map(\.at).max() : data?.lastUsed[client]
  }
  static func idleText(lastUsed: Date?, context: PresentationContext = .chinese) -> String {
    context.text("ai_projection.c41b0972aa")
      + (lastUsed.map { context.text("ai_projection.3219f9714d") + AIUsageFormatting.monthDay($0) }
        ?? "")
  }

  func clientRows(cost: Bool, context: PresentationContext = .chinese) -> [AIClientUsageRow] {
    let sums = AIClient.allCases.map { client in
      (client, usageSummary(client: client))
    }
    let amounts = sums.map { cost ? $0.1.originalCost : Double($0.1.tokens) }
    let total = amounts.reduce(0, +)
    let states = bindingStates(context: context)
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
          ? context.text("ai_projection.2736dc3b8c")
          : source == .officialAccount
            ? context.text("ai_projection.57adc05c18")
            : profile?.name ?? source.label(context: context),
        multiplier: source == .officialAccount
          ? nil
          : client == .harness
            ? AIUsageFormatting.multiplier(1)
            : profile.map { AIUsageFormatting.multiplier($0.multiplier) },
        state: states[client],
        stateCode: binding?.state ?? "",
        stateText: client == .harness
          ? context.text("ai_projection.3b5ec3533b")
          : configuration.available
            ? AIProfilesStore.stateLabel(binding, context: context)
            : context.text("ai_projection.2c72fe1c46"),
        needsAction: Self.actionStates.contains(binding?.state ?? ""),
        unconfigured: client != .harness
          && binding?.state == "missing",
        value: used
          ? (cost
            ? (sum.officialCostLabel
              + (sum.accountOnly
                ? context.text("ai_projection.3bfc15f873")
                : context.text("ai_projection.4fef922940")))
            : AIUsageFormatting.tokens(sum.tokens)) : "—",
        billedCostText: used ? sum.costLabel : "—",
        shareText: used && total > 0 ? AIUsageFormatting.percent(amount / total) : "",
        requestsText: used
          ? context.text("ai_projection.2f1240019d", ["arg0": String(describing: sum.count)])
            + AIUsageFormatting.percent(sum.successRate)
          : Self.idleText(lastUsed: lastUsed(client), context: context),
        amount: used ? amount : 0)
    }
  }

  /// Input / output / cache layers per bucket, in the legend's order. Cache is read + write.
  /// Tokens are scaled to K or M so the shared chart's two-decimal readout stays legible.
  func trendSeries(cost: Bool, context: PresentationContext = .chinese) -> (
    samples: [PaxChartSample], unit: String
  ) {
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
          ? context.text(
            "ai_projection.9b0d52d667",
            ["arg0": String(describing: calendar.component(.month, from: date))])
          : trendComponent == .hour
            ? String(format: "%02d:00", calendar.component(.hour, from: date))
            : dateLabel,
        values: layers(sum).map { $0 / scale }, at: date)
      let range = ranges[index]
      if period == .year {
        let last = min(range.upperBound, interval.upperBound).addingTimeInterval(-1)
        let dates =
          range.lowerBound >= interval.upperBound
          ? context.text("ai_projection.541e655330")
          : AIUsageFormatting.date(date) + "–" + AIUsageFormatting.date(last)
        sample.readout = [
          context.text(
            "ai_projection.550a92c1a0",
            [
              "arg0": String(describing: calendar.component(.year, from: date)),
              "arg1": String(describing: sample.label), "arg2": String(describing: dates),
            ])
        ]
      } else {
        sample.readout = [trendComponent == .day ? AIUsageFormatting.date(date) : sample.label]
      }
      let values = sample.values.map { $0.formatted(.number.precision(.fractionLength(2))) }
      sample.readout? += zip(
        [
          context.text("ai_projection.2087c777c0"), context.text("ai_projection.fb04addb4c"),
          context.text("ai_projection.e3ca04a9cb"),
        ], values
      ).map { "\($0.0) \($0.1) \(unit)" }
      let total = sample.values.reduce(0, +).formatted(.number.precision(.fractionLength(2)))
      sample.readout?.append(
        context.text(
          "ai_projection.82a0bc6125",
          ["arg0": String(describing: total), "arg1": String(describing: unit)]))
      return sample
    }
    return (samples, unit)
  }
}
