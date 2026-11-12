import Foundation

/// Demo scope assembly of the limits view: the list rows and summary cards for every
/// session and for each single session.
extension AISessionLimitsMath {
  /// Independent child sessions fold into their parent as sub-agent requests.
  static func merged(_ inputs: [AILimitSessionInput]) -> [AILimitSessionInput] {
    inputs.filter { $0.parent == nil }.map { parent in
      var result = parent
      for child in inputs where child.parent == parent.id {
        result.requests += child.requests.map { request in
          var request = request
          request.agent = child.agentName ?? child.title
          return request
        }
      }
      return result
    }
  }

  private struct Group {
    var cli: String
    var requests: [AILimitRequest] = []
    var sessions: Set<String> = []
  }

  private struct DayGroup {
    var sessions: Set<String> = []
    var input = AILimitMoney()
    var inputTokens = 0
    var requests = 0
    var pricedRequests = 0
  }

  /// One group per model x effort; keys come out in a stable order before the rows are sorted.
  private static func modelGroups(_ sessions: [(AILimitSessionInput, [AILimitRequest])])
    -> [(key: String, effort: String, model: String, group: Group)]
  {
    var groups: [String: (model: String, effort: String, group: Group)] = [:]
    for (session, requests) in sessions {
      for request in requests {
        let key = request.model + "\u{1f}" + request.effort
        var entry =
          groups[key] ?? (request.model, request.effort, Group(cli: session.cli))
        entry.group.requests.append(request)
        entry.group.sessions.insert(session.id)
        groups[key] = entry
      }
    }
    return groups.sorted { $0.key < $1.key }.map {
      ($0.key, $0.value.effort, $0.value.model, $0.value.group)
    }
  }

  static func summary(
    _ sessions: [AILimitSessionInput], rows: [AILimitListRow], single: Bool
  ) -> AILimitSummary {
    var input = AILimitMoney()
    var spend = AILimitMoney()
    for row in rows {
      input.add(row.input)
      if let value = row.overSpend { spend.add(value) }
    }
    var turns: Set<String> = []
    var agents: Set<String> = []
    var requests = 0
    var turnsComplete = true
    var bands = [AILimitBand](repeating: .init(), count: 3)
    var byDay: [Date: DayGroup] = [:]
    let calendar = Calendar.current
    for session in sessions {
      for request in session.requests {
        requests += 1
        let day = calendar.startOfDay(for: request.at)
        var group = byDay[day] ?? DayGroup()
        group.sessions.insert(session.cli + ":" + session.id)
        group.requests += 1
        group.inputTokens += request.context
        if let official = request.official?.total, let channel = request.channel?.total {
          let money = AILimitMoney(official: official, channel: channel)
          group.input.add(money)
          group.pricedRequests += 1
          if let lines = request.lines {
            let index = bandIndex(request.context, limit: lines)
            bands[index].money.add(money)
            bands[index].pricedRequests += 1
          }
        }
        if let lines = request.lines {
          bands[bandIndex(request.context, limit: lines)].requests += 1
        }
        byDay[day] = group
        if let turn = request.turn {
          turns.insert(session.id + "#" + String(turn))
        } else {
          turnsComplete = false
        }
        if let agent = request.agent { agents.insert(session.id + "#" + agent) }
      }
    }
    var days: [AILimitDay] = []
    var observedThrough: Date?
    if let first = byDay.keys.min(), let last = byDay.keys.max() {
      var day = first
      while day <= last {
        let group = byDay[day] ?? DayGroup()
        days.append(
          .init(
            at: day, sessions: group.sessions.count, input: group.input,
            inputTokens: group.inputTokens,
            requests: group.requests, pricedRequests: group.pricedRequests, available: true))
        guard let next = calendar.date(byAdding: .day, value: 1, to: day) else { break }
        day = next
      }
      observedThrough = calendar.date(byAdding: .day, value: 1, to: last)
    }
    let multiple: AILimitMultiple
    if let safe = bands[0].average?.official, safe > 0,
      let over = bands[2].average?.official
    {
      multiple = .value(over / safe)
    } else {
      multiple = .dash
    }
    var current: AILimitCurrent?
    if single, let session = sessions.first,
      let last = ordered(session.requests.filter(\.isMain)).last
    {
      let limit = last.lines
      current = .init(
        context: last.context, limit: limit?.limit,
        band: bandIndex(last.context, limit: limit))
    }
    return .init(
      input: input, inputTokens: byDay.values.reduce(0) { $0 + $1.inputTokens }, overSpend: spend,
      sessions: sessions.count,
      overSessions: rows.filter { $0.bands[2].requests > 0 }.count, current: current,
      requests: requests, turns: turns.count, turnsComplete: turnsComplete,
      subagents: agents.count, bands: bands, multiple: multiple, days: days,
      observedThrough: observedThrough,
      unpriced: sessions.reduce(0) { $0 + $1.requests.filter { !$0.priced }.count })
  }

  static func allScope(_ sessions: [AILimitSessionInput]) -> AILimitScope {
    let recent = sessions.sorted {
      let lhs = $0.requests.map(\.at).max() ?? .distantPast
      let rhs = $1.requests.map(\.at).max() ?? .distantPast
      if lhs != rhs { return lhs > rhs }
      if $0.cli != $1.cli { return $0.cli < $1.cli }
      return $0.id < $1.id
    }
    let sessionRows = recent.map {
      row(
        id: "s:" + $0.id, cli: $0.cli, title: $0.title, requests: $0.requests,
        sessionID: $0.id)
    }
    let modelRows = modelGroups(sessions.map { ($0, $0.requests) }).map { entry in
      row(
        id: "m:" + entry.key, cli: entry.group.cli, title: entry.model,
        requests: entry.group.requests, note: "\(entry.group.sessions.count) 个会话")
    }
    return .init(
      summary: summary(sessions, rows: sessionRows, single: false),
      lists: [
        .init(
          kind: .sessions, rows: sessionRows, emptyMessage: "所选日期没有已记录用量"),
        .init(kind: .models, rows: sorted(modelRows), emptyMessage: "所选日期没有已记录用量"),
      ], trend: nil)
  }

  static func singleScope(_ session: AILimitSessionInput) -> AILimitScope {
    let main = session.requests.filter(\.isMain)
    let mainTotal = row(
      id: "main", cli: session.cli, title: "主会话", requests: main, emphasized: true)
    let mainRows = modelGroups([(session, main)]).map { entry in
      row(
        id: "main:" + entry.key, cli: session.cli, title: entry.model,
        requests: entry.group.requests)
    }
    var byAgent: [String: [AILimitRequest]] = [:]
    for request in session.requests {
      if let agent = request.agent { byAgent[agent, default: []].append(request) }
    }
    let agentRows = byAgent.keys.sorted().map {
      row(id: "a:" + $0, cli: session.cli, title: $0, requests: byAgent[$0] ?? [])
    }
    let total = row(
      id: "s:" + session.id, cli: session.cli, title: session.title, requests: session.requests)
    return .init(
      summary: summary([session], rows: [total], single: true),
      lists: [
        .init(
          kind: .main, rows: [mainTotal] + (mainRows.count > 1 ? sorted(mainRows) : []),
          emptyMessage: "该会话没有主会话请求"),
        .init(
          kind: .subagents, rows: sorted(agentRows), emptyMessage: "该会话没有子代理"),
      ], trend: trend(session.requests))
  }

  static func model(_ inputs: [AILimitSessionInput])
    -> AISessionLimitsModel
  {
    let sessions = merged(inputs)
    return .init(
      sessions: sessions.map { .init(id: $0.id, cli: $0.cli, title: $0.title) },
      all: allScope(sessions),
      single: Dictionary(
        uniqueKeysWithValues: sessions.map { ($0.id, singleScope($0)) }))
  }
}
