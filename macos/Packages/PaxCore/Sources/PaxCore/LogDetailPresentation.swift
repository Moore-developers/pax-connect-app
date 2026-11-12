import Foundation

/// One immutable presentation plan for access, DNS and arbitrary system events.
public struct LogDetailPresentation: Sendable {
  public enum Perspective: Sendable { case application, domain, system }
  public struct Section: Sendable {
    public let title: String
    public let rows: [(String, String)]
  }
  public let title: String
  public let status: String
  public let sections: [Section]
  public let notes: [String]
  public let source: String

  public static func application(_ record: LogRecord) -> String {
    guard let flow = record.flow else { return record.categoryName }
    return record.categoryName.isEmpty ? FlowPresentation.application(flow) : record.categoryName
  }

  public init(detail: HistoryLogDetail, perspective: Perspective) {
    let record = detail.record
    let flow = record?.flow ?? detail.flow
    let savedSource = record?.source ?? detail.event?.source ?? ""
    source = savedSource.isEmpty ? "来源未记录" : savedSource
    status = FlowPresentation.recordedStatus(
      flow, fallback: record?.title ?? detail.event?.message ?? "结果未确认")
    var blocks: [Section] = []
    var missing: [String] = []
    if let f = flow {
      let dns = f.purpose == "dns"
      let application = record.map(Self.application) ?? FlowPresentation.application(f)
      title =
        dns
        ? f.domain + " · DNS " + (f.dns?.queryType ?? "查询")
        : perspective == .domain
          ? FlowPresentation.domain(f) + " → " + FlowPresentation.ip(f)
          : application + " → " + FlowPresentation.target(f)
      if dns {
        var rows = [
          ("查询域名", FlowPresentation.domain(f)), ("查询类型", f.dns?.queryType ?? "未保存"),
          ("来源", application),
        ]
        if let result = f.dns {
          rows.append(("解析结果", result.result.isEmpty ? "尚无结果" : result.result))
          rows.append(
            ("解析答案", result.answers.isEmpty ? "未返回地址或别名" : result.answers.joined(separator: "\n")))
        }
        rows.append(("解析耗时", TrafficFormatting.milliseconds(TrafficFormatting.responseLatency(f))))
        blocks.append(.init(title: "DNS 查询", rows: rows))
      } else {
        var identity: [(String, String)] = []
        if perspective == .domain {
          identity.append(("所属网站", f.primaryDomain.isEmpty ? f.domain : f.primaryDomain))
        }
        identity += [
          ("应用", application),
          ("完整域名", f.domain.isEmpty ? (f.ip.isEmpty ? "—" : "直接访问 IP") : f.domain),
          ("目标 IP", f.ip.isEmpty ? (f.ipSource == "proxy_domain" ? "代理未提供" : "—") : f.ip),
        ]
        if f.port > 0 { identity.append(("端口", String(f.port))) }
        if !f.transport.isEmpty { identity.append(("传输协议", f.transport.uppercased())) }
        if !f.application.isEmpty && f.application != application {
          identity.append(("记录的应用名", f.application))
        }
        if !f.process.isEmpty { identity.append(("进程路径", f.process)) }
        blocks.append(.init(title: perspective == .domain ? "访问对象" : "应用与目标", rows: identity))
        if FlowPresentation.isIPv6Limited(f) {
          blocks.append(
            .init(title: "连接限制", rows: [("IPv6 未启用", TrafficRuleFormatting.ipv6Limitation)]))
        }
        var connection = [("实际连接", FlowPresentation.actualConnection(f))]
        if !f.rule.isEmpty {
          connection.append(
            ("规则包", TrafficRuleFormatting.package(rule: f.rule, source: f.ruleSource)))
          connection.append(
            ("触发条件", TrafficRuleFormatting.condition(rule: f.rule, source: f.ruleSource)))
        } else {
          missing.append("未保存当时命中的规则。")
        }
        if FlowPresentation.isIPv6Limited(f) {
          connection += [("原始去向", f.direction), ("原始规则", f.rule), ("原始来源", f.ruleSource)]
        }
        blocks.append(.init(title: "实际连接与规则", rows: connection))
        var metrics = [
          ("响应延迟", TrafficFormatting.milliseconds(TrafficFormatting.responseLatency(f)))
        ]
        if let m = record?.metrics {
          metrics += [
            ("连接存续时长", TrafficFormatting.connectionDuration(m)),
            ("上传／下载", TrafficFormatting.connectionBytes(m)),
          ]
          if m.bytesState != "complete" {
            missing.append(
              TrafficFormatting.connectionMetricsNote(m).replacingOccurrences(
                of: "仅为已观测下界，存在缺口", with: "数据不完整，仅统计已记录部分"))
          }
        }
        blocks.append(.init(title: "连接指标", rows: metrics))
      }
      if f.purpose.isEmpty { missing.append("这条旧记录的部分字段当时未保存，现有数据无法补回。") }
      if f.application.isEmpty { missing.append(FlowPresentation.application(f)) }
      if f.ip.isEmpty && !dns { missing.append(FlowPresentation.ip(f)) }
      if TrafficFormatting.responseLatency(f) == nil {
        missing.append(FlowPresentation.latencyReason(f))
      }
      if f.process.hasSuffix("/mDNSResponder") { missing.append("查询由系统 DNS 服务发出，不能确认最初调用它的应用。") }
    } else {
      title = record?.systemEvent?.name ?? String(status.split(separator: "：").first ?? "系统事件")
      if let event = record?.systemEvent {
        for s in event.sections {
          let rows = s.fields.map { f in
            (
              f.label,
              !f.before.isEmpty || !f.after.isEmpty
                ? (f.before.isEmpty ? "未保存旧值" : f.before) + " → "
                  + (f.after.isEmpty ? "未保存新值" : f.after) : f.value
            )
          }
          if !rows.isEmpty {
            blocks.append(.init(title: s.title.isEmpty ? "事件事实" : s.title, rows: rows))
          }
        }
        if let started = event.startedAt, let ended = record?.lastAt, ended >= started {
          blocks.append(
            .init(
              title: "操作时间",
              rows: [
                ("开始", TrafficFormatting.dateMinute(started)),
                ("完成", TrafficFormatting.dateMinute(ended)),
                ("操作耗时", TrafficFormatting.milliseconds(ended.timeIntervalSince(started) * 1000)),
              ]))
        }
        if !event.errorCode.isEmpty {
          missing.append(
            "错误类别：" + event.errorCode)
        }
        if event.sections.isEmpty { missing.append("这次事件只保存了结果，没有保存更多上下文。") }
      } else {
        if let r = record, !r.operationFrom.isEmpty || !r.operationTo.isEmpty {
          blocks.append(
            .init(
              title: "线路变化",
              rows: [
                (
                  "线路",
                  (r.operationFrom.isEmpty ? "旧线路未保存" : r.operationFrom) + " → "
                    + (r.operationTo.isEmpty ? "新线路未保存" : r.operationTo)
                )
              ]))
        }
        missing.append("这条历史事件未保存结构化详情，仅展示已有结果。")
      }
    }
    sections = blocks
    notes = Array(NSOrderedSet(array: missing).array.compactMap { $0 as? String })
  }
}
