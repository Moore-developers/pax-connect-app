import Foundation
import PaxProto
import Testing

@testable import PaxClient
@testable import PaxCore

@MainActor @Suite("IPv6 风险行与完成度") struct EnvironmentIPv6Tests {
  typealias P = EnvironmentDiagnosticPresentation
  let address = "2606:4700:4700::1111"

  func check(_ kind: String, _ state: String, value: String = "", error: String = "")
    -> Pax_V1_EnvironmentCheck
  {
    .with {
      $0.kind = kind
      $0.target = kind
      $0.state = state
      $0.value = value
      $0.errorCode = error
      $0.at = .init(date: Date(timeIntervalSince1970: 1_733_011_200))
    }
  }
  /// `route` nil means the Mac carried no ipv6_route evidence at all.
  func report(route: Pax_V1_EnvironmentCheck?, v6Failed: Bool) -> EnvironmentDiagnosticReport {
    EnvironmentDiagnosticReport(
      .with {
        $0.mac = .with {
          $0.complete = true
          $0.ipv4 = "8.8.8.8"
          $0.ipv6 = v6Failed ? "" : address
          $0.checks =
            (route.map { [$0] } ?? [])
            + [
              v6Failed
                ? check("ipv6", "failed", error: "target_dns_failed")
                : check("ipv6", "observed", value: address)
            ]
        }
        $0.browser = .with {
          $0.complete = true
          $0.browser = "Chrome"
          $0.ipv4 = "8.8.8.8"
          $0.checks =
            v6Failed
            ? [check("ipv6", "failed", error: "browser_request_failed")]
            : [check("ipv6", "observed", value: address)]
        }
      })
  }
  func row(_ report: EnvironmentDiagnosticReport) -> P.Row? {
    P.assessedRows(report, local: nil, waiting: false).first { $0.title == "IPv6" }
  }
  func decodedOld() throws -> EnvironmentDiagnosticReport {
    let data = try JSONEncoder().encode(report(route: nil, v6Failed: true))
    let legacy = String(decoding: data, as: UTF8.self).replacingOccurrences(
      of: "\"checks\"", with: "\"legacy\"")
    return try JSONDecoder().decode(EnvironmentDiagnosticReport.self, from: Data(legacy.utf8))
  }

  @Test("四种 IPv6 行：有路由、无路由、读不到、旧记录")
  func rows() throws {
    let on = row(report(route: check("ipv6_route", "observed", value: "en0"), v6Failed: false))
    #expect(on?.value == "存在潜在风险 · 已开启，尚未确认 IPv6 出口是否经代理")
    #expect(on?.tone == .difference)
    #expect(on?.detail.contains("配置 IPv6") == true)
    #expect(on?.detail.contains("开启不等于已泄露") == true)
    let off = row(
      report(route: check("ipv6_route", "absent", error: "ipv6_no_default_route"), v6Failed: true))
    #expect(off?.value == "本项不适用 · 未开启，无 IPv6 默认路由")
    #expect(off?.tone == .information)
    let unreadable = row(report(route: nil, v6Failed: true))
    #expect(unreadable?.value == "暴露风险待确认 · 无法读取 IPv6 默认路由")
    #expect(unreadable?.tone == .pending)
    #expect(row(try decodedOld())?.value == "旧记录未采集")
  }

  @Test("无路由时 Mac 与浏览器检测不算部分未完成；有路由而请求失败仍算")
  func completeness() async {
    for (route, partial) in [
      (check("ipv6_route", "absent", error: "ipv6_no_default_route"), false),
      (check("ipv6_route", "observed", value: "en0"), true),
    ] {
      let value = report(route: route, v6Failed: true)
      let store = EnvironmentDiagnosticStore(request: { _, _ in value }, openBrowser: { _ in true })
      store.start()
      for _ in 0..<200 where store.progressReport == nil { await Task.yield() }
      for browser in [false, true] {
        #expect((store.activity(browser: browser).state == .partial) == partial)
      }
      store.cancel()
      store.clear()
    }
  }

  @Test("无路由时逐项详情写不适用，不写失败；报告只写状态不写地址")
  func detailsAndReport() async {
    let value = report(
      route: check("ipv6_route", "absent", error: "ipv6_no_default_route"), v6Failed: true)
    let failed = value.browser.checks?.first { $0.kind == "ipv6" }
    #expect(P.checkOutcome(failed!, ipv6Off: true).contains("不适用：本机未开启 IPv6"))
    #expect(!P.checkOutcome(failed!).contains("不适用"))
    let on = report(route: check("ipv6_route", "observed", value: "en0"), v6Failed: false)
    let store = EnvironmentDiagnosticStore(request: { _, _ in on }, openBrowser: { _ in true })
    store.start()
    for _ in 0..<100 where store.report == nil { await Task.yield() }
    let line = P.markdown(store).split(separator: "\n").first { $0.hasPrefix("- IPv6：") }
    #expect(line?.contains("已开启") == true)
    #expect(line?.contains(address) == false)
    #expect(line?.contains("en0") == false)
    let whole = P.markdown(store)
    #expect(!whole.contains("en0"))
    #expect(!whole.contains(address))
  }
}
