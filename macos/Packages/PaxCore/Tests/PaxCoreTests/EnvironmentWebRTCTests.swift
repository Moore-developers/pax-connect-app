import Foundation
import PaxProto
import Testing

@testable import PaxClient
@testable import PaxCore

@MainActor @Suite("WebRTC 统一判断") struct EnvironmentWebRTCTests {
  typealias P = EnvironmentDiagnosticPresentation

  func check(
    _ kind: String, target: String = "", state: String, value: String = "", error: String = ""
  ) -> Pax_V1_EnvironmentCheck {
    .with {
      $0.kind = kind
      $0.target = target
      $0.state = state
      $0.value = value
      $0.errorCode = error
      $0.at = .init(date: Date(timeIntervalSince1970: 1_733_011_200))
    }
  }
  func route(_ state: String, _ detail: String = "节点A\t默认\t\tChrome") -> Pax_V1_EnvironmentCheck {
    check("route_stun", target: "74.125.1.1:19302", state: state, value: detail)
  }
  func physical(_ value: String) -> Pax_V1_EnvironmentCheck {
    check("egress_physical", target: "physical", state: "observed", value: value)
  }
  func report(
    addresses: [String] = [], state: String = "complete", v4: String = "1.1.1.1",
    v6: String = "", checks: [Pax_V1_EnvironmentCheck] = [], versioned: Bool = true
  ) -> EnvironmentDiagnosticReport {
    EnvironmentDiagnosticReport(
      .with {
        $0.browser = .with {
          $0.complete = true
          $0.ipv4 = v4
          $0.ipv6 = v6
          $0.rtcAddresses = addresses
          $0.rtcState = state
          $0.checks = checks
        }
      })
  }
  func verdict(_ report: EnvironmentDiagnosticReport) -> P.WebRTCVerdict {
    P.webRTCVerdict(report.browser, pending: false)
  }

  @Test("规则 1：不支持")
  func unsupported() {
    let v = verdict(report(state: "unsupported"))
    #expect(v.conclusion == "泄露风险待确认 · 浏览器不支持 WebRTC，无法检测")
    #expect(v.tone == .pending)
  }
  @Test("规则 2：暴露真实 IP、未经代理、同出口、都不同、缺对照")
  func publicCandidates() {
    let exposed = verdict(
      report(addresses: ["9.9.9.9"], checks: [physical("v4=9.9.9.9;v6=")]))
    #expect(exposed.conclusion == "已发现公网地址泄露 · WebRTC 暴露本机真实 IP")
    #expect(exposed.tone == .difference)
    let direct = verdict(
      report(addresses: ["1.1.1.1"], checks: [physical("v4=1.1.1.1;v6=")]))
    #expect(direct.conclusion == "当前未经代理，WebRTC 与网页同为本机出口")
    #expect(direct.tone == .information)
    let same = verdict(
      report(addresses: ["1.1.1.1"], checks: [physical("v4=9.9.9.9;v6=")]))
    #expect(same.conclusion == "本次未发现公网地址泄露 · 与网页出口一致")
    #expect(same.tone == .matching)
    let both = verdict(
      report(addresses: ["2.2.2.2"], checks: [physical("v4=9.9.9.9;v6=")]))
    #expect(both.conclusion == "存在潜在风险 · 与网页、物理出口都不同，需核对")
    #expect(both.tone == .difference)
    let missing = verdict(
      report(addresses: ["2606:4700::1"], checks: [physical("v4=9.9.9.9;v6=")]))
    #expect(missing.conclusion.contains("缺少同地址族网页出口"))
    #expect(missing.tone == .pending)
  }
  @Test("采集中：无线路证据时不下比较结论")
  func pendingCollecting() {
    let r = report(addresses: ["9.9.9.9"])
    let v = P.webRTCVerdict(r.browser, pending: true)
    #expect(v.conclusion == "已取得 1 个公网候选，正在核对出口")
    #expect(v.tone == .pending)
    #expect(v.route == "正在采集线路证据")
    let done = P.webRTCVerdict(r.browser, pending: false)
    #expect(done.route == "旧记录未采集线路证据")
  }
  @Test("规则 3、4、5：阻断、经代理无 UDP、其余")
  func noPublicCandidates() {
    let blocked = verdict(report(state: "empty", checks: [route("blocked", "")]))
    #expect(blocked.conclusion == "泄露风险待确认 · 探测被规则阻断，未取得公网地址")
    #expect(blocked.tone == .pending)
    let timeout = check(
      "rtc_stun", target: "stun", state: "timeout", error: "rtc_gathering_timeout")
    let proxied = verdict(report(state: "complete", checks: [timeout, route("proxy")]))
    #expect(proxied.conclusion == "泄露风险待确认 · 探测经代理但未返回地址（代理可能不支持 UDP）")
    #expect(proxied.tone == .pending)
    #expect(proxied.summary == "STUN 超时 · 泄露待确认")
    let failed = verdict(report(state: "failed", checks: [timeout, route("direct", "")]))
    #expect(failed.tone == .failure)
    let rest = verdict(report(addresses: ["192.168.1.2"], state: "complete"))
    #expect(rest.conclusion == "泄露风险待确认 · 仅取得局域网 / mDNS 候选，未取得公网地址")
    #expect(rest.tone == .pending)
  }
  @Test("线路证据单独成句")
  func routeSentence() {
    let restricted = verdict(
      report(state: "empty", checks: [route("proxy", "节点A\tWebRTC出口限制\t\tChrome")]))
    #expect(restricted.route == "STUN 经 节点A（WebRTC 出口限制生效）")
    #expect(verdict(report(checks: [route("proxy")])).route == "STUN 经 节点A")
    #expect(verdict(report(checks: [route("direct", "")])).route == "STUN 直连")
    #expect(verdict(report(checks: [route("blocked", "")])).route == "STUN 被阻断")
    #expect(verdict(report(checks: [route("unobserved", "")])).route == "未观察到 STUN 连接")
    #expect(
      verdict(report(checks: [route("unavailable", "")])).route == "Pax 未接管，无线路证据")
    #expect(verdict(report()).route == "旧记录未采集线路证据")
  }
  @Test("Assessment 与 Presentation 对同一输入给出相同结论与 tone")
  func sameVerdict() {
    let value = report(
      addresses: ["9.9.9.9"], checks: [physical("v4=9.9.9.9;v6="), route("proxy")])
    let assessed = P.assessedRows(value, local: nil, waiting: false).first { $0.title == "WebRTC" }
    let plain = P.rows(value.browser, browser: true, waiting: false).last
    #expect(assessed?.value == plain?.value)
    #expect(assessed?.tone == plain?.tone)
    #expect(assessed?.value == verdict(value).summary)
    #expect(assessed?.detail.contains("9.9.9.9") == true)
  }
  @Test("旧记录可解码并显示未采集线路证据；rtc 过滤不含新 kind")
  func legacy() throws {
    let value = report(addresses: ["1.1.1.1"], checks: [check("rtc_stun", state: "observed")])
    let decoded = try JSONDecoder().decode(
      EnvironmentDiagnosticReport.self, from: JSONEncoder().encode(value))
    let row = P.assessedRows(decoded, local: nil, waiting: false).first { $0.title == "WebRTC" }
    #expect(row?.detail.contains("旧记录未采集线路证据") == true)
    let new = report(checks: [route("proxy"), physical("v4=9.9.9.9;v6=")])
    #expect(new.browser.checks?.filter { $0.kind.hasPrefix("rtc_") }.isEmpty == true)
  }
}
