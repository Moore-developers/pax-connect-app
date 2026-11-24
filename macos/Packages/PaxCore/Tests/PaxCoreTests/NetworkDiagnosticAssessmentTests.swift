import Foundation
import PaxProto
import Testing

@testable import PaxClient
@testable import PaxCore

@Suite("线路诊断摘要与证据判断") struct NetworkDiagnosticAssessmentTests {
  @Test("服务HTTP判断不冒充账号可用，认证拒绝与限流分别解释")
  func services() {
    for (code, expected) in [
      (200, "权限未验"), (401, "需认证"), (403, "拒绝"), (429, "限流"), (503, "服务器报错"),
    ] {
      let probe = NetworkDiagnosticReport(
        .with { $0.probes = [.with { $0.httpStatus = UInt32(code) }] }
      ).probes[0]
      let value = NetworkDiagnosticPresentation.serviceAssessment(probe, api: true)
      #expect(value.text.contains(expected))
      #expect(!value.text.contains("已解锁"))
    }
  }
  @Test("字段缺失不算分歧，ASN前缀不造成假冲突")
  func sources() {
    let report = NetworkDiagnosticReport(
      .with {
        $0.sources = [
          .with {
            $0.country = "US"
            $0.city = "Los Angeles"
            $0.asn = "AS36352"
          },
          .with {
            $0.country = "US"
            $0.asn = "36352"
          },
        ]
      })
    let overview = NetworkDiagnosticPresentation.sourceOverview(report.sources)
    #expect(overview.text.contains("未见分歧"))
    #expect(overview.text.contains("1 源字段不全"))
    let row = NetworkDiagnosticPresentation.sourceAssessment(report.sources[1], all: report.sources)
    #expect(row.text.contains("缺城市"))
    #expect(!row.text.contains("有分歧"))
  }
  @Test("DNS集合不同不判泄露，零候选不判安全，完整读值留详情")
  func environment() {
    let report = EnvironmentDiagnosticReport(
      .with {
        $0.mac = .with {
          $0.complete = true
          $0.ipv4 = "8.8.8.8"
          $0.resolvers = [.with { $0.ip = "1.1.1.1" }]
        }
        $0.browser = .with {
          $0.complete = true
          $0.ipv4 = "8.8.8.8"
          $0.browser = "Chrome"
          $0.rtcState = "empty"
          $0.resolvers = [
            .with { $0.ip = "9.9.9.9" }, .with { $0.ip = "8.8.4.4" },
            .with { $0.ip = "149.112.112.112" },
          ]
        }
      })
    let rows = EnvironmentDiagnosticPresentation.assessedRows(report, local: nil, waiting: false)
    #expect(rows[0].value.contains("未发现出口差异"))
    #expect(rows[3].value.contains("请求路径未确认"))
    #expect(rows[3].tone == .pending)
    #expect(rows[3].value.contains("尚无完整的本轮 DNS 传输证据"))
    #expect(rows[3].detail.contains("1.1.1.1"))
    #expect(rows[3].detail.contains("9.9.9.9"))
    #expect(rows[3].detail.contains("149.112.112.112"))
    #expect(rows[4].value == "无公网候选 · 泄露待确认")
    #expect(rows[4].detail.contains("不能据此确认防泄露"))
  }
  @Test("相同DNS出口仍待确认；IP和偏好只报告差异，不宣称安全", arguments: [true, false])
  func evidenceBoundaries(matching: Bool) {
    let at = Date(timeIntervalSince1970: 1_733_011_200)
    let local = NetworkDiagnosticEnvironment(
      timezone: "America/Los_Angeles", languages: ["en-US", "en-GB"], region: "en_US", at: at)
    let report = EnvironmentDiagnosticReport(
      .with {
        $0.at = .init(date: at)
        $0.mac = .with {
          $0.complete = true
          $0.ipv4 = "8.8.8.8"
          $0.resolvers = [.with { $0.ip = "1.1.1.1" }]
        }
        $0.browser = .with {
          $0.complete = true
          $0.ipv4 = matching ? "8.8.8.8" : "9.9.9.9"
          $0.languages = matching ? ["en-US", "en-GB"] : ["en-GB", "en-US"]
          $0.timezone = matching ? "America/Los_Angeles" : "America/Vancouver"
          $0.resolvers = [.with { $0.ip = "1.1.1.1" }]
        }
      })
    let rows = EnvironmentDiagnosticPresentation.assessedRows(report, local: local, waiting: false)
    for (index, name) in [(0, "出口"), (1, "语言"), (2, "时区")] {
      #expect(rows[index].value.hasPrefix((matching ? "未发现" : "发现") + name + "差异"))
      #expect(!rows[index].value.contains("安全"))
    }
    #expect(rows[1].detail.contains("en-US"))
    #expect(rows[2].detail.contains("America/Los_Angeles"))
    if matching {
      #expect(rows[1].value.contains("英语"))
      #expect(rows[2].value.contains("UTC−08:00"))
    } else {
      #expect(rows[2].value.contains("名称不同，本次 UTC 偏移相同"))
    }
    #expect(rows[3].value == "请求路径未确认 · 尚无完整的本轮 DNS 传输证据")
    #expect(rows[3].tone == .pending)
    #expect(rows[3].detail.contains("Mac 1 个解析器，浏览器 1 个解析器"))
  }

  @Test("不同地址族的两个回显不判分流")
  func exitFamilies() {
    let report = EnvironmentDiagnosticReport(
      .with {
        $0.browser = .with {
          $0.checks = [
            .with {
              $0.kind = "echo_ipip"
              $0.state = "observed"
              $0.value = "8.8.8.8"
            },
            .with {
              $0.kind = "echo_ipinfo"
              $0.state = "observed"
              $0.value = "2606:4700:4700::1111"
            },
          ]
        }
      })
    let value = EnvironmentDiagnosticPresentation.exitAssessment(report.browser.checks ?? [])
    #expect(value.text.contains("地址族不同"))
    #expect(value.tone == .information)
    #expect(!value.text.contains("存在按目标分流"))
  }

  @Test("同地址族的出口 IP 不同时，保留各家地区并报告异常")
  @MainActor
  func differentBrowserExits() throws {
    let ipip = "14.19.29.200 · 中国 广东 广州"
    let ipinfo = "204.152.192.235 · US, California, Los Angeles"
    let sample = EnvironmentDiagnosticReport.Sample(
      .with {
        $0.checks = [("echo_ipip", ipip), ("echo_ipinfo", ipinfo)].map { kind, value in
          .with {
            $0.kind = kind
            $0.state = "observed"
            $0.value = value
          }
        }
      })
    let checks = try #require(sample.checks)
    let assessment = EnvironmentDiagnosticPresentation.exitAssessment(checks)
    #expect(assessment.tone == .difference)
    #expect(assessment.text.contains("出口异常"))
    #expect(EnvironmentDiagnosticPresentation.exitReading(checks[0]) == ipip)
    #expect(
      EnvironmentDiagnosticPresentation.exitReading(checks[1])
        == "204.152.192.235 · 美国 · California · Los Angeles")
    #expect(
      EnvironmentDiagnosticStore.sampleIssues(sample, ipv6Off: false, context: .chinese)
        == [assessment.text])
  }

  @Test("相同 IP 的旧回显和新地区记录仍判同一出口")
  func sameBrowserExitWithLegacyRecord() throws {
    let sample = EnvironmentDiagnosticReport.Sample(
      .with {
        $0.checks = [
          .with {
            $0.kind = "echo_ipip"
            $0.state = "observed"
            $0.value = "8.8.8.8"
          },
          .with {
            $0.kind = "echo_ipinfo"
            $0.state = "observed"
            $0.value = "8.8.8.8 · US, California"
          },
        ]
      })
    let checks = try #require(sample.checks)
    #expect(EnvironmentDiagnosticPresentation.exitAssessment(checks).text.contains("同出口"))
    #expect(EnvironmentDiagnosticPresentation.exitReading(checks[0]) == "8.8.8.8 · 地区未取得")
    #expect(EnvironmentDiagnosticPresentation.exitAssessment([checks[0]]).tone == .pending)
  }
}
