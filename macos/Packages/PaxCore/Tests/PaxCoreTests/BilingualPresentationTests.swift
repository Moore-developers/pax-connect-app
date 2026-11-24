import Foundation
import PaxProto
import Testing

@testable import PaxClient
@testable import PaxCore

@MainActor struct BilingualPresentationTests {
  private func english() throws -> PresentationContext {
    let pack = try #require(LanguageCatalog.bundled().packs[.english])
    return PresentationContext(language: .english, messages: pack.messages)
  }

  @Test func renamedEnvironmentHeadingsKeepTheirMeaningAndReadings() throws {
    let original = try english()
    var messages = original.messages
    messages["environment.row.public_ip"] = .text("Renamed public address")
    messages["environment.row.language"] = .text("A different heading")
    let custom = PresentationContext(language: .english, revision: 2, messages: messages)
    let local = NetworkDiagnosticEnvironment(
      timezone: "America/Los_Angeles", languages: ["en-US"], region: "en_US", at: .now)
    let report = EnvironmentDiagnosticReport(
      .with {
        $0.mac = .with {
          $0.complete = true
          $0.ipv4 = "8.8.8.8"
        }
        $0.browser = .with {
          $0.complete = true
          $0.ipv4 = "1.1.1.1"
          $0.browser = "My Browser 名称"
          $0.languages = ["en-US"]
          $0.timezone = "America/Los_Angeles"
        }
      })
    let baseline = EnvironmentDiagnosticPresentation.assessedRows(
      report, local: local, waiting: false, context: original)
    let rows = EnvironmentDiagnosticPresentation.assessedRows(
      report, local: local, waiting: false, context: custom)
    #expect(rows.map(\.kind) == baseline.map(\.kind))
    #expect(rows.map(\.value) == baseline.map(\.value))
    #expect(rows.first?.title == "Renamed public address")
    #expect(rows.first?.value.contains("Exit differs") == true)
    #expect(rows[1].title == "A different heading")
    #expect(report.browser.languages == ["en-US"])
    #expect(report.browser.browser == "My Browser 名称")
  }

  @Test func diagnosticDetailsUseTheChosenSnapshotAndRetainUnknownEvidence() throws {
    let context = try english()
    let report = NetworkDiagnosticReport(
      .with {
        $0.providers = [
          .with {
            $0.id = "ipx.al"
            $0.state = "failed"
            $0.errorCode = "provider_timeout"
          }
        ]
        $0.facts = [
          .with {
            $0.group = "extra"
            $0.label = "Unknown 原文"
            $0.value = "Fact [value] <raw>"
            $0.source = "Provider 原文"
            $0.state = "unknown"
          }
        ]
      })
    let details = NetworkDiagnosticPresentation.providerDetails(
      report, id: "ipx.al", context: context)
    #expect(details.contains("Provider request timed out"))
    let fact = try #require(report.facts.first)
    #expect(fact.label == "Unknown 原文")
    #expect(NetworkDiagnosticPresentation.factValue(fact, context: context) == "Fact [value] <raw>")
    #expect(NetworkDiagnosticPresentation.sourceRows(report, context: context).isEmpty)
  }
  @Test func retainedFailuresAndDismissedFeedbackDoNotChangeIdentityWithLanguage() throws {
    let context = try english()
    let failure = ProductFailure(ProductError(code: "history_cursor_expired"))
    #expect(failure.message(context: context) == context.text("error.history_cursor_expired"))
    #expect(ProductFailure.original("Unknown 用户原文").message(context: context) == "Unknown 用户原文")
    let store = ConnectionStore()
    let original = store.feedback
    let localized = store.feedback(context: context)
    #expect(localized.map(\.id) == original.map(\.id))
    #expect(localized.map(\.action) == original.map(\.action))
    #expect(localized.first?.title != original.first?.title)
    store.dismissAllFeedback()
    #expect(store.feedback.isEmpty)
    #expect(store.feedback(context: context).isEmpty)
  }
  @Test func problemSummariesLocalizeOnlyKnownReducerTitles() throws {
    let context = try english()
    let title = "连接或操作失败 · 原因未分类"
    let original = ProductLog(
      at: .now, level: .error, message: title, operationID: "", snapshotVersion: 1)
    var problem = original
    problem.problemID = "problem:recorded"
    problem.category = "failure"
    #expect(
      ProductTextPresentation.logTitle(problem, context: context)
        == "Connection or Operation Failed · Unclassified")
    #expect(ProductTextPresentation.logTitle(problem, context: .chinese) == title)
    #expect(ProductTextPresentation.logTitle(original, context: context) == title)
  }

  @Test func savedFlowStatusesUseEnglishAndPreserveOriginalText() throws {
    let context = try english()
    let flow = FlowFact()
    let cases = [
      ("连接结束：此前收到回包", "Closed · Reply received"),
      ("连接结束：未观测到回包", "Closed · No reply seen"),
      ("域名解析：解析成功", "DNS · Resolved"),
      ("域名解析：域名不存在", "DNS · NXDOMAIN"),
      ("线路选择：连接超时", "Routing · Timed out"),
    ]
    for (original, expected) in cases {
      #expect(
        FlowPresentation.recordedStatus(flow, fallback: original, context: context) == expected)
      #expect(
        FlowPresentation.recordedStatus(flow, fallback: original, context: .chinese) == original)
    }
    let custom = "自定义原文：域名解析：解析成功"
    #expect(FlowPresentation.recordedStatus(flow, fallback: custom, context: context) == custom)
    #expect(
      FlowPresentation.recordedStatus(nil, fallback: cases[0].0, context: context) == cases[0].0)
  }

}
