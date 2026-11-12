import PaxProto

/// Date-window input usage, grouped by request size. Output is excluded from both costs.
public struct AISessionContextBand: Equatable, Sendable {
  public let input: Int
  public let officialCost: Double
  public let channelCost: Double
  public let requests: Int
  public let pricedRequests: Int
  init(_ p: Pax_V1_AIUsageContextBand) {
    input = Int(p.input)
    officialCost = Double(p.officialCostMicro) / 1_000_000
    channelCost = Double(p.channelCostMicro) / 1_000_000
    requests = Int(p.requests)
    pricedRequests = Int(p.pricedRequests)
  }
}

/// A nil session selects all sessions; an empty model identifies the deduplicated total.
public struct AISessionContextUsage: Equatable, Sendable {
  public let session: AISessionKey?
  public let model: String
  public let effort: String
  public let bands: [AISessionContextBand]
  public let requests: Int
  public let turns: Int
  public let knownTurnRequests: Int
  public let subagents: Int
  public let subagentBands: [AISessionContextBand]
  public let subagentsIncomplete: Bool
  public var input: Int { bands.reduce(0) { $0 + $1.input } }
  init(_ p: Pax_V1_AIUsageContextUsage) {
    session = p.cli.isEmpty ? nil : .init(cli: p.cli, session: p.sessionID)
    model = p.modelID
    effort = p.reasoningEffort
    bands = p.bands.map(AISessionContextBand.init)
    requests = Int(p.requests)
    turns = Int(p.turns)
    knownTurnRequests = Int(p.knownTurnRequests)
    subagents = Int(p.subagents)
    subagentBands = p.subagentBands.map(AISessionContextBand.init)
    subagentsIncomplete = p.subagentsIncomplete
  }
}
