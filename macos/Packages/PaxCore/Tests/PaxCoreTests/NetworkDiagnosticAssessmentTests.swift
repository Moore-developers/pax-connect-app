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
    #expect(rows[0].value.contains("同出口"))
    #expect(rows[3].value.contains("不等于泄露"))
    #expect(rows[3].detail.contains("1.1.1.1"))
    #expect(rows[3].detail.contains("9.9.9.9"))
    #expect(rows[3].detail.contains("149.112.112.112"))
    #expect(rows[4].value.contains("不能据此确认防泄露"))
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
    #expect(!value.text.contains("存在按目标分流"))
  }
}
