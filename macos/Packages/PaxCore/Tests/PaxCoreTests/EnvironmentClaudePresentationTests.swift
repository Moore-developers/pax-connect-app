import Foundation
import PaxProto
import Testing

@testable import PaxClient
@testable import PaxCore

@MainActor @Suite("Claude browser evidence") struct EnvironmentClaudePresentationTests {
  typealias P = EnvironmentDiagnosticPresentation

  private func sample(
    v4: String = "", v6: String = "", egress: String, status: String,
    statusState: String = "observed"
  ) -> EnvironmentDiagnosticReport.Sample {
    EnvironmentDiagnosticReport.Sample(
      .with {
        $0.ipv4 = v4
        $0.ipv6 = v6
        $0.checks = [
          .with {
            $0.kind = "echo_claude"
            $0.state = "observed"
            $0.value = egress
          },
          .with {
            $0.kind = "claude_status"
            $0.state = statusState
            $0.value = status
            if statusState == "timeout" { $0.errorCode = "probe_timeout" }
          },
        ]
      })
  }

  @Test func comparesOnlyTheSameFamilyAndNormalizesIPv6() {
    let same = sample(
      v6: "2001:4860:4860::8888", egress: "2001:4860:4860:0:0:0:0:8888 · US",
      status: "operational")
    #expect(P.claudeReading(.exit, sample: same).tone == .matching)
    #expect(P.claudeReading(.exit, sample: same).text.contains("美国"))
    let different = sample(v4: "8.8.8.8", egress: "1.1.1.1", status: "operational")
    #expect(P.claudeReading(.exit, sample: different).tone == .difference)
    let noMatch = sample(v4: "1.1.1.1", egress: "2001:4860:4860::8888", status: "operational")
    #expect(P.claudeReading(.exit, sample: noMatch).text.contains("无法对照"))
  }

  @Test func showsOfficialOutageAndTimeoutWithoutReplacingEgress() {
    let outage = sample(v4: "8.8.8.8", egress: "8.8.8.8", status: "partial_outage")
    #expect(P.claudeReading(.status, sample: outage).text == "官方报告部分故障")
    let timeout = sample(v4: "8.8.8.8", egress: "8.8.8.8", status: "", statusState: "timeout")
    #expect(P.claudeReading(.status, sample: timeout).text == "超时")
    #expect(P.claudeReading(.exit, sample: timeout).tone == .matching)
    #expect(P.claudeReading(.status, sample: nil).text == "未取得")
  }

  @Test func reportsTraceHealthAndLegacyTLS() {
    func reading(_ kind: P.ClaudeCheck, value: String, error: String = "") -> String {
      let sample = EnvironmentDiagnosticReport.Sample(
        .with {
          $0.checks = [
            .with {
              $0.kind = kind.rawValue
              $0.value = value
              $0.state = error.isEmpty ? "observed" : "failed"
              $0.httpStatus = 200
              $0.errorCode = error
            }
          ]
        })
      return P.claudeReading(kind, sample: sample).text
    }
    #expect(reading(.edge, value: "IAD") == "正常 · IAD")
    #expect(reading(.connection, value: "HTTP/2 · TLS 1.3") == "正常 · HTTP/2 · TLS 1.3")
    #expect(reading(.connection, value: "HTTP/1.1 · TLS 1.1").hasPrefix("异常 · 旧版 TLS"))
    #expect(
      reading(.connection, value: "", error: "probe_response_invalid")
        == "无法确认 · 字段缺失或无效")
  }

  @Test func distinguishesCredentialServicesFromDiagnosticEntries() {
    let outcome = ClaudeKeychainReading.inspectServices([
      "Claude Code-doctor-probe", "Claude Code-credentials", "Claude Code-credentials-525493ee",
      "Claude Code-credentials-unrelated", "Claude Code-credentials-1234",
    ])
    #expect(outcome == .checked(doctorProbeCount: 1, defaultCount: 1, otherCount: 1))
    let result = P.claudeKeychainReading(.init(outcome: outcome, at: .now), checking: false)
    #expect(result.text == "诊断条目 1 条")
    #expect(result.tone == .failure)
    #expect(result.detail.contains("其他匹配 1 条"))
    #expect(result.detail.hasPrefix("Claude Code-doctor-probe"))
    let empty = ClaudeKeychainReading.inspectServices(["Claude Code-doctor-probe-backup"])
    let cleared = P.claudeKeychainReading(.init(outcome: empty, at: .now), checking: false)
    #expect(cleared.text == "诊断条目 0 条")
    #expect(cleared.tone == .matching)
    let duplicate = ClaudeKeychainReading.inspectServices(
      Array(repeating: "Claude Code-doctor-probe", count: 2))
    let repeated = P.claudeKeychainReading(.init(outcome: duplicate, at: .now), checking: false)
    #expect(repeated.text == "诊断条目 2 条")
    #expect(repeated.tone == .failure)

    #expect(
      P.claudeKeychainReading(.init(outcome: .restricted, at: .now), checking: false).text
        == "无法确认 · 钥匙串访问受限")
  }

  @Test func cardOnlyShowsCurrentClaudeChecks() {
    #expect(
      P.ClaudeCheck.allCases.map(\.rawValue) == [
        "echo_claude", "claude_status", "echo_claude_api", "claude_api_status",
        "claude_edge", "claude_protocol",
      ])
  }
}
