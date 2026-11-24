import Foundation
import PaxProto
import Testing

@testable import PaxClient
@testable import PaxCore

@MainActor @Suite("多源对照完整列表") struct NetworkDiagnosticSourcesTests {
  typealias P = NetworkDiagnosticPresentation

  @Test("旧报告的转交记录与重复来源从对照中剔除")
  func historicTransfersStayOutOfComparison() {
    let value = NetworkDiagnosticReport(
      .with {
        $0.sources = ["ipquery", "ipquery", "ipx.al", ""].map { id in
          .with {
            $0.name = id == "ipquery" ? "ipquery" : "IPQuery"
            $0.providerID = id
            $0.country = "US"
            $0.city = "Los Angeles"
            $0.asn = "AS36352"
          }
        }
        $0.providers = ["ipquery", "ipx.al"].map { id in
          .with {
            $0.id = id
            $0.state = "observed"
          }
        }
      })
    #expect(P.sourceRows(nil).isEmpty)
    #expect(P.sourceRows(value).map(\.title) == ["ipquery"])
    #expect(P.sourceListOverview(P.sourceRows(value)).text == "1 个来源")
    #expect(value.sources.count == 4)
  }

  @Test("新增来源保留独立失败，预期缺字段与 BGP 不算地理分歧")
  func comparisonAndFailure() {
    let value = NetworkDiagnosticReport(
      .with {
        $0.sources = [
          .with {
            $0.name = "db-ip"
            $0.providerID = "db-ip"
            $0.country = "US"
            $0.city = "Los Angeles"
          },
          .with {
            $0.name = "ipapi.is"
            $0.providerID = "ipapi.is"
            $0.country = "United States"
            $0.city = "Los Angeles"
            $0.asn = "AS36352"
          },
          .with {
            $0.name = "RIPEstat"
            $0.providerID = "ripestat"
            $0.asn = "AS64500"
          },
        ]
        $0.providers = ["db-ip", "ipapi.is", "ripestat", "proxycheck.io"].map { id in
          .with {
            $0.id = id
            $0.state = id == "proxycheck.io" ? "failed" : "observed"
            $0.errorCode = id == "proxycheck.io" ? "provider_http_403" : ""
          }
        }
        $0.facts = [
          .with {
            $0.group = "source_network"
            $0.providerID = "ripestat"
            $0.value = "203.0.113.0/24"
          }
        ]
      })
    let all = P.sourceRows(value)
    #expect(all.first { $0.title == "DB-IP" }?.tone == .information)
    #expect(all.first { $0.title == "RIPEstat" }?.detail.contains("203.0.113.0/24") == true)
    #expect(all.first { $0.title == "proxycheck.io" }?.value.contains("403") == true)
    #expect(all.map(\.title) == ["proxycheck.io", "DB-IP", "ipapi.is", "RIPEstat"])
    #expect(P.sourceListOverview(all).text == "1 个来源缺失 · 4 个来源")
  }

  @Test("来源分歧和缺失并存时，先显示分歧并保留完整来源")
  func differencesComeFirst() {
    let value = NetworkDiagnosticReport(
      .with {
        $0.sources = [("ipquery", "US"), ("ipwho.is", "CA")].map { id, country in
          .with {
            $0.name = id
            $0.providerID = id
            $0.country = country
            $0.city = "Vancouver"
            $0.asn = "AS64500"
          }
        }
        $0.providers = [
          .with {
            $0.id = "db-ip"
            $0.state = "failed"
          }
        ]
      })
    let rows = P.sourceRows(value)
    #expect(rows.map(\.title) == ["ipquery", "ipwho.is", "DB-IP"])
    let summary = P.sourceListOverview(rows)
    #expect(summary.text == "2 个来源有分歧 · 1 个来源缺失 · 3 个来源")
    #expect(summary.tone == .difference)
  }

}
