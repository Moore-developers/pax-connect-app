import Foundation
import Testing

@testable import PaxCore

@Suite("数据驱动详情") struct LogDetailPresentationTests {
  @Test func localizedEventKeepsUserNamesAndUnknownMeaning() throws {
    let catalog = try LanguageCatalog.bundled()
    let context = PresentationContext(
      language: .english, messages: try #require(catalog.packs[.english]).messages)
    var event = SystemEvent()
    event.type = "route.edit"
    event.name = "编辑线路"
    event.nameText = ProductText(code: "event.route.edit")
    event.result = "completed"
    var field = EventField()
    field.label = "线路"
    field.labelText = ProductText(code: "event.field.route")
    field.before = "开启代理"
    field.after = "用户自定路线"
    var section = EventSection()
    section.kind = "changes"
    section.title = "线路变化"
    section.titleText = ProductText(code: "event.section.route_changes")
    section.fields = [field]
    event.sections = [section]
    var record = LogRecord()
    record.kind = "operation"
    record.systemEvent = event
    record.title = "编辑线路：已完成"
    var detail = HistoryLogDetail()
    detail.record = record
    let plan = LogDetailPresentation(detail: detail, perspective: .system, context: context)
    #expect(plan.title == "Edit Route")
    #expect(plan.sections[0].title == "Route Changes")
    #expect(plan.sections[0].rows[0].0 == "Route")
    #expect(plan.sections[0].rows[0].1 == "开启代理 → 用户自定路线")
    #expect(
      ProductTextPresentation.recordTitle(record, context: context) == "Edit Route: Completed")
    #expect(
      ProductTextPresentation.text(
        ProductText(code: "future.unknown"), original: "原文", context: context) == "原文")
  }
  @Test("新系统类别无需页面模板，事实与变化不出现网络字段")
  func unknownSystem() {
    var r = LogRecord()
    r.kind = "operation"
    r.title = "更新信任策略：已完成"
    r.source = "fixture"
    var event = SystemEvent()
    event.name = "更新信任策略"
    event.type = "trust.change"
    event.category.name = "证书与信任"
    var field = EventField()
    field.label = "策略"
    field.before = "严格"
    field.after = "系统信任"
    var section = EventSection()
    section.kind = "changes"
    section.title = "变更"
    section.fields = [field]
    event.sections = [section]
    r.systemEvent = event
    var d = HistoryLogDetail()
    d.record = r
    let plan = LogDetailPresentation(detail: d, perspective: .system)
    #expect(plan.title == "更新信任策略")
    #expect(plan.sections.count == 1 && plan.sections[0].rows[0].1 == "严格 → 系统信任")
    #expect(
      !plan.sections.flatMap(\.rows).contains {
        ["域名", "端口", "上传／下载", "连接时长", "应用"].contains($0.0)
      })
  }
  @Test("应用域名只是同一连接视角，DNS只展示查询信息")
  func accessAndDNS() {
    var f = FlowFact()
    f.application = "ego lite"
    f.domain = "api.x.com"
    f.primaryDomain = "x.com"
    f.ip = "192.0.2.1"
    f.port = 443
    f.responseKnown = true
    f.responseMS = 82
    f.purpose = "access"
    var r = LogRecord()
    r.flow = f
    r.title = "响应：已收到回包"
    r.metrics.bytesState = "partial"
    r.metrics.upload = 4100
    r.metrics.download = 3300
    var d = HistoryLogDetail()
    d.record = r
    let app = LogDetailPresentation(detail: d, perspective: .application)
    let domain = LogDetailPresentation(detail: d, perspective: .domain)
    #expect(app.title == "ego lite → api.x.com" && domain.title == "api.x.com → 192.0.2.1")
    #expect(app.status == domain.status)
    #expect(app.sections.flatMap(\.rows).contains { $0.0 == "上传／下载" && !$0.1.contains("≥") })
    #expect(app.notes.contains { $0.contains("不完整") })
    f.purpose = "dns"
    var dns = DNSFact()
    dns.queryType = "A"
    dns.result = "NOERROR"
    dns.answers = ["192.0.2.2"]
    f.dns = dns
    r.flow = f
    d.record = r
    let plan = LogDetailPresentation(detail: d, perspective: .domain)
    #expect(plan.sections.count == 1 && plan.sections[0].title == "DNS 查询")
    #expect(!plan.sections.flatMap(\.rows).contains { $0.0 == "上传／下载" || $0.0 == "端口" })
  }
}
