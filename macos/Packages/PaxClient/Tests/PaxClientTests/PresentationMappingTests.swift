import Foundation
import PaxProto
import Testing

@testable import PaxClient

struct PresentationMappingTests {
  @Test func savedMeaningAndOriginalFactsRemainSeparate() {
    let proto = Pax_V1_LogRecord.with {
      $0.title = "原始结果"
      $0.titleText.code = "event.result"
      $0.titleText.params = ["route": "用户名称"]
      $0.systemEvent.type = "route.edit"
      $0.systemEvent.name = "编辑线路"
      $0.systemEvent.nameText.code = "event.route.edit"
      $0.systemEvent.category.nameText.code = "event.category.routes"
      $0.systemEvent.sections = [
        .with {
          $0.titleText.code = "event.section.route_changes"
          $0.fields = [
            .with {
              $0.label = "线路"
              $0.labelText.code = "event.field.route"
              $0.before = "我的线路"
              $0.after = "My route"
            }
          ]
        }
      ]
    }
    let record = LogRecord(proto)
    #expect(record.title == "原始结果")
    #expect(record.titleText?.params["route"] == "用户名称")
    #expect(record.systemEvent?.sections.first?.fields.first?.before == "我的线路")
    #expect(
      record.systemEvent?.sections.first?.fields.first?.labelText?.code == "event.field.route")
    #expect(LogRecord(Pax_V1_LogRecord()).titleText == nil)
  }
  @Test func diagnosticArchivesWithoutOptionalMeaningRemainReadable() throws {
    let old = Data(
      #"{"group":"risk","label":"未知原文","value":"value","source":"Vendor","state":"unknown"}"#.utf8)
    let fact = try JSONDecoder().decode(NetworkDiagnosticReport.Fact.self, from: old)
    #expect(fact.kind == nil && fact.labelText == nil)
    #expect(fact.label == "未知原文" && fact.source == "Vendor")
    let wire = Pax_V1_BrowserPresentation.with {
      $0.locale = "en"
      $0.messages = ["browser.title": "My browser check"]
    }
    let value = BrowserPresentation(locale: wire.locale, messages: wire.messages)
    #expect(value.proto == wire)
  }
  @Test func sessionNotesKeepLegacyFallbackAndTypedFacts() {
    let old = AISessionLimitRow(
      .with {
        $0.note = "原文"
        $0.title = "旧标题"
      })
    #expect(old.sessionCount == nil && old.titleSource.isEmpty && old.rawTitle.isEmpty)
    #expect(old.note == "原文" && old.title == "旧标题")
    let value = AISessionLimitRow(
      .with {
        $0.sessionCount = 3
        $0.titleSource = "claude.user-message"
        $0.rawTitle = "用户 {title}"
        $0.note = "3 个会话"
        $0.title = "最近消息：用户 {title}"
      })
    #expect(value.sessionCount == 3 && value.rawTitle == "用户 {title}")
    #expect(value.titleSource == "claude.user-message" && value.note == "3 个会话")
  }

}
