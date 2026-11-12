import Foundation
import PaxClient
import Testing

@testable import PaxCore

@Suite("AI 倍率与历史重算")
struct AIRepriceTests {
  private var calendar: Calendar {
    var c = Calendar(identifier: .gregorian)
    c.timeZone = TimeZone(identifier: "Asia/Shanghai")!
    return c
  }
  private func date(_ y: Int, _ m: Int, _ d: Int, hour: Int = 12) -> Date {
    calendar.date(from: DateComponents(year: y, month: m, day: d, hour: hour))!
  }

  @Test("倍率最多 3 位小数并去尾零")
  func formatter() {
    #expect(AIUsageFormatting.multiplier(0.265) == "0.265×")
    #expect(AIUsageFormatting.multiplier(0.26) == "0.26×")
    #expect(AIUsageFormatting.multiplier(1) == "1×")
    #expect(AIUsageFormatting.multiplier(0.3) == "0.3×")
    #expect(AIUsageFormatting.multiplier(100) == "100×")
    #expect(AIUsageFormatting.multiplier(0) == "0×")
    #expect(AIUsageFormatting.multiplierInput(0.265) == "0.265")
  }

  @Test("输入校验：至多 3 位小数，0 到 100，留空为继承")
  func validation() {
    #expect(AIMultiplierInput.parse("") == .empty)
    #expect(AIMultiplierInput.parse("  ") == .empty)
    #expect(AIMultiplierInput.parse("0.265") == .value(0.265))
    #expect(AIMultiplierInput.parse("0") == .value(0))
    #expect(AIMultiplierInput.parse("100") == .value(100))
    #expect(AIMultiplierInput.parse("100.000") == .value(100))
    for bad in ["0.2651", "100.001", "-1", "abc", "1e2", ".5", "1.", "1.2.3", "１"] {
      #expect(AIMultiplierInput.parse(bad) == .invalid, "\(bad)")
    }
  }

  @Test("当日、本周（周一起）、指定日期、全部")
  func ranges() {
    let wednesday = date(2026, 10, 28)
    #expect(
      AIReprice.range(.today, now: wednesday, calendar: calendar)
        == .days(from: "2026-10-28", to: "2026-10-28"))
    #expect(
      AIReprice.range(.week, now: wednesday, calendar: calendar)
        == .days(from: "2026-10-26", to: "2026-10-28"))
    #expect(
      AIReprice.range(.week, now: date(2026, 10, 26), calendar: calendar)
        == .days(from: "2026-10-26", to: "2026-10-26"))
    // Sunday belongs to the week that started the previous Monday, across a month boundary.
    #expect(
      AIReprice.range(.week, now: date(2026, 11, 1), calendar: calendar)
        == .days(from: "2026-10-26", to: "2026-11-01"))
    #expect(
      AIReprice.range(
        .custom(date(2026, 3, 1, hour: 23), date(2026, 3, 9, hour: 0)), now: wednesday,
        calendar: calendar)
        == .days(from: "2026-03-01", to: "2026-03-09"))
    #expect(AIReprice.range(.all, now: wednesday, calendar: calendar) == .all)
  }

  @Test("试算文案含汇总估算提示，无请求另说明")
  func previewText() {
    let plain = AIRepriceResult(requests: 12, oldCost: 1, newCost: 0.8, dryRun: true)
    #expect(AIReprice.previewText(plain) == "将更新 12 条请求，费用 $1.000 → $0.800")
    let mixed = AIRepriceResult(
      requests: 12, aggregatedRequests: 3, oldCost: 1, newCost: 0.8, dryRun: true)
    #expect(AIReprice.previewText(mixed).hasSuffix("（其中 3 条按小时汇总估算）"))
    #expect(
      AIReprice.previewText(.init(requests: 0, oldCost: 0, newCost: 0, dryRun: true)).contains("没有")
    )
  }

  @Test("受控错误文案")
  func messages() {
    #expect(AIReprice.message(ProductError(code: "ai_usage_busy")) == "正在采集，请稍后重试")
    #expect(AIReprice.message(ProductError(code: "ai_profile_not_found")).contains("不存在"))
    #expect(AIReprice.message(ProductError(code: "x")) == "更新失败，请稍后重试")
  }
}

private actor Calls {
  var items: [String] = []
  func add(_ value: String) { items.append(value) }
}

@MainActor @Suite("AI 倍率重算流程")
struct AIRepriceFlowTests {
  private func store(_ calls: Calls, fail: String? = nil) -> AIUsageDataStore {
    AIUsageDataStore(
      buckets: { _, _, _, _ in AIUsageBucketPage(buckets: [], state: .init(), notModified: false) },
      summary: { AIUsageBucketPage(buckets: [], state: .init(), notModified: false) },
      logs: { _, _, _ in
        AIUsageLogPage(requests: [], all: 0, success: 0, failed: 0, totals: .init())
      },
      reprice: { id, model, range, dry in
        await calls.add("\(id)|\(model ?? "-")|\(range)|\(dry)")
        if let fail { throw ProductError(code: fail) }
        return AIRepriceResult(
          requests: 5, aggregatedRequests: 1, oldCost: 2, newCost: 1, dryRun: dry)
      })
  }
  private let request = AIRepriceFlow.Request(
    profileID: "p", model: "m", range: .days(from: "2026-10-28", to: "2026-10-28"))

  @Test("先试算，确认才执行；取消不调用执行")
  func previewThenConfirm() async {
    let calls = Calls()
    let flow = AIRepriceFlow(data: store(calls))
    await flow.confirm()
    #expect(await calls.items.isEmpty)  // no preview, no run
    await flow.preview(request)
    #expect(
      flow.state
        == .preview(.init(requests: 5, aggregatedRequests: 1, oldCost: 2, newCost: 1, dryRun: true))
    )
    flow.reset()
    await flow.confirm()
    #expect(await calls.items.count == 1)
    await flow.preview(request)
    await flow.confirm()
    let items = await calls.items
    #expect(items.count == 3 && items[2].hasSuffix("|false") && items[1].hasSuffix("|true"))
    if case .done(let result) = flow.state {
      #expect(!result.dryRun && result.requests == 5)
    } else {
      Issue.record("应为 done")
    }
  }

  @Test("忙与档案不存在给出受控提示；无连接不可用")
  func failures() async {
    let flow = AIRepriceFlow(data: store(Calls(), fail: "ai_usage_busy"))
    await flow.preview(request)
    #expect(flow.state == .failed("正在采集，请稍后重试"))
    let gone = AIRepriceFlow(data: store(Calls(), fail: "ai_profile_not_found"))
    await gone.preview(request)
    #expect(gone.state == .failed("Profile 不存在，请刷新后重试"))
    let none = AIRepriceFlow(data: nil)
    #expect(!none.available)
    await none.preview(request)
    if case .failed = none.state {} else { Issue.record("应失败") }
  }
}
