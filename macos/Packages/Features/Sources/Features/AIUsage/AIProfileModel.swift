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
    label == "配置已写入" ? .healthy : actionLabels.contains(label) ? .warning : .neutral
  }

  /// A Profile takes the most urgent state among the CLIs bound to it; unbound is 未应用.
  func profileStatus(_ profile: AIProfile) -> (text: String, tone: PaxBadge.Tone) {
    let rows = clientRows(cost: false).filter { bindings[$0.client]?.profileID == profile.id }
    guard !rows.isEmpty else { return ("未应用", .neutral) }
    let pick =
      rows.first { $0.needsAction } ?? rows.first { $0.state != nil } ?? rows[0]
    return (pick.stateText, Self.tone(forState: pick.stateText))
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

  func modelsLine(_ p: AIProfile) -> String {
    let catalog = !p.catalog.isEmpty || p.modelsState == "ready" || p.modelsState == "partial"
    let count = catalog ? p.catalog.count : p.modelIDs.count
    if count > 0 {
      let used = modelUsage(profile: p.id, lifetime: true).values.filter { $0.count > 0 }.count
      return "\(count) 个模型 · 用过 \(used) 个"
    }
    if p.kind == "native" { return "原生账号" }
    return p.modelsState == "failed" ? "模型读取失败" : "读取模型目录"
  }

  /// The model with the highest cost in the period; ties resolve to the smaller id.
  func topModelLine(_ p: AIProfile) -> String? {
    let used = modelUsage(profile: p.id, lifetime: false).filter { $0.value.count > 0 }
    let best = used.max {
      $0.value.cost != $1.value.cost ? $0.value.cost < $1.value.cost : $0.key > $1.key
    }
    return best.map { "最常用 " + $0.key }
  }

  /// The currently bound Profile uses the same selected-period projection as its list row.
  /// Recent use remains explicitly labelled as the CLI's retained history.
  func cliCardLines(_ client: AIClient) -> AICLICardLines {
    let sum =
      bindings[client].flatMap { binding in
        profiles.first(where: { $0.id == binding.profileID }).map { usageSummary(profile: $0.id) }
      } ?? AIUsageSummary(requests: [])
    let last = lastUsed(client).map { AIUsageFormatting.recentUse($0, now: now) } ?? "—"
    let recent = AICLICardLines.Run(text: "CLI 最近使用 " + last, role: .label)
    guard sum.count > 0 else {
      return .init(usage: [.init(text: "本时段无请求", role: .label)], recent: [recent])
    }
    typealias Run = AICLICardLines.Run
    let usage = [
      Run(text: period.rawValue + " ", role: .label),
      Run(text: AIUsageFormatting.tokens(sum.tokens), role: .token),
      Run(text: " Token · ", role: .label),
      Run(text: String(sum.count), role: .request),
      Run(text: " 请求", role: .label),
    ]
    return .init(
      usage: usage,
      recent: [
        Run(text: "缓存 ", role: .label),
        Run(text: AIUsageFormatting.percent(sum.cache), role: .cache),
        Run(text: " · " + recent.text, role: .label),
      ],
      cost: [
        Run(text: "官方 ", role: .label), Run(text: sum.officialCostLabel, role: .officialCost),
        Run(text: " / 渠道 ", role: .label), Run(text: sum.costLabel, role: .cost),
      ])
  }

  static func host(_ endpoint: String) -> String {
    URL(string: endpoint)?.host
      ?? endpoint.replacingOccurrences(of: "https://", with: "")
      .replacingOccurrences(of: "http://", with: "")
  }

  var profileRecords: [AIProfileRecord] {
    profiles.map { p in
      let sum = usageSummary(profile: p.id)
      let used = sum.count > 0
      let status = profileStatus(p)
      let native = p.kind == "native"
      return AIProfileRecord(
        id: p.id,
        isNative: native, nativeCLI: p.nativeCLI, statusText: status.text,
        statusTone: status.tone,
        source: native
          ? (p.nativeCLI?.rawValue ?? "CLI") + " · 原生账号配置" : "API · " + Self.host(p.endpoint),
        missingKey: !p.hasKey && !native && !preview, name: p.name,
        multiplier: AIUsageFormatting.multiplier(p.multiplier), modelsLine: modelsLine(p),
        topModelLine: topModelLine(p), costHeader: period.rawValue + "费用",
        costText: used ? sum.officialCostLabel : "—",
        channelCostText: used ? sum.costLabel : "—",
        tokensText: used ? AIUsageFormatting.tokens(sum.tokens) : "—",
        requestsText: used ? String(sum.count) : "—",
        cacheText: used ? AIUsageFormatting.percent(sum.cache) : "—")
    }
  }
}
