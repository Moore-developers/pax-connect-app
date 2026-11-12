import Foundation
import PaxProto
import Testing

@testable import PaxClient

@Suite("网络诊断契约") struct NetworkDiagnosticMappingTests {
  @Test("缺失评分不能映射为0分，未知风险和跨国分歧保留")
  func partialEvidence() {
    let raw = Pax_V1_NetworkDiagnosticReport.with {
      $0.targetRouteID = "target"
      $0.runningRouteID = "running"
      $0.changedDuringCapture = true
      $0.sources = [
        .with {
          $0.name = "one"
          $0.country = "US"
        },
        .with {
          $0.name = "two"
          $0.country = "CA"
        },
      ]
      $0.facts = [
        .with {
          $0.group = "risk"
          $0.label = "VPN"
          $0.state = "unknown"
        }
      ]
      $0.probes = [
        .with {
          $0.target = "https://api.anthropic.com/v1/models"
          $0.httpStatus = 401
          $0.expectedResponse = false
        }
      ]
    }
    let value = NetworkDiagnosticReport(raw)
    #expect(value.score == nil)
    #expect(value.targetRouteID != value.runningRouteID)
    #expect(value.changedDuringCapture)
    #expect(value.sources.map(\.country) == ["US", "CA"])
    #expect(value.facts.first?.state == "unknown")
    #expect(value.probes.first?.httpStatus == 401)
    #expect(value.probes.first?.expectedResponse == false)
    var zero = raw
    zero.score = 0
    #expect(NetworkDiagnosticReport(zero).score == 0)
  }
}
