import Foundation
import PaxProto
import Testing

@testable import PaxClient

@Suite("AI 倍率映射")
struct AIRepriceMappingTests {
  @Test("档案的模型倍率经 proto 往返且保存时整体带上")
  func modelMultipliersRoundTrip() {
    var summary = Pax_V1_AIProfileSummary()
    summary.id = "p"
    summary.multiplier = 0.265
    summary.modelMultipliers = ["claude-opus-5-5": 0.5]
    let profile = AIProfile(summary)
    #expect(profile.modelMultipliers == ["claude-opus-5-5": 0.5] && profile.multiplier == 0.265)
    #expect(profile.proto.modelMultipliers == ["claude-opus-5-5": 0.5])
  }

  @Test("重算结果以美元计")
  func repriceResultUsesDollars() {
    var result = Pax_V1_RepriceAIUsageResult()
    result.requests = 3
    result.aggregatedRequests = 1
    result.oldCostMicro = 18_000_000
    result.newCostMicro = 4_770_000
    result.dryRun = true
    let mapped = AIRepriceResult(result)
    #expect(mapped.requests == 3 && mapped.aggregatedRequests == 1 && mapped.dryRun)
    #expect(mapped.oldCost == 18 && abs(mapped.newCost - 4.77) < 1e-9)
  }
}
