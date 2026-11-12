import Foundation
import PaxClient
import PaxCore

// Display model of the session-limits view (P07d-ai-session-limits). The view reads only these
// values; the demo builds them from in-memory requests, integration builds the same types from
// daemon responses. Business numbers (bands, multiples, over-spend) never come from the view.

/// Per-model limits, warning lines and baseline (spec §3). Two pricing cases: doubled and flat.
struct AIModelLimit: Equatable {
  let limit: Int
  let warn: Int
  /// Limit and warning line when the channel does not double input above 272k.
  let flatLimit: Int
  let flatWarn: Int
  /// Official $/request averaged below 150k (haiku 100k); nil when no baseline is known.
  let baseline: Double?
  var hasSecondLimit: Bool { flatLimit != limit }

  private init(_ limit: Int, _ warn: Int, flat: (Int, Int)? = nil, baseline: Double? = nil) {
    self.limit = limit * 1000
    self.warn = warn * 1000
    flatLimit = (flat?.0 ?? limit) * 1000
    flatWarn = (flat?.1 ?? warn) * 1000
    self.baseline = baseline
  }

  init(_ value: AISessionModelLimit) {
    limit = value.limit
    warn = value.warn
    flatLimit = value.flatLimit
    flatWarn = value.flatWarn
    baseline = value.baseline
  }

  static func preset(for model: String) -> AIModelLimit? {
    switch model {
    case "gpt-6.1-sol": return .init(272, 250, flat: (350, 300), baseline: 0.023)
    case "gpt-6.1-terra": return .init(272, 250, flat: (350, 300), baseline: 0.009)
    case "gpt-6-astra": return .init(200, 150, baseline: 0.137)
    case "claude-opus-5-5": return .init(200, 150, baseline: 0.069)
    case "claude-fable-5-2": return .init(200, 150, baseline: 0.12)
    case "claude-sonnet-5-5": return .init(250, 200, baseline: 0.025)
    case "claude-haiku-5-5": return .init(100, 80, baseline: 0.0011)
    default:
      if model.hasPrefix("gpt-") { return .init(272, 250) }
      if model.hasPrefix("claude-") { return .init(200, 150) }
      return nil
    }
  }
}

/// Input-class costs of one request: ordinary input, cache read and cache write; no output.
struct AILimitCosts: Equatable {
  var fresh: Double
  var read: Double
  var write: Double
  var total: Double { fresh + read + write }
}

struct AILimitRequest: Equatable {
  var at: Date
  /// nil when the native record carries no turn.
  var turn: Int?
  var model: String
  var effort: String
  /// nil for the main line; the native agent name for sub-agent requests.
  var agent: String?
  var fresh: Int
  var read: Int
  var write: Int
  /// nil when the request is unpriced; official and channel always come as a pair.
  var official: AILimitCosts?
  var channel: AILimitCosts?
  /// True when the official cost already includes the 2x input rule above 272k.
  var doubled = false
  var lines: AIModelLimit?
  var band: Int?
  var context: Int { fresh + read + write }
  var isMain: Bool { agent == nil }
  var priced: Bool { official != nil }
  func costs(channel useChannel: Bool) -> AILimitCosts? { useChannel ? channel : official }
}

struct AILimitMoney: Equatable {
  var official = 0.0
  var channel = 0.0
  var channelUnknown = false
  func value(channel useChannel: Bool) -> Double { useChannel ? channel : official }
  mutating func add(_ other: AILimitMoney) {
    official += other.official
    channel += other.channel
    channelUnknown = channelUnknown || other.channelUnknown
  }
}

enum AILimitMultiple: Equatable {
  case value(Double)
  /// No request above the limit, a model without a limit, or no denominator: shown as a dash.
  case dash
}

/// Requests and priced input cost that fall into one band; unpriced requests count only as
/// requests.
struct AILimitBand: Equatable {
  var requests = 0
  var money = AILimitMoney()
  var pricedRequests = 0
  var average: AILimitMoney? {
    guard pricedRequests > 0 else { return nil }
    return .init(
      official: money.official / Double(pricedRequests),
      channel: money.channel / Double(pricedRequests), channelUnknown: money.channelUnknown)
  }
  mutating func add(_ other: AILimitBand) {
    requests += other.requests
    pricedRequests += other.pricedRequests
    money.add(other.money)
  }
}

/// A model's lines inside one row, for the hover of mixed rows.
struct AILimitModelLine: Equatable {
  var model: String
  var limit: AIModelLimit?
  var requests: Int
}

/// Flat case (channel does not double above 272k): requests above the second limit.
struct AILimitFlat: Equatable {
  var limit: Int
  var requests: Int
  var cost: AILimitMoney
  var overSpend: AILimitMoney?
}

/// Which list the limits card shows.
enum AILimitListKind: Equatable {
  case sessions, models, main, subagents
  var title: String {
    switch self {
    case .sessions: "会话"
    case .models: "模型"
    case .main: "主会话"
    case .subagents: "子代理"
    }
  }
}

/// One line of the limits list (P07d-ai-session-limits §4.2).
struct AILimitListRow: Equatable, Identifiable {
  var id: String
  var cli: String
  var title: String
  var emphasized = false
  /// Latest model and effort of the row; model rows carry their own.
  var model: String
  var effort: String
  /// Second-line note after the tags, e.g. "3 个会话" or "另 1 个模型".
  var note = ""
  /// Set on session rows: clicking selects this session.
  var sessionID: String?
  /// <=warn, warn-limit, >limit; models without a limit keep everything in the first band.
  var bands = [AILimitBand](repeating: .init(), count: 3)
  /// Whole percentages of the three bands by official input cost, summing to 100.
  var percents = [0, 0, 0]
  /// Lines of the latest model; nil for models without a preset.
  var lines: AIModelLimit?
  var modelLines: [AILimitModelLine] = []
  var multiple = AILimitMultiple.dash
  /// Over-limit requests' input cost above the row's own safe average; nil when it cannot
  /// be judged.
  var overSpend: AILimitMoney?
  var flat: AILimitFlat?
  var unpriced = 0
  var input: AILimitMoney {
    var sum = AILimitMoney()
    for band in bands { sum.add(band.money) }
    return sum
  }
  var requests: Int { bands.reduce(0) { $0 + $1.requests } }
}

struct AILimitList: Equatable {
  var kind: AILimitListKind
  var rows: [AILimitListRow]
  var emptyMessage = ""
}

/// Current context of a single session, coloured by its band.
struct AILimitCurrent: Equatable {
  var context: Int
  var limit: Int?
  var band: Int
}

/// One local calendar day of active root sessions and their input costs, including descendants.
struct AILimitDay: Equatable {
  var at: Date
  var sessions: Int
  var input: AILimitMoney
  var inputTokens: Int? = nil
  var requests: Int
  var pricedRequests: Int
  var available: Bool
}

struct AILimitComparison: Equatable {
  var input: AILimitMoney
  var inputTokens: Int? = nil
  var sessions: Int
  var from: Date
  var to: Date
}

/// Summary cards and list counts (§4.1) for one scope.
struct AILimitSummary: Equatable {
  var input: AILimitMoney
  var inputTokens: Int? = nil
  var overSpend: AILimitMoney
  var sessions: Int
  var overSessions: Int
  /// Single session only.
  var current: AILimitCurrent?
  var requests: Int
  var turns: Int
  var turnsComplete: Bool
  var subagents: Int
  /// Aggregated priced bands only for models with configured context thresholds.
  var bands = [AILimitBand](repeating: .init(), count: 3)
  var multiple = AILimitMultiple.dash
  var days: [AILimitDay] = []
  var observedThrough: Date?
  var comparison: AILimitComparison?
  var unpriced = 0
}

/// A model or effort change inside a session, positioned at the first request after the change.
struct AILimitSwitch: Equatable {
  enum Kind: Equatable {
    case model(from: String, to: String)
    case effort(from: String, to: String)
  }
  var index: Int
  var kind: Kind
  /// Switch request's input cost over the previous request's.
  var turnMultiple: Double?
  /// Per-token cache-read price after the switch over the price before it.
  var afterReadMultiple: Double?
}

/// Single-session trend: main-line requests plus the markers derived from them.
struct AILimitTrend: Equatable {
  var requests: [AILimitRequest]
  /// Limits of the latest model; nil unless the peak context reaches the warning line.
  var limit: AIModelLimit?
  var overStart: Int?
  var flatStart: Int?
  var switches: [AILimitSwitch]
  var hasWriteLayer: Bool
}

/// Everything shown for one selection: every session or a single one.
struct AILimitScope: Equatable {
  var summary: AILimitSummary
  var lists: [AILimitList]
  /// Single session only.
  var trend: AILimitTrend?
}

struct AILimitSessionHead: Equatable {
  var id: String
  var cli: String
  var title: String
}

struct AISessionLimitsModel: Equatable {
  var sessions: [AILimitSessionHead]
  var all: AILimitScope
  var single: [String: AILimitScope]
}
