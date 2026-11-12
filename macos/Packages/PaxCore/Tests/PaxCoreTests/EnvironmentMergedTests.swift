import Foundation
import PaxProto
import Testing

@testable import PaxClient
@testable import PaxCore

@Suite("合并环境结果") struct EnvironmentMergedTests {
  private let local = NetworkDiagnosticEnvironment(
    timezone: "America/Los_Angeles", languages: ["en-US"], region: "en_US",
    at: Date(timeIntervalSince1970: 1_733_011_200))

  @Test("合并结果直接保留地址族、语言原值、时区和已有失败原因")
  func actualReadings() {
    let report = EnvironmentDiagnosticReport(
      .with {
        $0.mac = .with {
          $0.complete = true
          $0.ipv4 = "8.8.8.8"
          $0.ipv6 = "2606:4700:4700::1111"
          $0.dnsError = "sampling_timeout"
        }
        $0.browser = .with {
          $0.complete = true
          $0.browser = "Chrome"
          $0.ipError = "sampling_timeout"
          $0.dnsError = "dns_unavailable"
          $0.rtcState = "empty"
          $0.languages = ["zh-CN", "en-US"]
          $0.timezone = "Asia/Shanghai"
        }
      })
    let rows = EnvironmentDiagnosticPresentation.mergedRows(report, local: local, waiting: false)
    #expect(rows[0].value.contains("2606:4700:4700::1111"))
    #expect(rows[0].value.contains("45 秒"))
    #expect(rows[1].value.contains("zh-CN → en-US"))
    #expect(rows[2].value.contains("America/Los_Angeles"))
    #expect(rows[2].value.contains("Asia/Shanghai"))
    #expect(rows[3].value.contains("45 秒"))
    #expect(rows[3].value.contains("DNS 服务未返回有效解析器"))
    #expect(rows[4].value.contains("0 个公网地址"))
  }

  @Test("浏览器和 Mac 相同或不同时，摘要保留双方来源")
  func comparison() {
    let raw = Pax_V1_EnvironmentDiagnosticReport.with {
      $0.mac = .with {
        $0.complete = true
        $0.ipv4 = "8.8.8.8"
        $0.resolvers = [
          .with {
            $0.ip = "1.1.1.1"
            $0.country = "US"
          }
        ]
      }
      $0.browser = .with {
        $0.complete = true
        $0.browser = "Chrome"
        $0.ipv4 = "8.8.8.8"
        $0.languages = ["zh-CN"]
        $0.region = "zh_CN"
        $0.timezone = "America/Los_Angeles"
        $0.rtcState = "empty"
        $0.resolvers = [
          .with {
            $0.ip = "9.9.9.9"
            $0.country = "US"
          }
        ]
      }
    }
    let rows = EnvironmentDiagnosticPresentation.mergedRows(
      EnvironmentDiagnosticReport(raw), local: local, waiting: false)
    #expect(rows.count == 5)
    #expect(
      rows[0].value
        == "Mac：IPv4 8.8.8.8 · IPv6 本次未取得（旧记录未采集原因）；Chrome：IPv4 8.8.8.8 · IPv6 本次未取得（旧记录未采集原因）")
    #expect(rows[1].value.contains("Mac：en-US"))
    #expect(rows[1].value.contains("Chrome：zh-CN"))
    #expect(rows[2].value.contains("Mac：America/Los_Angeles"))
    #expect(rows[2].value.contains("Chrome：America/Los_Angeles"))
    #expect(rows[3].value.contains("1.1.1.1"))
    #expect(rows[3].value.contains("9.9.9.9"))
    #expect(rows[4].value.contains("0 个公网地址"))
  }
  @Test("浏览器未返回时不伪称双方相同，也不借用不同出口的情报")
  func partialAndIdentity() {
    let partial = EnvironmentDiagnosticReport(
      .with {
        $0.mac = .with {
          $0.complete = true
          $0.ipv4 = "8.8.8.8"
          $0.intelligence = .with { $0.country = "US" }
        }
        $0.browser = .with {
          $0.complete = true
          $0.rtcState = "failed"
        }
      })
    let rows = EnvironmentDiagnosticPresentation.mergedRows(partial, local: local, waiting: false)
    #expect(rows[0].value.contains("浏览器：公网 IP 请求未取得有效地址"))
    #expect(rows[0].tone == .failure)
    #expect(EnvironmentDiagnosticPresentation.overview(partial).source == "Mac 出口")
    let different = EnvironmentDiagnosticReport(
      .with {
        $0.mac = .with {
          $0.ipv4 = "8.8.8.8"
          $0.intelligence = .with { $0.country = "US" }
        }
        $0.browser = .with { $0.ipv4 = "1.1.1.1" }
      })
    let overview = EnvironmentDiagnosticPresentation.overview(different)
    #expect(overview.address == "1.1.1.1")
    #expect(overview.intelligence == nil)
    #expect(overview.comparison == "Mac：8.8.8.8")
  }
  @Test("IPv4 和 IPv6 分开对照")
  func addressFamilies() {
    let report = EnvironmentDiagnosticReport(
      .with {
        $0.mac = .with {
          $0.complete = true
          $0.ipv4 = "8.8.8.8"
          $0.ipv6 = "2001:db8::1"
        }
        $0.browser = .with {
          $0.complete = true
          $0.browser = "Chrome"
          $0.ipv4 = "8.8.8.8"
          $0.ipv6 = "2001:db8::2"
        }
      })
    let row = EnvironmentDiagnosticPresentation.mergedRows(report, local: local, waiting: false)[0]
    #expect(row.value.contains("IPv6 2001:db8::2"))
    #expect(row.value.contains("2001:db8::1"))
    #expect(row.tone == .difference)
    let split = EnvironmentDiagnosticReport(
      .with {
        $0.mac = .with {
          $0.complete = true
          $0.ipv6 = "2001:db8::1"
        }
        $0.browser = .with {
          $0.complete = true
          $0.ipv4 = "8.8.8.8"
        }
      })
    let other = EnvironmentDiagnosticPresentation.overview(split)
    #expect(other.comparison.contains("同地址族出口未取得"))
    #expect(
      EnvironmentDiagnosticPresentation.mergedRows(split, local: local, waiting: false)[0].value
        .contains("IPv4 本次未取得"))
  }
  @Test("相同出口可共用已取得的供应商资料")
  func sameIdentityFallback() {
    let report = EnvironmentDiagnosticReport(
      .with {
        $0.mac = .with {
          $0.ipv4 = "8.8.8.8"
          $0.intelligence = .with {
            $0.country = "US"
            $0.score = 55
          }
        }
        $0.browser = .with { $0.ipv4 = "8.8.8.8" }
      })
    let value = EnvironmentDiagnosticPresentation.overview(report)
    #expect(value.source == "浏览器出口")
    #expect(value.intelligence?.score == 55)
    #expect(value.comparison == "Mac 与浏览器 IPv4 出口相同")
  }

  @Test("浏览器资料超时仍可使用同 IP 的 Mac 资料，并保留失败原因")
  func failedIntelligenceFallback() {
    let report = EnvironmentDiagnosticReport(
      .with {
        $0.mac = .with {
          $0.ipv4 = "8.8.8.8"
          $0.intelligence = .with { $0.score = 55 }
        }
        $0.browser = .with {
          $0.ipv4 = "8.8.8.8"
          $0.intelligence = .with { $0.providerError = "target_connection_timeout" }
        }
      })
    let overview = EnvironmentDiagnosticPresentation.overview(report)
    #expect(overview.intelligence?.score == 55)
    #expect(overview.comparison.contains("超时"))
    #expect(overview.comparison.contains("采用 Mac 同 IP 资料"))
  }
}
