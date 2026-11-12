import Foundation
import PaxProto
import Testing

@testable import PaxClient
@testable import PaxCore

@MainActor @Suite("独立情报与备用证据") struct NetworkDiagnosticSourceTests {
  private func report() -> NetworkDiagnosticReport {
    NetworkDiagnosticReport(
      .with {
        $0.at = .init(date: Date(timeIntervalSince1970: 1_733_011_200))
        $0.sources = [
          .with {
            $0.name = "forwarded-db"
            $0.country = "US"
            $0.providerID = "ipx.al"
          }
        ]
        $0.providers = [
          .with {
            $0.id = "ipquery"
            $0.queryIp = "203.0.113.24"
            $0.family = "ipv4"
            $0.state = "failed"
            $0.errorCode = "provider_http_429"
            $0.httpStatus = 429
          },
          .with {
            $0.id = "ipx.al"
            $0.queryIp = "203.0.113.24"
            $0.family = "ipv4"
            $0.state = "observed"
            $0.fallbackFor = ["risk.vpn"]
            $0.fallbackReason = "independent_sources_unavailable"
            $0.at = .init(date: Date(timeIntervalSince1970: 1_733_011_200))
          },
        ]
        $0.facts = [
          .with {
            $0.group = "risk"
            $0.label = "VPN"
            $0.state = "hit"
            $0.providerID = "ipx.al"
          },
          .with {
            $0.group = "risk"
            $0.label = "VPN"
            $0.state = "hit"
            $0.providerID = "other"
          },
          .with {
            $0.group = "risk"
            $0.label = "自签证书"
            $0.state = "unknown"
          },
        ]
      })
  }

  @Test("来源映射保留逐家错误与备用原因，风险计数按项目去重")
  func provenanceAndCounts() throws {
    let value = report()
    #expect(value.providers?.first?.httpStatus == 429)
    #expect(value.providers?.last?.fallbackFor == ["risk.vpn"])
    let detail = NetworkDiagnosticPresentation.providerDetails(value, id: "ipx.al")
    #expect(detail.contains("补充 · 经 ipx.al"))
    #expect(detail.contains("仅补缺：VPN"))
    #expect(NetworkDiagnosticPresentation.sourceOverview(value.sources).text.contains("补充转交"))
    #expect(NetworkDiagnosticPresentation.riskCounts(value).hits == 1)
    #expect(NetworkDiagnosticPresentation.riskCounts(value).unknown == 1)
    #expect(
      try JSONDecoder().decode(NetworkDiagnosticReport.self, from: JSONEncoder().encode(value))
        == value)
  }

  @Test("直接来源零风险原值保留在详情，但不解释成安全概率")
  func directRiskNumber() {
    let direct = NetworkDiagnosticReport(
      .with {
        $0.providers = [
          .with {
            $0.id = "ipquery"
            $0.state = "observed"
          }
        ]
        $0.facts = [
          .with {
            $0.group = "risk"
            $0.label = "VPN"
            $0.state = "clear"
            $0.providerID = "ipquery"
          },
          .with {
            $0.group = "raw_risk"
            $0.label = "IPQuery 风险"
            $0.value = "0"
            $0.providerID = "ipquery"
          },
        ]
      })
    let detail = NetworkDiagnosticPresentation.factDetails(direct.facts.first, report: direct)
    #expect(detail.contains("IPQuery 风险：0"))
    #expect(detail.contains("不是概率或总体安全分"))
  }

  @Test("补充来源原因、DNSBL清单与原生性均归因，不冒充直接查询")
  func supplementaryDetails() {
    let value = NetworkDiagnosticReport(
      .with {
        $0.providers = [
          .with {
            $0.id = "ipx.al"
            $0.state = "observed"
            $0.fallbackFor = ["risk.dnsbl"]
            $0.fallbackReason = "independent_coverage_missing"
          }
        ]
        $0.facts = [
          .with {
            $0.group = "risk"
            $0.label = "邮件 DNSBL"
            $0.state = "clear"
            $0.providerID = "ipx.al"
          },
          .with {
            $0.group = "dnsbl"
            $0.label = "TestList"
            $0.state = "clear"
            $0.providerID = "ipx.al"
          },
          .with {
            $0.group = "registration"
            $0.label = "原生 / 广播"
            $0.value = "native"
            $0.providerID = "ipx.al"
          },
        ]
      })
    let detail = NetworkDiagnosticPresentation.factDetails(value.facts.first, report: value)
    #expect(detail.contains("独立来源未覆盖"))
    #expect(detail.contains("TestList：未命中"))
    #expect(detail.contains("补充 · 经 ipx.al"))
    #expect(NetworkDiagnosticPresentation.nativeAssessment(value).text == "原生 IP")
    #expect(NetworkDiagnosticPresentation.acquisitionSummary(value).contains("ipx.al 补充"))
  }

  @Test("旧JSON缺来源字段仍能读取，不能冒充本轮直接请求")
  func legacyJSON() throws {
    let data = try JSONEncoder().encode(report())
    var object = try #require(JSONSerialization.jsonObject(with: data) as? [String: Any])
    object.removeValue(forKey: "providers")
    for key in ["sources", "facts"] {
      object[key] = (object[key] as? [[String: Any]])?.map { value in
        var item = value
        item.removeValue(forKey: "providerID")
        return item
      }
    }
    let legacy = try JSONDecoder().decode(
      NetworkDiagnosticReport.self, from: JSONSerialization.data(withJSONObject: object))
    #expect(legacy.providers == nil)
    #expect(legacy.sources.first?.providerID == nil)
    #expect(NetworkDiagnosticPresentation.acquisitionSummary(legacy).contains("旧记录"))
  }

  @Test("导出保留备用原因并脱敏提供者查询IP，不把未收录当安全")
  func exportAndAbsence() throws {
    let markdown = NetworkDiagnosticPresentation.markdown(
      report(), services: nil, environment: nil, stale: false)
    #expect(markdown.contains("仅补缺：VPN"))
    #expect(markdown.contains("provider_http_429") == false)
    #expect(!markdown.contains("203.0.113.24"))
    let missing = NetworkDiagnosticReport(
      .with {
        $0.providers = [
          .with {
            $0.id = "internetdb"
            $0.state = "not_indexed"
          }
        ]
      })
    #expect(
      NetworkDiagnosticPresentation.providerState(try #require(missing.providers?.first)).contains(
        "不代表"))
    #expect(NetworkDiagnosticPresentation.acquisitionSummary(missing).contains("未请求 ipx.al"))
  }
}
