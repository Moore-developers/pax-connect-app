import Foundation
import PaxProto
import Testing

@testable import PaxClient
@testable import PaxCore

@MainActor @Suite("多源对照完整列表") struct NetworkDiagnosticSourcesTests {
  typealias P = NetworkDiagnosticPresentation

  private func report(failed: Bool, omit: String? = nil) -> NetworkDiagnosticReport {
    NetworkDiagnosticReport(
      .with {
        $0.sources = ["ipquery", "ipwho.is"].map { name in
          .with {
            $0.name = name
            $0.providerID = name
            $0.country = "US"
            $0.city = "Los Angeles"
            $0.asn = "AS36352"
          }
        }
        if !failed {
          let forwarded = [
            "Cloudflare 边缘", "MaxMind GeoLite", "IPinfo", "IP.SB", "ipapi.is",
            "proxycheck.io", "IPQuery", "IP-API", "IPLocate", "ip2region",
          ]
          $0.sources += forwarded.filter { $0 != omit }.map { name in
            .with {
              $0.name = name
              $0.providerID = "ipx.al"
              $0.country = "US"
              $0.city = "Los Angeles"
              $0.asn = "36352"
            }
          }
        }
        $0.providers = [
          .with {
            $0.id = "ipx.al"
            $0.state = failed ? "failed" : "observed"
            $0.errorCode = failed ? "provider_timeout" : ""
          }
        ]
      })
  }

  @Test("补充失败保留12行及超时原因，不增加返回数；详情与报告同样可解释")
  func supplementTimeout() {
    let value = report(failed: true)
    let rows = P.sourceRows(value)
    #expect(rows.count == 12)
    #expect(rows.filter(\.unavailable).count == 10)
    #expect(rows.first { $0.title == "IPLocate · 补充" }?.value.contains("资料请求超时") == true)
    #expect(rows.suffix(10).allSatisfy { $0.tone == .pending && $0.detail.contains("不参与来源对照") })
    #expect(P.sourceListOverview(value).text.contains("2 条来源资料"))
    #expect(P.sourceListOverview(value).text.contains("10 项未返回资料"))
    #expect(value.sources.count == 2)
    #expect(P.sourceRows(value, onlyDifferences: true).count == 10)
  }

  @Test("成功保持真实12行且无重复，单家未返回不会伪称整个补充失败")
  func successfulAndMissing() {
    let complete = P.sourceRows(report(failed: false))
    #expect(complete.count == 12)
    #expect(!complete.contains { $0.unavailable })
    #expect(Set(complete.map(\.title)).count == 12)
    #expect(P.sourceRows(report(failed: false), onlyDifferences: true).isEmpty)
    let partial = P.sourceRows(report(failed: false, omit: "IPLocate"))
    #expect(partial.count == 12)
    #expect(partial.filter(\.unavailable).count == 1)
    #expect(partial.first { $0.title == "IPLocate · 补充" }?.value == "未返回本来源定位资料")
  }

  @Test("未检测与旧记录明确缺失，不补写历史；额外真实来源保留")
  func oldAndAdditional() throws {
    #expect(P.sourceRows(nil).count == 12)
    #expect(P.sourceRows(nil).allSatisfy { $0.value == "尚未检测" })
    let old = NetworkDiagnosticReport(
      .with {
        $0.sources = [
          .with {
            $0.name = "MaxMind GeoLite"
            $0.country = "US"
          },
          .with {
            $0.name = "额外库"
            $0.country = "JP"
            $0.providerID = "extra"
          },
        ]
      })
    var json = try #require(
      JSONSerialization.jsonObject(with: JSONEncoder().encode(old)) as? [String: Any])
    json.removeValue(forKey: "providers")
    let legacy = try JSONDecoder().decode(
      NetworkDiagnosticReport.self, from: JSONSerialization.data(withJSONObject: json))
    let rows = P.sourceRows(legacy)
    #expect(rows.count == 13)
    #expect(rows.first { $0.title == "MaxMind GeoLite" }?.unavailable == false)
    #expect(rows.last?.title == "额外库")
    #expect(rows.first?.value == "旧记录未包含本来源资料")
    #expect(legacy.sources.count == 2)
  }
}
