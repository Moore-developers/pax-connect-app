import Foundation
import PaxProto
import SwiftProtobuf
import Testing

@testable import PaxClient

@Suite("互斥来源与DNS分组映射") struct TrafficSourceMappingTests {
  @Test("协议20附属未知/DNS行和来源证据完整保留") func sourceAndGroups() {
    var row = Pax_V1_TrafficRow()
    row.id = "service"
    row.sourceKind = "system"
    row.sourceLabel = "trustd"
    row.sourceEvidence = "保存的系统服务路径"
    var page = Pax_V1_TrafficPage()
    page.items = [row]
    page.unknownSource = row
    page.unknownSource.id = "unknown"
    page.dnsSummary = row
    page.dnsSummary.id = "dns"
    let mapped = TrafficPage(page)
    #expect(mapped.items[0].sourceKind == "system" && mapped.items[0].sourceLabel == "trustd")
    #expect(mapped.unknownSource?.id == "unknown" && mapped.dnsSummary?.id == "dns")
    var detail = Pax_V1_TrafficDetail()
    detail.summary = row
    detail.dnsGroupID = "g"
    detail.dnsGroupTotal = 2
    var g = Pax_V1_DNSTrafficGroup()
    g.id = "g"
    g.domain = "api.example.test"
    g.queryType = "A"
    g.result = "NXDOMAIN"
    g.queries = 3
    g.lastAt = Google_Protobuf_Timestamp(date: Date(timeIntervalSince1970: 100))
    g.latencyKnown = true
    g.latencyMs = 0
    detail.dnsGroups = [g]
    var flow = Pax_V1_FlowRecord()
    flow.sourceKind = "tool"
    flow.sourceLabel = "命令行工具"
    flow.sourceEvidence = "/usr/bin/curl"
    detail.items = [flow]
    let d = TrafficDetail(detail)
    #expect(d.dnsGroupID == "g" && d.dnsGroupTotal == 2 && d.dnsGroups[0].queries == 3)
    #expect(d.dnsGroups[0].latencyKnown && d.dnsGroups[0].latencyMS == 0)
    #expect(d.items[0].sourceKind == "tool" && d.items[0].sourceEvidence == "/usr/bin/curl")
  }
}
