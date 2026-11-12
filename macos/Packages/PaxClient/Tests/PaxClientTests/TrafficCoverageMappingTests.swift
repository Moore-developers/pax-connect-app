import Foundation
import PaxProto
import SwiftProtobuf
import Testing

@testable import PaxClient

@Suite("系统流量与延迟覆盖映射") struct TrafficCoverageMappingTests {
  @Test("工具小计独立映射，items仍带工具供统一分页")
  func processTotals() {
    var page = Pax_V1_TrafficPage()
    page.upload = 10
    page.otherProcessUpload = 20
    page.otherProcessDownload = 30
    page.otherProcessConnections = 4
    var tool = Pax_V1_TrafficRow()
    tool.sourceKind = "tool"
    var exit = Pax_V1_TrafficExit()
    exit.routeID = "real-route"
    exit.routeName = "线路"
    exit.direction = "proxy"
    exit.upload = 8
    exit.download = 9
    exit.connections = 7
    tool.exits = [exit]
    page.items = [tool]
    let value = TrafficPage(page)
    #expect(value.upload == 10 && value.otherProcessUpload == 20)
    #expect(value.otherProcessDownload == 30 && value.otherProcessConnections == 4)
    #expect(value.items.first?.sourceKind == "tool")
    #expect(value.items.first?.exits.first?.routeID == "real-route")
    #expect(value.items.first?.exits.first?.download == 9)
    #expect(value.items.first?.exits.first?.connections == 7)
  }

  @Test("协议19保留DNS类型、缺测字节与完整计数，不受明细分页影响")
  func mapping() {
    var wire = Pax_V1_TrafficRow()
    wire.id = "dns"
    wire.kind = "dns"
    wire.bytesKnown = false
    wire.connections = 418
    wire.validLatencyCount = 162
    wire.missingLatencyCount = 256
    wire.failedCount = 2
    wire.earlierCount = 3
    wire.latencySampled = true
    var s = Pax_V1_TrafficLatencySample()
    s.at = Google_Protobuf_Timestamp(date: Date(timeIntervalSince1970: 100))
    s.milliseconds = 0
    s.known = true
    wire.latencySamples = [s]
    var detail = Pax_V1_TrafficDetail()
    detail.summary = wire
    let mapped = TrafficDetail(detail)
    #expect(mapped.summary.kind == "dns" && !mapped.summary.bytesKnown)
    #expect(mapped.summary.validLatencyCount == 162 && mapped.summary.missingLatencyCount == 256)
    #expect(
      mapped.summary.failedCount == 2 && mapped.summary.earlierCount == 3
        && mapped.summary.latencySampled)
    #expect(
      mapped.summary.latencySamples.first?.known == true
        && mapped.summary.latencySamples.first?.milliseconds == 0)
  }
}

@Suite("真实连接响应序列") struct TrafficResponseMappingTests {
  @Test("独立序列保留key、同刻点、0ms与读取状态，不覆盖汇总")
  func responseMapping() {
    var row = Pax_V1_TrafficRow()
    row.connections = 900
    row.validLatencyCount = 700
    row.latencyAggregated = true
    row.p95Ms = 120
    var point = Pax_V1_TrafficResponsePoint()
    point.connectionKey = "a"
    point.startedAt = Google_Protobuf_Timestamp(date: Date(timeIntervalSince1970: 100))
    row.recentResponses = [point]
    point.connectionKey = "b"
    row.recentResponses.append(point)
    row.recentResponsesState = "partial"
    var detail = Pax_V1_TrafficDetail()
    detail.summary = row
    detail.retainedConnectionCount = 37
    detail.recentResponses = row.recentResponses
    detail.recentResponsesState = "failed"
    detail.recentResponsesError = "history_store_failed"
    let value = TrafficDetail(detail)
    #expect(value.retainedConnectionCount == 37)
    #expect(value.summary.connections == 900 && value.summary.validLatencyCount == 700)
    #expect(value.summary.latencyAggregated && value.summary.p95MS == 120)
    #expect(value.summary.recentResponses.map(\.connectionKey) == ["a", "b"])
    #expect(value.summary.recentResponses.allSatisfy { $0.milliseconds == 0 })
    #expect(value.summary.recentResponsesState == "partial")
    #expect(value.recentResponses.count == 2 && value.recentResponsesState == "failed")
    #expect(value.recentResponsesError == "history_store_failed")
  }
}

@Suite("路线使用记录与流量排序映射") struct RouteUsageMappingTests {
  @Test("使用记录映射时间窗连接总数、分桶连接数，保留此刻打开的连接数")
  func routeUsageConnections() {
    var wire = Pax_V1_RouteUsageResponse()
    wire.connections = 3
    wire.windowConnections = 41
    var bucket = Pax_V1_RouteUsageBucket()
    bucket.at = Google_Protobuf_Timestamp(date: Date(timeIntervalSince1970: 600))
    bucket.upload = 1
    bucket.download = 2
    bucket.connections = 17
    wire.items = [bucket]
    let page = RouteUsagePage(wire)
    #expect(page.connections == 3 && page.windowConnections == 41)
    #expect(page.items.first?.connections == 17)
  }

  @Test("流量行映射毫秒时间为最近活动，0 为未知；应用页带系统服务流量")
  func trafficRecency() {
    var row = Pax_V1_TrafficRow()
    row.lastActiveAt = 1_792_310_400_500
    #expect(TrafficRow(row).lastActiveAt == Date(timeIntervalSince1970: 1_792_310_400.5))
    #expect(TrafficRow(Pax_V1_TrafficRow()).lastActiveAt == nil)
    var page = Pax_V1_TrafficPage()
    page.systemUpload = 7
    page.systemDownload = 8
    let mapped = TrafficPage(page)
    #expect(mapped.systemUpload == 7 && mapped.systemDownload == 8)
  }
}
