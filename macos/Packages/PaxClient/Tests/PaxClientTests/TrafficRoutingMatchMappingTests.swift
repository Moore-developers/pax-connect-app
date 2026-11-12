import Foundation
import PaxProto
import Testing

@testable import PaxClient

@Suite("最近命中规则映射") struct TrafficRoutingMatchMappingTests {
  @Test func optionalRecordedDecision() {
    var wire = Pax_V1_TrafficRow()
    #expect(TrafficRow(wire).latestMatch == nil)
    wire.latestMatch.at = .init(date: Date(timeIntervalSince1970: 1_800_000_001))
    wire.latestMatch.rule = "application=fixture.app"
    wire.latestMatch.source = "Pax 包：优先规则"
    wire.latestMatch.applicationID = "fixture.app"
    wire.latestMatch.application = "Frozen App"
    wire.latestMatch.identity = "session/epoch/connection"
    let row = TrafficRow(wire)
    #expect(row.latestMatch?.at == Date(timeIntervalSince1970: 1_800_000_001))
    #expect(row.latestMatch?.rule == "application=fixture.app")
    #expect(row.latestMatch?.source == "Pax 包：优先规则")
    #expect(row.latestMatch?.applicationID == "fixture.app")
    #expect(row.latestMatch?.application == "Frozen App")
    #expect(row.latestMatch?.identity == "session/epoch/connection")
  }
}
