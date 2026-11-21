import Foundation
import PaxCore
import PaxUI

/// Display values for one Profile record row; built from the same binding projection as the overview.
struct AIProfileRecord: Equatable {
  let id: String
  let isNative: Bool
  let nativeCLI: AIClient?
  let statusText: String
  let statusTone: PaxBadge.Tone
  let source: String
  let missingKey: Bool
  let name: String
  let multiplier: String
  /// "20 个模型 · 用过 4 个", or a state text when no catalog exists.
  let modelsLine: String
  /// "最常用 <model>" by cost in the period; nil without usage.
  let topModelLine: String?
  /// Header of the cost column: the period name + 费用.
  let costHeader: String
  /// The four numeric columns; "—" in all of them when the period has no usage.
  let costText: String
  let channelCostText: String
  let tokensText: String
  let requestsText: String
  let cacheText: String
}

/// Rows 3 and 4 of a CLI binding card.
struct AICLICardLines: Equatable {
  /// A piece of a card line; only `value` roles are coloured, labels stay secondary.
  struct Run: Equatable {
    enum Role: Equatable { case label, cost, officialCost, token, request, cache }
    let text: String
    let role: Role
  }
  let usageRuns: [Run]
  let recentRuns: [Run]
  let costRuns: [Run]
  var usage: String { usageRuns.map(\.text).joined() }
  var recent: String { recentRuns.map(\.text).joined() }
  init(usage: [Run], recent: [Run], cost: [Run] = []) {
    usageRuns = usage
    recentRuns = recent
    costRuns = cost
  }
}

extension AIUsageStore {
  static func tone(forState label: String) -> PaxBadge.Tone {
    label == "applied" ? .healthy : actionStates.contains(label) ? .warning : .neutral
  }

  /// A Profile takes the most urgent state among the CLIs bound to it; unbound is 未应用.
  func profileStatus(_ profile: AIProfile, context: PresentationContext = .chinese) -> (
    text: String, tone: PaxBadge.Tone
  ) {
    let rows = clientRows(cost: false, context: context).filter {
      bindings[$0.client]?.profileID == profile.id
    }
    guard !rows.isEmpty else { return (context.text("ai_projection.04b0607b54"), .neutral) }
    let pick =
      rows.first { $0.needsAction } ?? rows.first { $0.state != nil } ?? rows[0]
    return (pick.stateText, Self.tone(forState: pick.stateCode))
  }

  /// Per-model totals of one Profile: the selected period, or all retained history.
  func modelUsage(profile: String, lifetime: Bool) -> [String: AIUsageSummary] {
    if preview {
      let rows = (lifetime ? requests : selected).filter {
        $0.profileID == profile && !AIUsageFormatting.isSyntheticModel($0.modelID)
      }
      return Dictionary(grouping: rows, by: \.modelID).mapValues { AIUsageSummary(requests: $0) }
    }
    let rows = (lifetime ? data?.lifetime : data?.buckets) ?? []
    return Dictionary(
      grouping: rows.filter {
        $0.profileID == profile && !AIUsageFormatting.isSyntheticModel($0.modelID)
      }, by: \.modelID
    )
    .mapValues { AIUsageSummary(buckets: $0) }
  }

  func modelsLine(_ p: AIProfile, context: PresentationContext = .chinese) -> String {
    let catalog = !p.catalog.isEmpty || p.modelsState == "ready" || p.modelsState == "partial"
    let count = catalog ? p.catalog.count : p.modelIDs.count
    if count > 0 {
      let used = modelUsage(profile: p.id, lifetime: true).values.filter { $0.count > 0 }.count
      return context.text(
        "ai_projection.22ef26c221",
        ["arg0": String(describing: count), "arg1": String(describing: used)])
    }
    if p.kind == "native" { return context.text("ai_projection.d1d072d54a") }
    return p.modelsState == "failed"
      ? context.text("ai_projection.40313ecd2b") : context.text("ai_projection.a2a35f38b8")
  }

  /// The model with the highest cost in the period; ties resolve to the smaller id.
  func topModelLine(_ p: AIProfile, context: PresentationContext = .chinese) -> String? {
    let used = modelUsage(profile: p.id, lifetime: false).filter { $0.value.count > 0 }
    let best = used.max {
      $0.value.cost != $1.value.cost ? $0.value.cost < $1.value.cost : $0.key > $1.key
    }
    return best.map { context.text("ai_projection.b39fe501cd") + $0.key }
  }

  /// The currently bound Profile uses the same selected-period projection as its list row.
  /// Recent use remains explicitly labelled as the CLI's retained history.
  func cliCardLines(_ client: AIClient, context: PresentationContext = .chinese) -> AICLICardLines {
    let sum =
      bindings[client].flatMap { binding in
        profiles.first(where: { $0.id == binding.profileID }).map { usageSummary(profile: $0.id) }
      } ?? usageSummary(client: client)
    let last = lastUsed(client).map { AIUsageFormatting.recentUse($0, now: now) } ?? "—"
    let source = bindings[client]?.serviceSource ?? .unknown
    let recent = AICLICardLines.Run(
      text: context.text("ai_projection.1e0354e0b0") + last, role: .label)
    guard sum.count > 0 else {
      return .init(
        usage: [.init(text: context.text("ai_projection.c41b0972aa"), role: .label)],
        recent: [recent])
    }
    typealias Run = AICLICardLines.Run
    let usage = [
      Run(text: period.label(context: context) + " ", role: .label),
      Run(text: AIUsageFormatting.tokens(sum.tokens), role: .token),
      Run(text: " Token · ", role: .label),
      Run(text: String(sum.count), role: .request),
      Run(text: context.text("ai_projection.e97f3e7c77"), role: .label),
    ]
    return .init(
      usage: usage,
      recent: [
        Run(text: context.text("ai_projection.32c7a9c133"), role: .label),
        Run(text: AIUsageFormatting.percent(sum.cache), role: .cache),
        Run(text: " · " + recent.text, role: .label),
      ],
      cost: source == .officialAccount || !sum.hasChannelEstimate
        ? [
          Run(text: context.text("ai_projection.83b6c5ab43"), role: .label),
          Run(text: sum.officialCostLabel, role: .officialCost),
        ]
        : [
          Run(text: context.text("ai_projection.5aee18ffc3"), role: .label),
          Run(text: sum.officialCostLabel, role: .officialCost),
          Run(text: context.text("ai_projection.b61a537950"), role: .label),
          Run(text: sum.costLabel, role: .cost),
        ])
  }

  static func host(_ endpoint: String) -> String {
    URL(string: endpoint)?.host
      ?? endpoint.replacingOccurrences(of: "https://", with: "")
      .replacingOccurrences(of: "http://", with: "")
  }

  var profileRecords: [AIProfileRecord] { profileRecords(context: .chinese) }
  func profileRecords(context: PresentationContext) -> [AIProfileRecord] {
    profiles.map { p in
      let sum = usageSummary(profile: p.id)
      let used = sum.count > 0
      let status = profileStatus(p, context: context)
      let native = p.kind == "native"
      return AIProfileRecord(
        id: p.id,
        isNative: native, nativeCLI: p.nativeCLI, statusText: status.text,
        statusTone: status.tone,
        source: native
          ? (p.nativeCLI?.rawValue ?? "CLI") + context.text("ai_projection.3a1990244f")
          : p.serviceSource.label(context: context) + " · " + Self.host(p.endpoint),
        missingKey: !p.hasKey && !native && !preview, name: p.name,
        multiplier: AIUsageFormatting.multiplier(p.multiplier),
        modelsLine: modelsLine(p, context: context),
        topModelLine: topModelLine(p, context: context),
        costHeader: period.label(context: context) + context.text("ai_projection.7fe8603c08"),
        costText: used ? sum.officialCostLabel : "—",
        channelCostText: used ? sum.costLabel : "—",
        tokensText: used ? AIUsageFormatting.tokens(sum.tokens) : "—",
        requestsText: used ? String(sum.count) : "—",
        cacheText: used ? AIUsageFormatting.percent(sum.cache) : "—")
    }
  }
}
