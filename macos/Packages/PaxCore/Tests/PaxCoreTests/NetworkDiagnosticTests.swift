import Foundation
import PaxProto
import Testing

@testable import PaxClient
@testable import PaxCore

@MainActor @Suite("网络诊断环境与生命周期") struct NetworkDiagnosticTests {
  @Test("时区对照按日期与实际偏移，语言保持事实")
  func timezoneEvidence() {
    let date = Date(timeIntervalSince1970: 1_733_011_200)
    let local = NetworkDiagnosticEnvironment(
      timezone: "Asia/Shanghai", languages: ["zh-Hans", "en"], region: "zh_CN", at: date)
    #expect(local.timezoneComparison("").contains("无法对照"))
    #expect(local.timezoneComparison("US").contains("无法对照"))
    #expect(local.timezoneComparison("Asia/Shanghai").contains("相同"))
    #expect(local.timezoneComparison("Asia/Singapore").contains("偏移相同"))
    #expect(local.timezoneComparison("America/Los_Angeles").contains("不同"))
    #expect(local.languages == ["zh-Hans", "en"])
  }
  @Test("关闭后迟到异常不污染新状态，重复开始不并发")
  func cancellation() async {
    actor Gate {
      var count = 0
      func wait() async throws -> NetworkDiagnosticReport {
        count += 1
        try await Task.sleep(for: .seconds(30))
        throw ClientError.unavailable
      }
    }
    let gate = Gate()
    let store = NetworkDiagnosticStore(run: { _, _ in try await gate.wait() })
    store.start()
    store.start()
    for _ in 0..<100 where await gate.count == 0 { await Task.yield() }
    #expect(await gate.count == 1)
    store.clear()
    for _ in 0..<10 { await Task.yield() }
    #expect(!store.busy)
    #expect(store.report == nil)
    #expect(store.message.contains("尚未检测"))
  }
  @Test("首次连接不混入重复延迟；导出冻结且默认脱敏")
  func coldWarmAndExport() {
    let raw = Pax_V1_NetworkDiagnosticReport.with {
      $0.targetRouteID = "route-fixture"
      $0.targetRouteName = "Private route"
      $0.at = .init(date: Date(timeIntervalSince1970: 1_733_011_200))
      $0.exit = .with {
        $0.ip = "203.0.113.24"
        $0.at = .init(date: Date(timeIntervalSince1970: 1_733_011_200))
      }
      $0.probes = [900, 100, 120, 140].enumerated().map { index, value in
        .with {
          $0.target = "https://example.test/"
          $0.attempt = UInt32(index + 1)
          $0.durationMs = UInt64(value)
          $0.expectedResponse = true
        }
      }
      $0.facts = [
        .with {
          $0.group = "network"
          $0.label = "宣告前缀"
          $0.value = "203.0.113.0/24"
        },
        .with {
          $0.group = "routing"
          $0.label = "claude.ai"
          $0.value = "private other route"
          $0.state = "information"
        },
        .with {
          $0.label = "出口 203.0.113.24"
          $0.value = "Private route"
          $0.state = "information"
        },
      ]
    }
    let report = PaxClient.NetworkDiagnosticReport(raw)
    let summary = NetworkDiagnosticPresentation.targets(report)[0]
    #expect(summary.firstMS == 900)
    #expect(summary.median == 120)
    #expect(summary.range == "100–140 ms")
    let text = NetworkDiagnosticPresentation.markdown(
      report, services: nil, environment: nil, stale: false)
    #expect(!text.contains("203.0.113.24"))
    #expect(!text.contains("203.0.113.0/24"))
    #expect(!text.contains("Private route"))
    #expect(!text.contains("private other route"))
    #expect(
      text
        == NetworkDiagnosticPresentation.markdown(
          report, services: nil, environment: nil, stale: false))
  }

  @Test("时区显示保留负号与夏令时，未知不补猜")
  func offsets() {
    #expect(
      NetworkDiagnosticPresentation.utcOffset(
        "America/Los_Angeles", at: Date(timeIntervalSince1970: 1_733_011_200)) == "UTC−08:00")
    #expect(
      NetworkDiagnosticPresentation.utcOffset(
        "America/Los_Angeles", at: Date(timeIntervalSince1970: 1_720_000_000)) == "UTC−07:00")
    #expect(NetworkDiagnosticPresentation.utcOffset("unknown", at: Date()).contains("未知"))
    #expect(NetworkDiagnosticPresentation.languageName("en-TW").contains("en-TW"))
  }

}
