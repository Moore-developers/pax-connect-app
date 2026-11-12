import Foundation
import PaxProto
import Testing

@testable import PaxClient
@testable import PaxCore

@MainActor @Suite("当前环境采样与人话结果") struct EnvironmentDiagnosticTests {
  @Test("等待浏览器后台打开时取消，迟到打开回调不恢复检测或覆写停止消息")
  func cancelWhileOpeningBrowser() async {
    actor Gate {
      var pending: CheckedContinuation<Bool, Never>?
      var opening: Bool { pending != nil }
      func open() async -> Bool {
        await withCheckedContinuation { pending = $0 }
      }
      func release() {
        pending?.resume(returning: false)
        pending = nil
      }
    }
    let gate = Gate()
    let store = EnvironmentDiagnosticStore(
      request: { action, _ in
        if action == "read" { Issue.record("cancelled browser opening must not resume sampling") }
        return EnvironmentDiagnosticReport(
          .with {
            $0.sessionID = "opening-fixture"
            $0.browserURL = "http://127.0.0.1:1234/fixture/"
          })
      }, openBrowser: { _ in await gate.open() })
    store.start()
    for _ in 0..<500 where await gate.opening == false { await Task.yield() }
    #expect(await gate.opening)
    let initialReport = store.report
    store.cancel()
    let stoppedMessage = store.message
    await gate.release()
    for _ in 0..<50 { await Task.yield() }
    #expect(!store.busy)
    #expect(store.runState == .stopped)
    #expect(store.message == stoppedMessage)
    #expect(store.report == initialReport)
  }

  @Test("DNS 数量使用解析器数，国家与地址完整保留")
  func dnsCount() {
    let raw = Pax_V1_EnvironmentDiagnosticReport.with {
      $0.browser = .with {
        $0.complete = true
        $0.resolvers = (1...21).map { index in
          .with {
            $0.ip = "8.8.8.\(index)"
            $0.country = "US"
            $0.organization = "Google"
          }
        }
      }
    }
    let report = EnvironmentDiagnosticReport(raw)
    let row = EnvironmentDiagnosticPresentation.rows(report.browser, browser: true, waiting: false)
      .first { $0.title == "DNS" }
    #expect(row?.value == "发现 21 个 DNS 解析出口，均位于美国")
    #expect(row?.detail.contains("8.8.8.1") == true)
    #expect(row?.detail.contains("其余 19 个") == true)
    #expect(report.browser.resolvers.count == 21)
  }
  @Test("无 DNS 或 ICE 结果不能显示安全；局域网地址不算公网")
  func unavailable() {
    let report = EnvironmentDiagnosticReport(
      .with {
        $0.browser = .with {
          $0.complete = true
          $0.rtcState = "empty"
        }
      })
    let rows = EnvironmentDiagnosticPresentation.rows(report.browser, browser: true, waiting: false)
    #expect(rows.first { $0.title == "DNS" }?.tone == .failure)
    #expect(rows.first { $0.title == "WebRTC" }?.detail.contains("可能已禁用") == true)
    for address in [
      "192.168.1.1", "10.1.1.1", "172.16.1.2", "100.64.0.1", "fe80::1", "fd12::1", "browser.local",
      "::1",
    ] {
      #expect(!EnvironmentDiagnosticPresentation.isPublicAddress(address))
    }
    #expect(EnvironmentDiagnosticPresentation.isPublicAddress("8.8.8.8"))
  }
  @Test("时区包含国家，UTC 固定偏移不猜国家")
  func countryTimezone() {
    let date = Date(timeIntervalSince1970: 1_733_011_200)
    let text = EnvironmentDiagnosticPresentation.timezone("America/Los_Angeles", at: date)
    #expect(text.contains("美国"))
    #expect(text.contains("UTC−08:00"))
    #expect(!EnvironmentDiagnosticPresentation.timezone("Etc/GMT-8", at: date).contains("中国"))
  }
  @Test("公网 ICE 地址与网页出口对照，缺网页出口不报一致")
  func rtcComparison() {
    for known in [true, false] {
      let report = EnvironmentDiagnosticReport(
        .with {
          $0.browser = .with {
            $0.complete = true
            $0.ipv4 = known ? "8.8.8.8" : ""
            $0.rtcAddresses = ["8.8.8.8", "192.168.1.2"]
            $0.rtcState = "complete"
          }
        })
      let row = EnvironmentDiagnosticPresentation.rows(
        report.browser, browser: true, waiting: false
      ).last
      #expect(row?.value.contains("1 个公网地址") == true)
      #expect(row?.tone == (known ? .matching : .information))
    }
  }
  @Test("WebRTC 不跨地址族判断出口不同")
  func rtcFamily() {
    let report = EnvironmentDiagnosticReport(
      .with {
        $0.browser = .with {
          $0.complete = true
          $0.ipv6 = "2606:4700:4700::1111"
          $0.rtcAddresses = ["8.8.8.8"]
          $0.rtcState = "complete"
        }
      })
    let row = EnvironmentDiagnosticPresentation.rows(report.browser, browser: true, waiting: false)
      .last
    #expect(row?.tone == .information)
    #expect(row?.value.contains("未取得同地址族") == true)
  }
  @Test("环境差异只对照双方已取得的项目，导出冻结并脱敏")
  func differencesAndExport() async {
    let raw = Pax_V1_EnvironmentDiagnosticReport.with {
      $0.sessionID = "report-fixture"
      $0.browserURL = "http://127.0.0.1:1234/fixture/"
      $0.at = .init(date: Date(timeIntervalSince1970: 1_733_011_200))
      $0.complete = true
      $0.mac = .with {
        $0.complete = true
        $0.ipv4 = "8.8.8.8"
      }
      $0.browser = .with {
        $0.complete = true
        $0.ipv4 = "8.8.8.8"
        $0.browser = "Chrome"
        $0.languages = ["en-US"]
        $0.timezone = "America/Los_Angeles"
        $0.rtcState = "empty"
      }
    }
    let report = EnvironmentDiagnosticReport(raw)
    let local = NetworkDiagnosticEnvironment(
      timezone: "Asia/Shanghai", languages: ["zh-Hans"], region: "zh_CN", at: raw.at.date)
    #expect(
      EnvironmentDiagnosticPresentation.differences(report, local: local) == Set(["语言", "时区"]))
    let store = EnvironmentDiagnosticStore(request: { _, _ in report }, openBrowser: { _ in true })
    store.start()
    for _ in 0..<100 where store.report == nil { await Task.yield() }
    let first = EnvironmentDiagnosticPresentation.markdown(store)
    #expect(first.contains("### 环境结果"))
    #expect(first.contains("Mac：IPv4 [已脱敏]"))
    #expect(!first.contains("8.8.8.8"))
    #expect(!first.contains("127.0.0.1"))
    #expect(!first.contains("report-fixture"))
    #expect(first == EnvironmentDiagnosticPresentation.markdown(store))
    store.cancel()
  }
  @Test("取消后迟到样本不进入新一轮")
  func cancelLateSample() async {
    actor Gate {
      var started = false
      func request(_ action: String, _ session: String) async throws -> EnvironmentDiagnosticReport
      {
        if action == "start" {
          started = true
          try? await Task.sleep(for: .milliseconds(80))
        }
        return EnvironmentDiagnosticReport(
          .with {
            $0.sessionID = "fixture"
            $0.browserURL = "http://127.0.0.1:1234/token/"
          })
      }
    }
    let gate = Gate()
    let store = EnvironmentDiagnosticStore(
      request: { try await gate.request($0, $1) },
      openBrowser: { _ in
        Issue.record("cancelled start must not open browser")
        return true
      })
    store.start()
    for _ in 0..<100 where await gate.started == false { await Task.yield() }
    store.cancel()
    try? await Task.sleep(for: .milliseconds(100))
    #expect(!store.busy)
    #expect(store.report == nil)
  }
}
