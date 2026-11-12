import Foundation

/// Input of the demo builder: one session with all its requests.
struct AILimitSessionInput {
  var id: String
  var cli: String
  var title: String
  /// Set on an independent child session (Codex, DeepSeek): it folds into its parent as a
  /// sub-agent named `agentName`.
  var parent: String?
  var agentName: String?
  var requests: [AILimitRequest]
}

/// Pure rules of the limits view. Everything derives from per-request rows and the preset
/// limits, so integration can feed daemon rows through the same functions.
enum AISessionLimitsMath {
  static func ordered(_ requests: [AILimitRequest]) -> [AILimitRequest] {
    requests.sorted { $0.at < $1.at }
  }

  // MARK: Bands

  /// 0 = up to the warning line, 1 = up to the limit, 2 = above it. Models without limits keep
  /// every request in the first band.
  static func bandIndex(_ context: Int, limit: AIModelLimit?) -> Int {
    guard let limit else { return 0 }
    if context <= limit.warn { return 0 }
    return context <= limit.limit ? 1 : 2
  }

  /// Whole percentages that sum to 100 (largest remainder); all zero without a positive total.
  static func percents(_ values: [Double]) -> [Int] {
    let total = values.reduce(0, +)
    guard total > 0 else { return values.map { _ in 0 } }
    let shares = values.map { $0 / total * 100 }
    var rounded = shares.map { Int($0.rounded(.down)) }
    let order = shares.indices.sorted {
      shares[$0] - Double(rounded[$0]) > shares[$1] - Double(rounded[$1])
    }
    for index in order.prefix(max(0, 100 - rounded.reduce(0, +))) { rounded[index] += 1 }
    return rounded
  }

  // MARK: Rows

  /// Priced input cost of one request plus the reference baseline of its model.
  private struct Sample {
    var official: Double
    var channel: Double
    var baseline: Double?
    /// Channel amount per official dollar of this request.
    var rate: Double { official > 0 ? channel / official : 1 }
  }

  /// Multiple and over-spend of one set of over-limit requests (spec §2). The denominator is
  /// the row's own safe-request average; with none, each request's model baseline.
  /// Without either the row cannot be judged: dash and nil spend.
  private static func judge(over: [Sample], rest: [Sample]) -> (AILimitMultiple, AILimitMoney?) {
    guard !over.isEmpty else { return (.dash, AILimitMoney()) }
    func mean(_ values: [Double]) -> Double { values.reduce(0, +) / Double(values.count) }
    var spend = AILimitMoney()
    let denominator: Double
    if !rest.isEmpty {
      let official = mean(rest.map(\.official))
      let channel = mean(rest.map(\.channel))
      denominator = official
      for sample in over {
        spend.add(.init(official: sample.official - official, channel: sample.channel - channel))
      }
    } else {
      let baselines = over.compactMap(\.baseline)
      guard baselines.count == over.count else { return (.dash, nil) }
      denominator = mean(baselines)
      for sample in over {
        let baseline = sample.baseline ?? 0
        spend.add(
          .init(
            official: sample.official - baseline,
            channel: sample.channel - baseline * sample.rate))
      }
    }
    guard denominator > 0 else { return (.dash, nil) }
    return (.value(mean(over.map(\.official)) / denominator), spend)
  }

  /// One list row from the requests it covers. Bands use each request's own model lines. The
  /// multiple and over-spend come from `judge`; both are estimates that ignore the cost of
  /// rebuilding the context in a new session.
  static func row(
    id: String, cli: String, title: String, requests: [AILimitRequest], emphasized: Bool = false,
    sessionID: String? = nil, note: String = ""
  ) -> AILimitListRow {
    let sorted = ordered(requests)
    let latest = sorted.last(where: \.isMain) ?? sorted.last
    var row = AILimitListRow(
      id: id, cli: cli, title: title, model: latest?.model ?? "", effort: latest?.effort ?? "")
    row.emphasized = emphasized
    row.sessionID = sessionID
    row.lines = latest?.lines
    var counts: [String: Int] = [:]
    var over: [Sample] = []
    var rest: [Sample] = []
    var flatOver: [Sample] = []
    var flatRest: [Sample] = []
    var flatRequests = 0
    var flatCost = AILimitMoney()
    var hasLimit = false
    for request in sorted {
      let limit = request.lines
      counts[request.model, default: 0] += 1
      let index = bandIndex(request.context, limit: limit)
      row.bands[index].requests += 1
      guard let official = request.official?.total, let channel = request.channel?.total else {
        row.unpriced += 1
        continue
      }
      row.bands[index].money.add(.init(official: official, channel: channel))
      row.bands[index].pricedRequests += 1
      guard let limit else { continue }
      hasLimit = true
      let sample = Sample(official: official, channel: channel, baseline: limit.baseline)
      if index == 2 { over.append(sample) } else if index == 0 { rest.append(sample) }
      guard limit.hasSecondLimit else { continue }
      // Flat case: the channel does not double, so costs above 272k halve back.
      let factor = request.doubled ? 0.5 : 1
      let flat = Sample(
        official: official * factor, channel: channel * factor, baseline: limit.baseline)
      if request.context > limit.flatLimit {
        flatOver.append(flat)
        flatRequests += 1
        flatCost.add(.init(official: flat.official, channel: flat.channel))
      } else if request.context <= limit.flatWarn {
        flatRest.append(flat)
      }
    }
    if hasLimit { (row.multiple, row.overSpend) = judge(over: over, rest: rest) }
    if let lines = row.lines, lines.hasSecondLimit {
      row.flat = .init(
        limit: lines.flatLimit, requests: flatRequests, cost: flatCost,
        overSpend: judge(over: flatOver, rest: flatRest).1)
    }
    row.percents = percents(row.bands.map(\.money.official))
    if counts.count > 1 {
      row.modelLines = counts.sorted { ($1.value, $0.key) < ($0.value, $1.key) }.map {
        .init(model: $0.key, limit: AIModelLimit.preset(for: $0.key), requests: $0.value)
      }
      if note.isEmpty { row.note = "另 \(counts.count - 1) 个模型" }
    }
    if !note.isEmpty { row.note = note }
    return row
  }

  /// Largest over-limit spend first, then the larger share above the limit.
  static func sorted(_ rows: [AILimitListRow]) -> [AILimitListRow] {
    func spend(_ row: AILimitListRow) -> Double { row.overSpend?.official ?? 0 }
    func share(_ row: AILimitListRow) -> Double {
      let total = row.input.official
      return total > 0 ? row.bands[2].money.official / total : 0
    }
    return rows.sorted {
      if spend($0) != spend($1) { return spend($0) > spend($1) }
      if share($0) != share($1) { return share($0) > share($1) }
      return $0.title < $1.title
    }
  }

  // MARK: Trend markers

  static func switches(_ requests: [AILimitRequest]) -> [AILimitSwitch] {
    var result: [AILimitSwitch] = []
    for index in requests.indices.dropFirst() {
      let previous = requests[index - 1]
      let current = requests[index]
      let kind: AILimitSwitch.Kind
      if !current.model.isEmpty, !previous.model.isEmpty, current.model != previous.model {
        kind = .model(from: previous.model, to: current.model)
      } else if current.model == previous.model, !current.effort.isEmpty,
        !previous.effort.isEmpty, current.effort != previous.effort
      {
        kind = .effort(from: previous.effort, to: current.effort)
      } else {
        continue
      }
      result.append(
        .init(
          index: index, kind: kind,
          turnMultiple: ratio(current.official?.total, previous.official?.total),
          afterReadMultiple: afterReadMultiple(requests, at: index)))
    }
    return result
  }

  private static func ratio(_ value: Double?, _ base: Double?) -> Double? {
    guard let value, let base, base > 0 else { return nil }
    return value / base
  }

  private static func readPrice(_ request: AILimitRequest) -> Double? {
    guard request.read > 0, let cost = request.official?.read else { return nil }
    return cost / Double(request.read)
  }

  /// First priced cache-read price at or after the switch over the last one before it.
  static func afterReadMultiple(_ requests: [AILimitRequest], at index: Int) -> Double? {
    let before = requests[..<index].reversed().lazy.compactMap(readPrice).first
    let after = requests[index...].lazy.compactMap(readPrice).first
    return ratio(after, before)
  }

  static func trend(_ requests: [AILimitRequest]) -> AILimitTrend {
    let main = ordered(requests.filter(\.isMain))
    let latest = main.last?.lines
    // Lines and tints appear only once the session has come near the limit.
    let drawn = latest.flatMap { limit in
      (main.map(\.context).max() ?? 0) >= limit.warn ? limit : nil
    }
    var overStart: Int?
    var flatStart: Int?
    if drawn != nil {
      overStart = main.firstIndex { request in
        request.lines.map { request.context > $0.limit } ?? false
      }
      flatStart = main.firstIndex(where: flatHit)
    }
    return .init(
      requests: main, limit: drawn, overStart: overStart, flatStart: flatStart,
      switches: switches(main), hasWriteLayer: main.contains { $0.write > 0 })
  }

  private static func flatHit(_ request: AILimitRequest) -> Bool {
    guard let limit = request.lines, limit.hasSecondLimit else {
      return false
    }
    return request.context > limit.flatLimit
  }

  /// Band of one trend point against its own model, for the hover line.
  static func pointBand(_ request: AILimitRequest) -> (limit: Int, band: Int)? {
    request.lines.map {
      ($0.limit, request.band ?? bandIndex(request.context, limit: $0))
    }
  }
}
