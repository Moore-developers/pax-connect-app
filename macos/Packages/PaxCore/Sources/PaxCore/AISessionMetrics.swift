import Foundation
import PaxClient

/// Pure analysis arithmetic shared by trajectories, comparisons and readouts.
public struct AISessionMetrics: Sendable {
  public let rows: [AISessionRow]
  public init(_ rows: [AISessionRow]) { self.rows = rows }
  public var totals: AIUsageTotals { rows.reduce(into: AIUsageTotals()) { $0.add($1.totals) } }
  public var completedTurns: Int {
    Set(rows.flatMap { row in row.completedTurnIDs.map { row.key.id + ":" + $0 } }).count
  }
  public var hasUnpriced: Bool { rows.contains { $0.pricedRequests < $0.totals.count } }
  public var knownRequests: Int { rows.reduce(0) { $0 + $1.knownRequests } }
  public var completeRows: [AISessionRow] { rows.filter(\.complete) }
  public func value(_ metric: String, channel: Bool) -> Double? {
    if metric.hasSuffix("PerTurn") {
      let completed = AISessionMetrics(rows.filter(\.complete))
      guard completed.completedTurns > 0,
        let total = completed.value(
          metric.replacingOccurrences(of: "PerTurn", with: ""), channel: channel)
      else { return nil }
      return total / Double(completed.completedTurns)
    }
    let t = totals
    guard t.count > 0 else { return nil }
    switch metric {
    case "cost": return hasUnpriced ? nil : channel ? t.cost : t.costs.reduce(0, +)
    case "tokens": return Double(t.input + t.output)
    case "turns": return knownRequests == t.count ? Double(completedTurns) : nil
    case "requests": return Double(t.count)
    case "cache": return t.input > 0 ? Double(t.read) / Double(t.input) : nil
    default: return nil
    }
  }
  public func breakdown(channel: Bool) -> [Double] {
    if !channel { return totals.costs }
    // Analysis sends authoritative channel total. Component values are filled by the
    // query from the stored per-request Billed amounts, never today's multiplier.
    return totals.billedCosts
  }
}
