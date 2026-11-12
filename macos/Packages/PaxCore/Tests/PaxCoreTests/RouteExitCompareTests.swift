import Foundation
import Testing

@testable import PaxCore

struct RouteExitCompareTests {
  @Test("出口查询完成消息区分 IP 是否变化")
  func exitChangeMessage() {
    func summary(_ ip: String, at: Double, ok: Bool = true) -> RouteExitSummary {
      RouteExitSummary(
        at: Date(timeIntervalSince1970: at), success: ok, testedConfigRevision: 1, ip: ip)
    }
    let before = summary("1.1.1.1", at: 1000)
    #expect(
      RouteExitCompare.message(before: before, after: summary("1.1.1.1", at: 2000))
        == "IP 地区查询完成 · IP 未变化（1.1.1.1）")
    #expect(
      RouteExitCompare.message(before: before, after: summary("2.2.2.2", at: 2000))
        == "IP 地区查询完成 · IP 已变为 2.2.2.2")
    #expect(
      RouteExitCompare.message(before: nil, after: summary("2.2.2.2", at: 2000))
        == "IP 地区查询完成")
    // 新摘要还没到：返回 nil 等快照
    #expect(RouteExitCompare.message(before: before, after: before) == nil)
  }
}
