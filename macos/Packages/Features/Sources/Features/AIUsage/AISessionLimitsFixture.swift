import Foundation

/// In-memory sessions for the demo entry only. Per-request rows are generated deterministically
/// from the plans below; every number shown by the limits view derives from them.
enum AISessionLimitsFixture {
  private struct Price {
    var fresh: Double
    var read: Double
    var write: Double = 0
    /// Official rule: above this context the whole input class doubles.
    var doubleAbove: Int?
  }
  private static let prices: [String: Price] = [
    "gpt-6.1-sol": .init(fresh: 1.25, read: 0.125, doubleAbove: 272_000),
    "gpt-6.1-terra": .init(fresh: 0.5, read: 0.05, doubleAbove: 272_000),
    "gpt-6-astra": .init(fresh: 14, read: 1.4),
    "gpt-6.2-nova": .init(fresh: 1.5, read: 0.15, doubleAbove: 272_000),
    "claude-opus-5-5": .init(fresh: 5, read: 0.5, write: 6.25),
    "claude-opus-5-6": .init(fresh: 5, read: 0.5, write: 6.25),
    "claude-fable-5-2": .init(fresh: 8, read: 0.8, write: 10),
    "claude-sonnet-5-5": .init(fresh: 3, read: 0.3, write: 3.75),
    "claude-haiku-5-5": .init(fresh: 0.1, read: 0.01, write: 0.125),
    "deepseek-v4": .init(fresh: 0.3, read: 0.03),
  ]
  /// Channel amounts are the official ones times the recorded rate.
  private static let channelRate = 0.35

  private struct Phase {
    var from: Int
    var model: String
    var effort: String
  }
  private struct Plan {
    var id: String
    var cli: String
    var title: String
    var count: Int
    var start: Int
    var end: Int
    var phases: [Phase]
    var writes = false
    /// The last N requests miss the cache almost entirely (cost-limit scenario).
    var missTail = 0
    var subagentEvery: Int?
    var unpriced: Range<Int> = 0..<0
    /// Independent child session (Codex, DeepSeek): folds into `parent` as sub-agent `agent`.
    var parent: String?
    var agent: String?
    /// The first N requests carry no native turn.
    var unknownTurns = 0
  }

  private struct Generator {
    var state: UInt64
    mutating func next() -> Double {
      state &+= 0x9E37_79B9_7F4A_7C15
      var z = state
      z = (z ^ (z >> 30)) &* 0xBF58_476D_1CE4_E5B9
      z = (z ^ (z >> 27)) &* 0x94D0_49BB_1331_11EB
      return Double(z ^ (z >> 31)) / Double(UInt64.max)
    }
  }

  private static let plans: [Plan] = [
    .init(
      id: "codex-route", cli: "codex", title: "重构 daemon 路由链路", count: 360, start: 30_000,
      end: 384_000,
      phases: [
        .init(from: 0, model: "gpt-6.1-sol", effort: "high"),
        .init(from: 60, model: "gpt-6.1-sol", effort: "medium"),
        .init(from: 120, model: "gpt-6.1-sol", effort: "high"),
        .init(from: 190, model: "gpt-6.1-sol", effort: "medium"),
        .init(from: 230, model: "gpt-6.1-sol", effort: "high"),
        .init(from: 275, model: "gpt-6.1-terra", effort: "high"),
        .init(from: 300, model: "gpt-6.1-terra", effort: "medium"),
      ], unpriced: 210..<213),
    .init(
      id: "codex-tests", cli: "codex", title: "补全用量采集测试", count: 120, start: 20_000,
      end: 288_000,
      phases: [
        .init(from: 0, model: "gpt-6.1-terra", effort: "high"),
        .init(from: 60, model: "gpt-6.1-sol", effort: "xhigh"),
      ]),
    .init(
      id: "codex-ledger", cli: "codex", title: "批量迁移历史账本脚本", count: 90, start: 40_000,
      end: 118_000, phases: [.init(from: 0, model: "gpt-6-astra", effort: "high")], missTail: 12),
    .init(
      id: "codex-channel", cli: "codex", title: "探索新渠道计价", count: 80, start: 30_000,
      end: 258_000, phases: [.init(from: 0, model: "gpt-6.2-nova", effort: "medium")]),
    .init(
      id: "claude-audit", cli: "claude", title: "审计会话分析口径", count: 150, start: 25_000,
      end: 172_000,
      phases: [
        .init(from: 0, model: "claude-opus-5-5", effort: "medium"),
        .init(from: 80, model: "claude-opus-5-5", effort: "high"),
      ], writes: true, subagentEvery: 5),
    .init(
      id: "claude-docs", cli: "claude", title: "整理接入文档", count: 100, start: 20_000,
      end: 64_000, phases: [.init(from: 0, model: "claude-sonnet-5-5", effort: "medium")],
      writes: true),
    .init(
      id: "claude-notes", cli: "claude", title: "生成发布说明", count: 60, start: 10_000,
      end: 38_000, phases: [.init(from: 0, model: "claude-haiku-5-5", effort: "low")],
      writes: true),
    .init(
      id: "claude-trial", cli: "claude", title: "试验新模型", count: 50, start: 20_000,
      end: 232_000, phases: [.init(from: 0, model: "claude-opus-5-6", effort: "high")],
      writes: true),
    .init(
      id: "dsh-debug", cli: "dsh", title: "DeepSeek 排查", count: 70, start: 20_000,
      end: 45_000, phases: [.init(from: 0, model: "deepseek-v4", effort: "")],
      unknownTurns: 12),
    .init(
      id: "codex-astra", cli: "codex", title: "全量回放账单核对", count: 100, start: 40_000,
      end: 232_000, phases: [.init(from: 0, model: "gpt-6-astra", effort: "high")]),
    .init(
      id: "claude-fable", cli: "claude", title: "评审新计价方案", count: 80, start: 25_000,
      end: 214_000, phases: [.init(from: 0, model: "claude-fable-5-2", effort: "high")],
      writes: true),
    .init(
      id: "claude-long", cli: "claude", title: "整理长文档上下文", count: 70, start: 30_000,
      end: 268_000, phases: [.init(from: 0, model: "claude-sonnet-5-5", effort: "medium")],
      writes: true),
    .init(
      id: "codex-docs", cli: "codex", title: "更新使用说明", count: 50, start: 15_000,
      end: 92_000, phases: [.init(from: 0, model: "gpt-6.1-sol", effort: "low")]),
    .init(
      id: "codex-tests-explorer", cli: "codex", title: "explorer", count: 24, start: 14_000,
      end: 52_000, phases: [.init(from: 0, model: "gpt-6.1-terra", effort: "medium")],
      parent: "codex-tests", agent: "explorer"),
    .init(
      id: "codex-route-worker", cli: "codex", title: "worker", count: 30, start: 20_000,
      end: 140_000, phases: [.init(from: 0, model: "gpt-6.1-sol", effort: "medium")],
      parent: "codex-route", agent: "worker"),
  ]

  static let sessions: [AILimitSessionInput] = plans.enumerated().map { offset, plan in
    // A child session ends when its parent does.
    let slot = plan.parent.flatMap { id in plans.firstIndex { $0.id == id } } ?? offset
    return .init(
      id: plan.id, cli: plan.cli, title: plan.title, parent: plan.parent, agentName: plan.agent,
      requests: requests(plan, seed: offset, slot: slot))
  }
  static let model = AISessionLimitsMath.model(sessions)

  private static func cost(_ price: Price, request: AILimitRequest) -> AILimitCosts {
    let doubled = request.doubled ? 2.0 : 1
    return .init(
      fresh: Double(request.fresh) / 1e6 * price.fresh * doubled,
      read: Double(request.read) / 1e6 * price.read * doubled,
      write: Double(request.write) / 1e6 * price.write * doubled)
  }

  private static func request(
    plan: Plan, at: Date, turn: Int?, phase: Phase, agent: String? = nil, fresh: Int, read: Int,
    write: Int, priced: Bool = true
  ) -> AILimitRequest {
    var result = AILimitRequest(
      at: at, turn: turn, model: phase.model, effort: phase.effort, agent: agent, fresh: fresh,
      read: read, write: write)
    result.lines = AIModelLimit.preset(for: phase.model)
    guard priced, let price = prices[phase.model] else { return result }
    if let threshold = price.doubleAbove, result.context > threshold { result.doubled = true }
    let official = cost(price, request: result)
    result.official = official
    result.channel = .init(
      fresh: official.fresh * channelRate, read: official.read * channelRate,
      write: official.write * channelRate)
    return result
  }

  private static func requests(_ plan: Plan, seed: Int, slot: Int) -> [AILimitRequest] {
    var random = Generator(state: UInt64(seed + 1) * 7919)
    let finish = Date(timeIntervalSince1970: 1_794_200_000 - Double(slot) * 5_400)
    var cursor = finish.addingTimeInterval(-Double(plan.count) * 95)
    var previous = 0
    var result: [AILimitRequest] = []
    for index in 0..<plan.count {
      cursor.addTimeInterval(45 + random.next() * 100)
      let progress = Double(index) / Double(max(1, plan.count - 1))
      let noise = 1 + (random.next() - 0.5) * 0.03
      let context = max(
        previous, Int(Double(plan.start) + Double(plan.end - plan.start) * progress * noise))
      let phase = plan.phases.last { $0.from <= index } ?? plan.phases[0]
      let increment = max(300, context - previous)
      var fresh = plan.writes ? 200 + Int(random.next() * 1_300) : min(context, increment)
      // Real Claude data: cache writes are 65%-78% of an opus request's input cost, so they are
      // about 15%-21% of the context in tokens (write price is 12.5x the read price).
      let share = 0.15 + random.next() * 0.06
      var write = plan.writes ? min(context - fresh, Int(Double(context) * share)) : 0
      var read = context - fresh - write
      if index >= plan.count - plan.missTail {
        fresh = Int(Double(context) * 0.88)
        read = context - fresh
      } else if index == 0 || random.next() < 0.04 {
        // A cache miss: nothing is read, the whole context is written or billed as fresh input.
        fresh = plan.writes ? min(fresh, 1_500) : context
        write = plan.writes ? context - fresh : 0
        read = 0
      }
      let turn: Int? = index < plan.unknownTurns ? nil : index / 6
      result.append(
        request(
          plan: plan, at: cursor, turn: turn, phase: phase, fresh: fresh, read: read,
          write: write, priced: !plan.unpriced.contains(index)))
      if let every = plan.subagentEvery, index % every == 2 {
        let size = 15_000 + Int(random.next() * 25_000)
        result.append(
          request(
            plan: plan, at: cursor.addingTimeInterval(1), turn: turn,
            phase: .init(from: 0, model: "claude-haiku-5-5", effort: ""),
            agent: ["Explore", "code-reviewer", "general-purpose"][index / every % 3], fresh: 900,
            read: size - 7_000,
            write: 6_100))
      }
      previous = context
    }
    return result
  }
}
