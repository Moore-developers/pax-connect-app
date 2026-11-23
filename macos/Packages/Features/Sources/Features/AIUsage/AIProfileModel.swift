import Foundation
import PaxCore
import PaxUI

/// Display values for one Profile record row; built from the same binding projection as the overview.
struct AIProfileRecord {
  let id: String
  let isNative: Bool
  let nativeCLI: AIClient?
  let statusText: String
  let statusTone: PaxBadge.Tone
  let source: String
  let missingKey: Bool
  let name: String
  let multiplier: String
  let periodUsage: AIUsageSummary
  let lifetimeUsage: AIUsageSummary
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

  /// The model with the highest cost in the period; ties resolve to the smaller id.
  func topModelLine(_ p: AIProfile, context: PresentationContext = .chinese) -> String? {
    let used = modelUsage(profile: p.id, lifetime: false).filter { $0.value.count > 0 }
    let best = used.max {
      $0.value.cost != $1.value.cost ? $0.value.cost < $1.value.cost : $0.key > $1.key
    }
    return best.map { context.text("ai_projection.b39fe501cd") + $0.key }
  }

  var periodUsageReady: Bool { preview || data?.bucketsLoaded == true }
  var lifetimeUsageReady: Bool { preview || data?.lifetimeLoaded == true }

  static func host(_ endpoint: String) -> String {
    URL(string: endpoint)?.host
      ?? endpoint.replacingOccurrences(of: "https://", with: "")
      .replacingOccurrences(of: "http://", with: "")
  }

  var profileRecords: [AIProfileRecord] { profileRecords(context: .chinese) }
  func profileRecords(context: PresentationContext) -> [AIProfileRecord] {
    profiles.map { p in
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
        periodUsage: usageSummary(profile: p.id),
        lifetimeUsage: usageSummary(profile: p.id, lifetime: true))
    }
  }
}
