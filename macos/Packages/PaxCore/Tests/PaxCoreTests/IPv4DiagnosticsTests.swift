import Foundation
import Testing

@testable import PaxCore

@MainActor @Suite("IPv4运行诊断")
struct IPv4DiagnosticsTests {
  @Test("IPv6关闭、不适用和未知不能混用，复制报告与主表同源")
  func states() {
    let cases = [
      ("disabled", "关闭（系统已关闭）"),
      ("link_local", "关闭（仅本地链路）"),
      ("unavailable", "不可用，已使用 IPv4"),
      ("unknown", "无法确认"),
    ]
    for (state, expected) in cases {
      let finding = TrafficPathFinding(kind: "ip_family", subject: "ipv6", state: state)
      #expect(TrafficPathPresentation.title(finding) == "公网 IPv6")
      #expect(TrafficPathPresentation.detail(finding) == expected)
      let report = TrafficPathDiagnostics(at: Date(), findings: [finding])
      #expect(TrafficPathPresentation.text(report).contains("公网 IPv6：" + expected))
    }
    #expect(
      TrafficPathPresentation.detail(
        .init(kind: "ip_family", subject: "connection", state: "ipv4")) == "IPv4")
  }
}
