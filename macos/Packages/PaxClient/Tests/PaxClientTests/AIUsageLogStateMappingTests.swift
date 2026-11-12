import PaxProto
import Testing

@testable import PaxClient

@Suite("AI日志状态映射") struct AIUsageLogStateMappingTests {
  @Test func existingWireWatermark() {
    var proto = Pax_V1_AIUsageLogs()
    proto.state.revision = 12
    proto.state.generation = 3
    let page = AIUsageLogPage(proto)
    #expect(page.state?.revision == 12 && page.state?.generation == 3)
    #expect(AIUsageLogPage(Pax_V1_AIUsageLogs()).state == nil)
  }
}
