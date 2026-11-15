import Foundation
import PaxProto
import Testing

@testable import PaxClient
@testable import PaxCore

@Suite("DNS实际请求路径") struct EnvironmentDNSPathTests {
  typealias P = EnvironmentDiagnosticPresentation

  private func report(mac: String, browser: String, count: Int = 12) -> EnvironmentDiagnosticReport
  {
    func sample(_ state: String) -> Pax_V1_EnvironmentSample {
      .with {
        $0.complete = true
        $0.checks = [
          .with {
            $0.kind = "dns_path"
            $0.target = "dns"
            $0.state = state
            $0.value = "{\"expected\":12,\"observed\":\(count),\"unverified\":0}"
          },
          .with {
            $0.kind = "dns_upstream"
            $0.target = "dns"
            $0.state = state
            $0.value = #"{"server":"1.1.1.1","outbound":"proxy","pool":"overseas"}"#
          },
        ]
      }
    }
    return EnvironmentDiagnosticReport(
      .with {
        $0.mac = sample(mac)
        $0.browser = sample(browser)
      })
  }

  @Test("两侧都完整才确认，单侧完整与部分覆盖仍保留未知")
  func coverage() {
    let full = report(mac: "proxy", browser: "proxy")
    #expect(P.dnsPathAssessment(full, waiting: false).tone == .matching)
    #expect(P.dnsPathAssessment(full, waiting: false).text.contains("本次 DNS 请求已确认经代理"))
    #expect(
      P.dnsPathAssessment(report(mac: "proxy", browser: "proxy", count: 11), waiting: false).tone
        == .pending)
    let partial = P.dnsPathAssessment(report(mac: "proxy", browser: "unobserved"), waiting: false)
    #expect(partial.text == "部分已确认经代理 · 浏览器请求路径未确认")
    let old = EnvironmentDiagnosticReport(
      .with {
        $0.mac.complete = true
        $0.browser.complete = true
      })
    #expect(P.dnsPathAssessment(old, waiting: false).tone == .pending)
    #expect(P.dnsPathAssessment(old, waiting: false).text.contains("请求路径未确认"))
  }

  @Test("按规则直连不判泄露，观察到绕过代理优先于完整度")
  func directions() {
    let direct = P.dnsPathAssessment(
      report(mac: "policy_direct", browser: "policy_direct"), waiting: false)
    #expect(direct.tone == .information && direct.text.contains("按 DNS 分流规则直连"))
    #expect(
      P.dnsPathAssessment(report(mac: "mixed", browser: "proxy"), waiting: false).text.contains(
        "包含代理与按规则直连"))
    let bypass = P.dnsPathAssessment(
      report(mac: "bypass", browser: "unobserved", count: 1), waiting: false)
    #expect(bypass.tone == .difference && bypass.text.contains("发现绕过代理"))
  }

  @Test("合并结果与详情使用同份路径证据，保留覆盖数而不暴露随机域名")
  func assessedAndDetails() {
    let value = report(mac: "proxy", browser: "proxy")
    let rows = P.assessedRows(value, local: nil, waiting: false)
    let dns = rows.first { $0.title == "DNS" }
    #expect(dns?.value == P.dnsPathAssessment(value, waiting: false).text)
    #expect(dns?.detail.contains("已关联 12/12 个测试域名") == true)
    #expect(dns?.detail.contains("海外加密 DNS · 1.1.1.1 · 出站 proxy") == true)
    #expect(dns?.detail.contains("未关联到的请求仍保留未知") == true)
  }
  @Test("远端解析有连接响应时明确指出交给代理，不能冒充本机上游服务器")
  func remoteProxyDetails() {
    let value = EnvironmentDiagnosticReport(
      .with {
        $0.browser = .with {
          $0.checks = [
            .with {
              $0.kind = "dns_path"
              $0.target = "dns"
              $0.state = "proxy"
              $0.value = #"{"expected":12,"observed":12,"unverified":0}"#
            },
            .with {
              $0.kind = "dns_upstream"
              $0.target = "dns"
              $0.state = "remote_proxy"
              $0.value =
                #"{"server":"","outbound":"proxy","pool":"remote","route":"Fixture Route"}"#
            },
          ]
        }
      })
    let detail = P.dnsPathDetails(value.browser, name: "浏览器")
    #expect(detail.contains("域名已交由「Fixture Route」解析"))
    #expect(detail.contains("具体解析器由代理端选择"))
    #expect(!detail.contains("海外加密 DNS ·"))
  }

}
