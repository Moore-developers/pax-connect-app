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
    application(record, context: .chinese)
  }
  public static func application(_ record: LogRecord, context: PresentationContext) -> String {
    guard let flow = record.flow else { return record.categoryName }
    return record.categoryName.isEmpty
      ? FlowPresentation.application(flow, context: context) : record.categoryName
  }

  public init(
    detail: HistoryLogDetail, perspective: Perspective, context: PresentationContext = .chinese
  ) {
    let record = detail.record
    let flow = record?.flow ?? detail.flow
    let savedSource = record?.source ?? detail.event?.source ?? ""
    source = savedSource.isEmpty ? context.text("log_detail.source_missing") : savedSource
    status = FlowPresentation.recordedStatus(
      flow,
      fallback: record.map { ProductTextPresentation.recordTitle($0, context: context) } ?? detail
        .event?.message ?? context.text("log_detail.result_unknown"))
    var blocks: [Section] = []
    var missing: [String] = []
    if let f = flow {
      let dns = f.purpose == "dns"
      let application =
        record.map { Self.application($0, context: context) }
        ?? FlowPresentation.application(f, context: context)
      title =
        dns
        ? f.domain + " · DNS " + (f.dns?.queryType ?? context.text("log_detail.query"))
        : perspective == .domain
          ? FlowPresentation.domain(f, context: context) + " → "
            + FlowPresentation.ip(f, context: context)
          : application + " → " + FlowPresentation.target(f, context: context)
      if dns {
        var rows = [
          (context.text("log_detail.query_domain"), FlowPresentation.domain(f, context: context)),
          (
            context.text("log_detail.query_type"),
            f.dns?.queryType ?? context.text("log_detail.not_saved")
          ),
          (context.text("log_detail.source"), application),
        ]
        if let result = f.dns {
          rows.append(
            (
              context.text("log_detail.resolution_result"),
              result.result.isEmpty ? context.text("log_detail.no_result") : result.result
            ))
          rows.append(
            (
              context.text("log_detail.answers"),
              result.answers.isEmpty
                ? context.text("log_detail.no_answers") : result.answers.joined(separator: "\n")
            ))
        }
        rows.append(
          (
            context.text("log_detail.resolution_latency"),
            TrafficFormatting.milliseconds(TrafficFormatting.responseLatency(f))
          ))
        blocks.append(.init(title: context.text("log_detail.dns_query"), rows: rows))
      } else {
        var identity: [(String, String)] = []
        if perspective == .domain {
          identity.append(
            (
              context.text("log_detail.website"),
              f.primaryDomain.isEmpty ? f.domain : f.primaryDomain
            ))
        }
        identity += [
          (context.text("log_detail.application"), application),
          (
            context.text("log_detail.full_domain"),
            f.domain.isEmpty
              ? (f.ip.isEmpty ? "—" : context.text("log_detail.direct_ip")) : f.domain
          ),
          (
            context.text("log_detail.target_ip"),
            f.ip.isEmpty
              ? (f.ipSource == "proxy_domain"
                ? context.text("log_detail.proxy_ip_unavailable") : "—") : f.ip
          ),
        ]
        if f.port > 0 { identity.append((context.text("log_detail.port"), String(f.port))) }
        if !f.transport.isEmpty {
          identity.append((context.text("log_detail.transport"), f.transport.uppercased()))
        }
        if !f.application.isEmpty && f.application != application {
          identity.append((context.text("log_detail.recorded_application"), f.application))
        }
        if !f.process.isEmpty {
          identity.append((context.text("log_detail.process_path"), f.process))
        }
        blocks.append(
          .init(
            title: perspective == .domain
              ? context.text("log_detail.access_target")
              : context.text("log_detail.application_target"), rows: identity))
        if FlowPresentation.isIPv6Limited(f) {
          blocks.append(
            .init(
              title: context.text("log_detail.connection_limit"),
              rows: [
                (context.text("log_detail.ipv6_disabled"), TrafficRuleFormatting.ipv6Limitation)
              ]))
        }
        var connection = [
          (
            context.text("log_detail.actual_connection"),
            FlowPresentation.actualConnection(f, context: context)
          )
        ]
        if !f.rule.isEmpty {
          connection.append(
            (
              context.text("log_detail.rule_package"),
              TrafficRuleFormatting.package(rule: f.rule, source: f.ruleSource)
            ))
          connection.append(
            (
              context.text("log_detail.matching_condition"),
              TrafficRuleFormatting.condition(rule: f.rule, source: f.ruleSource)
            ))
        } else {
          missing.append(context.text("log_detail.rule_not_recorded"))
        }
        if FlowPresentation.isIPv6Limited(f) {
          connection += [
            (context.text("log_detail.original_direction"), f.direction),
            (context.text("log_detail.original_rule"), f.rule),
            (context.text("log_detail.original_source"), f.ruleSource),
          ]
        }
        blocks.append(.init(title: context.text("log_detail.connection_rules"), rows: connection))
        var metrics = [
          (
            context.text("log_detail.response_latency"),
            TrafficFormatting.milliseconds(TrafficFormatting.responseLatency(f))
          )
        ]
        if let m = record?.metrics {
          metrics += [
            (
              context.text("log_detail.connection_duration"),
              TrafficFormatting.connectionDuration(m)
            ),
            (context.text("log_detail.transfer"), TrafficFormatting.connectionBytes(m)),
          ]
          if m.bytesState != "complete" {
            missing.append(
              TrafficFormatting.connectionMetricsNote(m).replacingOccurrences(
                of: "仅为已观测下界，存在缺口", with: context.text("log_detail.incomplete_data")))
          }
        }
        blocks.append(.init(title: context.text("log_detail.connection_metrics"), rows: metrics))
      }
      if f.purpose.isEmpty { missing.append(context.text("log_detail.legacy_missing")) }
      if f.application.isEmpty { missing.append(FlowPresentation.application(f, context: context)) }
      if f.ip.isEmpty && !dns { missing.append(FlowPresentation.ip(f, context: context)) }
      if TrafficFormatting.responseLatency(f) == nil {
        missing.append(FlowPresentation.latencyReason(f, context: context))
      }
      if f.process.hasSuffix("/mDNSResponder") {
        missing.append(context.text("log_detail.system_dns"))
      }
    } else {
      title =
        record?.systemEvent.map { ProductTextPresentation.eventName($0, context: context) }
        ?? (status.split(separator: "：").first.map(String.init)
          ?? context.text("log_detail.system_event"))
      if let event = record?.systemEvent {
        for s in event.sections {
          let rows = s.fields.map { f in
            (
              ProductTextPresentation.staticEventLabel(
                f.labelText, original: f.label, context: context),
              !f.before.isEmpty || !f.after.isEmpty
                ? (f.before.isEmpty
                  ? context.text("log_detail.before_missing")
                  : ProductTextPresentation.text(f.beforeText, original: f.before, context: context))
                  + " → "
                  + (f.after.isEmpty
                    ? context.text("log_detail.after_missing")
                    : ProductTextPresentation.text(f.afterText, original: f.after, context: context))
                : ProductTextPresentation.text(f.valueText, original: f.value, context: context)
            )
          }
          if !rows.isEmpty {
            blocks.append(
              .init(
                title: s.title.isEmpty
                  ? context.text("log_detail.event_facts")
                  : ProductTextPresentation.staticEventLabel(
                    s.titleText, original: s.title, context: context), rows: rows))
          }
        }
        if let started = event.startedAt, let ended = record?.lastAt, ended >= started {
          blocks.append(
            .init(
              title: context.text("log_detail.operation_time"),
              rows: [
                (context.text("log_detail.started"), TrafficFormatting.dateMinute(started)),
                (context.text("log_detail.completed"), TrafficFormatting.dateMinute(ended)),
                (
                  context.text("log_detail.operation_duration"),
                  TrafficFormatting.milliseconds(ended.timeIntervalSince(started) * 1000)
                ),
              ]))
        }
        if !event.errorCode.isEmpty {
          missing.append(
            context.text("log_detail.error_category") + event.errorCode)
        }
        if event.sections.isEmpty { missing.append(context.text("log_detail.event_no_context")) }
      } else {
        if let r = record, !r.operationFrom.isEmpty || !r.operationTo.isEmpty {
          blocks.append(
            .init(
              title: context.text("log_detail.route_change"),
              rows: [
                (
                  context.text("log_detail.route"),
                  (r.operationFrom.isEmpty
                    ? context.text("log_detail.old_route_missing") : r.operationFrom) + " → "
                    + (r.operationTo.isEmpty
                      ? context.text("log_detail.new_route_missing") : r.operationTo)
                )
              ]))
        }
        missing.append(context.text("log_detail.legacy_event"))
      }
    }
    sections = blocks
    notes = Array(NSOrderedSet(array: missing).array.compactMap { $0 as? String })
  }
}
