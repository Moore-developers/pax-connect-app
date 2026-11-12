import Foundation
import PaxProto

/// Daemon-owned business projection of P07d session limits; amounts are input-only USD.
public struct AISessionLimitMoney: Equatable, Sendable {
  public let official: Double
  public let channel: Double
  public let channelUnknown: Bool
  init(_ p: Pax_V1_AIUsageLimitMoney) {
    official = p.official
    channel = p.channel
    channelUnknown = p.channelUnknown
  }
}
public struct AISessionModelLimit: Equatable, Sendable {
  public let model: String
  public let limit: Int
  public let warn: Int
  public let flatLimit: Int
  public let flatWarn: Int
  public let baseline: Double?
  init(_ p: Pax_V1_AIUsageModelLimit) {
    model = p.model
    limit = Int(p.limit)
    warn = Int(p.warn)
    flatLimit = Int(p.flatLimit)
    flatWarn = Int(p.flatWarn)
    baseline = p.hasBaseline ? p.baseline : nil
  }
}
public struct AISessionLimitBand: Equatable, Sendable {
  public let requests: Int
  public let pricedRequests: Int
  public let money: AISessionLimitMoney
  init(_ p: Pax_V1_AIUsageLimitBand) {
    requests = Int(p.requests)
    pricedRequests = Int(p.pricedRequests)
    money = AISessionLimitMoney(p.money)
  }
}
public struct AISessionLimitModelLine: Equatable, Sendable {
  public let model: String
  public let lines: AISessionModelLimit?
  public let requests: Int
  init(_ p: Pax_V1_AIUsageLimitModelLine) {
    model = p.model
    lines = p.hasLines ? AISessionModelLimit(p.lines) : nil
    requests = Int(p.requests)
  }
}
public struct AISessionLimitFlat: Equatable, Sendable {
  public let limit: Int
  public let requests: Int
  public let cost: AISessionLimitMoney
  public let overSpend: AISessionLimitMoney?
  init(_ p: Pax_V1_AIUsageLimitFlat) {
    limit = Int(p.limit)
    requests = Int(p.requests)
    cost = AISessionLimitMoney(p.cost)
    overSpend = p.hasOverSpend ? AISessionLimitMoney(p.overSpend) : nil
  }
}
public struct AISessionLimitRow: Equatable, Sendable {
  public let id: String
  public let cli: String
  public let sessionID: String
  public let title: String
  public let model: String
  public let effort: String
  public let note: String
  public let emphasized: Bool
  public let bands: [AISessionLimitBand]
  public let lines: AISessionModelLimit?
  public let modelLines: [AISessionLimitModelLine]
  public let multiple: Double?
  public let overSpend: AISessionLimitMoney?
  public let flat: AISessionLimitFlat?
  public let unpriced: Int
  init(_ p: Pax_V1_AIUsageLimitRow) {
    id = p.id
    cli = p.cli
    sessionID = p.sessionID
    title = p.title
    model = p.model
    effort = p.effort
    note = p.note
    emphasized = p.emphasized
    bands = p.bands.map(AISessionLimitBand.init)
    lines = p.hasLines ? AISessionModelLimit(p.lines) : nil
    modelLines = p.modelLines.map(AISessionLimitModelLine.init)
    multiple = p.hasMultiple ? p.multiple : nil
    overSpend = p.hasOverSpend ? AISessionLimitMoney(p.overSpend) : nil
    flat = p.hasFlat ? AISessionLimitFlat(p.flat) : nil
    unpriced = Int(p.unpriced)
  }
}
public struct AISessionLimitCurrent: Equatable, Sendable {
  public let context: Int
  public let limit: Int?
  public let band: Int
  init(_ p: Pax_V1_AIUsageLimitCurrent) {
    context = Int(p.context)
    limit = p.hasLimit ? Int(p.limit) : nil
    band = Int(p.band)
  }
}
/// One local calendar day; unavailable days must not be presented as zero usage.
public struct AISessionLimitDay: Equatable, Sendable {
  public let at: Date
  public let sessions: Int
  public let input: AISessionLimitMoney
  public let inputTokens: Int?
  public let requests: Int
  public let pricedRequests: Int
  public let available: Bool
  init(_ p: Pax_V1_AIUsageLimitDay) {
    at = Date(timeIntervalSince1970: Double(p.atMs) / 1000)
    sessions = Int(p.sessions)
    input = AISessionLimitMoney(p.input)
    inputTokens = p.hasInputTokens ? Int(p.inputTokens) : nil
    requests = Int(p.requests)
    pricedRequests = Int(p.pricedRequests)
    available = p.available
  }
}
/// Previous equal calendar range, clipped to the same elapsed part as the current range.
public struct AISessionLimitComparison: Equatable, Sendable {
  public let input: AISessionLimitMoney
  public let inputTokens: Int?
  public let sessions: Int
  public let from: Date
  public let to: Date
  init(_ p: Pax_V1_AIUsageLimitComparison) {
    input = AISessionLimitMoney(p.input)
    inputTokens = p.hasInputTokens ? Int(p.inputTokens) : nil
    sessions = Int(p.sessions)
    from = Date(timeIntervalSince1970: Double(p.fromMs) / 1000)
    to = Date(timeIntervalSince1970: Double(p.toMs) / 1000)
  }
}
public struct AISessionLimitSummary: Equatable, Sendable {
  public let input: AISessionLimitMoney
  public let inputTokens: Int?
  public let overSpend: AISessionLimitMoney
  public let sessions: Int
  public let overSessions: Int
  public let requests: Int
  public let turns: Int
  public let subagents: Int
  public let turnsComplete: Bool
  public let current: AISessionLimitCurrent?
  public let curve: [Double]
  public let bands: [AISessionLimitBand]
  public let multiple: Double?
  public let days: [AISessionLimitDay]
  public let observedThrough: Date?
  public let comparison: AISessionLimitComparison?
  init(_ p: Pax_V1_AIUsageLimitSummary) {
    input = AISessionLimitMoney(p.input)
    inputTokens = p.hasInputTokens ? Int(p.inputTokens) : nil
    overSpend = AISessionLimitMoney(p.overSpend)
    sessions = Int(p.sessions)
    overSessions = Int(p.overSessions)
    requests = Int(p.requests)
    turns = Int(p.turns)
    subagents = Int(p.subagents)
    turnsComplete = p.turnsComplete
    current = p.hasCurrent ? AISessionLimitCurrent(p.current) : nil
    curve = p.curve
    bands = p.bands.map(AISessionLimitBand.init)
    multiple = p.hasMultiple ? p.multiple : nil
    days = p.days.map(AISessionLimitDay.init)
    observedThrough =
      p.observedThroughMs > 0
      ? Date(timeIntervalSince1970: Double(p.observedThroughMs) / 1000) : nil
    comparison = p.hasComparison ? AISessionLimitComparison(p.comparison) : nil
  }
}
public struct AISessionLimitRequest: Equatable, Sendable {
  public let request: AISessionRow
  public let lines: AISessionModelLimit?
  public let band: Int
  public let turnNumber: Int?
  init(_ p: Pax_V1_AIUsageLimitRequest) {
    request = AISessionRow(p.request)
    lines = p.hasLines ? AISessionModelLimit(p.lines) : nil
    band = Int(p.band)
    turnNumber = p.hasTurnNumber ? Int(p.turnNumber) : nil
  }
}
public struct AISessionLimitSwitch: Equatable, Sendable {
  public let index: Int
  public let kind: String
  public let from: String
  public let to: String
  public let turnMultiple: Double?
  public let afterReadMultiple: Double?
  init(_ p: Pax_V1_AIUsageLimitSwitch) {
    index = Int(p.index)
    kind = p.kind
    from = p.from
    to = p.to
    turnMultiple = p.hasTurnMultiple ? p.turnMultiple : nil
    afterReadMultiple = p.hasAfterReadMultiple ? p.afterReadMultiple : nil
  }
}
public struct AISessionLimitTrend: Equatable, Sendable {
  public let requests: [AISessionLimitRequest]
  public let lines: AISessionModelLimit?
  public let overStart: Int?
  public let flatStart: Int?
  public let switches: [AISessionLimitSwitch]
  public let hasWriteLayer: Bool
  init(_ p: Pax_V1_AIUsageLimitTrend) {
    requests = p.requests.map(AISessionLimitRequest.init)
    lines = p.hasLines ? AISessionModelLimit(p.lines) : nil
    overStart = p.hasOverStart ? Int(p.overStart) : nil
    flatStart = p.hasFlatStart ? Int(p.flatStart) : nil
    switches = p.switches.map(AISessionLimitSwitch.init)
    hasWriteLayer = p.hasWriteLayer_p
  }
}
public struct AISessionLimits: Equatable, Sendable {
  public let summary: AISessionLimitSummary
  public let primary: [AISessionLimitRow]
  public let secondary: [AISessionLimitRow]
  public let trend: AISessionLimitTrend?
  public let agentsIncomplete: Bool
  init(_ p: Pax_V1_AIUsageSessionLimits) {
    summary = AISessionLimitSummary(p.summary)
    primary = p.primary.map(AISessionLimitRow.init)
    secondary = p.secondary.map(AISessionLimitRow.init)
    trend = p.hasTrend ? AISessionLimitTrend(p.trend) : nil
    agentsIncomplete = p.agentsIncomplete
  }
}
