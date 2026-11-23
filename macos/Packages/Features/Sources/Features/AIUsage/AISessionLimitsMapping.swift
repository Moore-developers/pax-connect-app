import Foundation
import PaxClient
import PaxCore

/// Projection only: business amounts, thresholds, classifications and markers come from daemon.
enum AISessionLimitsMapping {
  static func money(_ value: AISessionLimitMoney) -> AILimitMoney {
    .init(official: value.official, channel: value.channel, channelUnknown: value.channelUnknown)
  }

  static func scope(
    _ value: AISessionLimits, selected: Bool, context: PresentationContext = .chinese
  ) -> AILimitScope {
    let s = value.summary
    // Selected primary rows include a main total followed by its model breakdown. Count
    // that total once, then the disjoint agent rows; all-session primary rows are disjoint.
    let unpriced =
      selected
      ? (value.primary.first?.unpriced ?? 0) + value.secondary.reduce(0) { $0 + $1.unpriced }
      : value.primary.reduce(0) { $0 + $1.unpriced }
    return .init(
      summary: .init(
        input: money(s.input), inputTokens: s.inputTokens, overSpend: money(s.overSpend),
        sessions: s.sessions,
        overSessions: s.overSessions,
        current: s.current.map {
          .init(
            context: $0.context, limit: $0.limit, band: $0.band,
            nativeContextLimit: $0.nativeContextLimit)
        },
        requests: s.requests, turns: s.turns, turnsComplete: s.turnsComplete,
        subagents: s.subagents, bands: s.bands.map(band),
        multiple: s.multiple.map { .value($0) } ?? .dash,
        days: s.days.map {
          .init(
            at: $0.at, sessions: $0.sessions, input: money($0.input), inputTokens: $0.inputTokens,
            requests: $0.requests,
            pricedRequests: $0.pricedRequests, available: $0.available)
        }, observedThrough: s.observedThrough,
        comparison: s.comparison.map {
          .init(
            input: money($0.input), inputTokens: $0.inputTokens, sessions: $0.sessions,
            from: $0.from, to: $0.to)
        }, unpriced: unpriced),
      lists: [
        .init(
          kind: selected ? .main : .sessions, rows: value.primary.map { row($0, context: context) },
          emptyMessage: selected ? "该会话没有主会话请求" : "所选日期没有已记录用量"),
        .init(
          kind: selected ? .subagents : .models,
          rows: value.secondary.map { row($0, context: context) },
          emptyMessage: selected ? "该会话没有子代理" : "所选日期没有已记录用量"),
      ], trend: value.trend.map(trend))
  }

  private static func band(_ value: AISessionLimitBand) -> AILimitBand {
    .init(
      requests: value.requests, money: money(value.money), pricedRequests: value.pricedRequests)
  }

  static func title(_ value: AISessionLimitRow, context: PresentationContext) -> String {
    if value.titleSource == "pax.unnamed" {
      return context.text("session_title.unnamed_id", ["id": value.rawTitle])
    }
    if ["claude.last-prompt", "claude.user-message"].contains(value.titleSource) {
      return context.text("session_title.recent_with_title", ["title": value.rawTitle])
    }
    return value.rawTitle.isEmpty ? value.title : value.rawTitle
  }
  private static func row(_ value: AISessionLimitRow, context: PresentationContext)
    -> AILimitListRow
  {
    var row = AILimitListRow(
      id: value.id, cli: value.cli,
      title: value.id == "main"
        ? context.text("session.kind.main") : title(value, context: context),
      model: value.model, effort: value.effort)
    row.emphasized = value.emphasized
    row.sessionID =
      value.sessionID.isEmpty ? nil : AISessionKey(cli: value.cli, session: value.sessionID).id
    if let count = value.sessionCount {
      row.note = context.text("session_note.sessions", count: count)
    } else if value.note.isEmpty && value.modelLines.count > 1 {
      row.note = context.text("session_note.more_models", count: value.modelLines.count - 1)
    } else {
      row.note = value.note
    }
    row.bands = value.bands.map(band)
    row.percents = AISessionLimitsMath.percents(row.bands.map(\.money.official))
    row.lines = value.lines.map(AIModelLimit.init)
    row.modelLines = value.modelLines.map {
      .init(model: $0.model, limit: $0.lines.map(AIModelLimit.init), requests: $0.requests)
    }
    row.multiple = value.multiple.map { .value($0) } ?? .dash
    row.overSpend = value.overSpend.map(money)
    row.flat = value.flat.map {
      .init(
        limit: $0.limit, requests: $0.requests, cost: money($0.cost),
        overSpend: $0.overSpend.map(money))
    }
    row.unpriced = value.unpriced
    return row
  }

  private static func trend(_ value: AISessionLimitTrend) -> AILimitTrend {
    let requests: [AILimitRequest] = value.requests.map { point in
      let r = point.request
      let o = r.totals.costs
      let c = r.totals.billedCosts
      return .init(
        at: r.at, turn: point.turnNumber, model: r.model, effort: r.effort,
        fresh: r.totals.input - r.totals.read - r.totals.write,
        read: r.totals.read, write: r.totals.write,
        official: r.pricedRequests > 0 ? .init(fresh: o[0], read: o[1], write: o[2]) : nil,
        channel: r.pricedRequests > 0 && r.totals.unconfirmedChannel == 0
          ? .init(fresh: c[0], read: c[1], write: c[2]) : nil,
        lines: point.lines.map(AIModelLimit.init), band: point.band)
    }
    return .init(
      requests: requests, limit: value.lines.map(AIModelLimit.init),
      overStart: value.overStart, flatStart: value.flatStart,
      switches: value.switches.map {
        .init(
          index: $0.index,
          kind: $0.kind == "model"
            ? .model(from: $0.from, to: $0.to) : .effort(from: $0.from, to: $0.to),
          turnMultiple: $0.turnMultiple, afterReadMultiple: $0.afterReadMultiple)
      }, hasWriteLayer: value.hasWriteLayer,
      officialOnly: value.requests.first?.request.key.cli == AIClient.antigravity.code)
  }
}
