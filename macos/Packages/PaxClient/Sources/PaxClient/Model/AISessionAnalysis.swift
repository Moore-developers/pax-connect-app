import Foundation
import PaxProto

/// Native identities are scoped to a CLI; display titles are optional metadata, never identities.
public struct AISessionKey: Hashable, Sendable, Identifiable {
  public let cli: String
  public let session: String
  public var id: String { cli + ":" + session }
  public init(cli: String, session: String) {
    self.cli = cli
    self.session = session
  }
}
public struct AISessionRow: Equatable, Sendable {
  public let key: AISessionKey
  public let requestID: String
  public let sessionTitle: String
  public let turn: String
  public let model: String
  public let effort: String
  public let at: Date
  public let end: Date
  public let complete: Bool
  public let compressed: Bool
  public let totals: AIUsageTotals
  public let maxInput: Int
  public let turns: Int
  public let knownRequests: Int
  public let completedTurnIDs: [String]
  public let pricedRequests: Int
  public init(
    key: AISessionKey, requestID: String = "", sessionTitle: String = "", turn: String = "",
    model: String,
    effort: String = "", at: Date,
    end: Date, complete: Bool, compressed: Bool = false, totals: AIUsageTotals, maxInput: Int,
    turns: Int, knownRequests: Int, pricedRequests: Int, completedTurnIDs: [String] = []
  ) {
    self.key = key
    self.requestID = requestID
    self.sessionTitle = sessionTitle
    self.turn = turn
    self.model = model
    self.effort = effort
    self.at = at
    self.end = end
    self.complete = complete
    self.compressed = compressed
    self.totals = totals
    self.maxInput = maxInput
    self.turns = turns
    self.knownRequests = knownRequests
    self.pricedRequests = pricedRequests
    self.completedTurnIDs = completedTurnIDs
  }
  init(_ p: Pax_V1_AIUsageAnalysisRow) {
    self.init(
      key: .init(cli: p.cli, session: p.sessionID), requestID: p.requestID,
      sessionTitle: p.sessionTitle, turn: p.turnID,
      model: p.modelID,
      effort: p.reasoningEffort, at: Date(timeIntervalSince1970: Double(p.atMs) / 1000),
      end: Date(timeIntervalSince1970: Double(p.endMs) / 1000), complete: p.complete,
      compressed: p.compressed, totals: AIUsageTotals(p.totals), maxInput: Int(p.maxInput),
      turns: Int(p.turns), knownRequests: Int(p.knownTurnRequests),
      pricedRequests: Int(p.pricedRequests), completedTurnIDs: p.completedTurnIds)
  }
}
public struct AISessionAnalysis: Sendable {
  public let rows: [AISessionRow]
  public let requests: [AISessionRow]
  public let limits: AISessionLimits?
  public let contextUsage: [AISessionContextUsage]
  public let state: AIUsageCollectionState
  public let truncated: Bool
  public let retainedFrom: Date
  public init(
    rows: [AISessionRow], requests: [AISessionRow] = [], state: AIUsageCollectionState = .init(),
    truncated: Bool = false, contextUsage: [AISessionContextUsage] = [],
    retainedFrom: Date = .distantPast, limits: AISessionLimits? = nil
  ) {
    self.limits = limits
    self.rows = rows
    self.requests = requests
    self.contextUsage = contextUsage
    self.state = state
    self.truncated = truncated
    self.retainedFrom = retainedFrom
  }
  init(_ p: Pax_V1_AIUsageAnalysis) {
    self.init(
      rows: p.rows.map(AISessionRow.init), requests: p.requests.map(AISessionRow.init),
      state: AIUsageCollectionState(p.state),
      truncated: p.truncated, contextUsage: p.contextUsage.map(AISessionContextUsage.init),
      retainedFrom: Date(timeIntervalSince1970: Double(p.retainedFromMs) / 1000),
      limits: p.hasLimits ? AISessionLimits(p.limits) : nil)
  }
}
