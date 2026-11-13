import Foundation
import PaxCore

extension WorkbenchTrafficRecord {
  init(_ f: FlowFact) {
    self.init(
      id: f.key, applicationID: f.applicationID,
      application: f.sourceKind == "unknown" ? "未识别来源" : FlowPresentation.application(f),
      process: f.process, domain: f.domain.isEmpty ? nil : f.domain,
      ip: FlowPresentation.ip(f),
      routeID: f.routeID,
      route: f.direction == "proxy"
        ? TrafficFormatting.actualConnection(direction: f.direction, route: f.routeName)
        : f.routeName,
      direction: f.direction == "proxy"
        ? "代理" : f.direction == "direct" ? "直连" : FlowPresentation.connection(f),
      started: f.startedAt, transport: f.transport.isEmpty ? "协议未记录" : f.transport.uppercased(),
      ended: f.closedAt != nil,
      failure: FlowPresentation.isIPv6Limited(f)
        ? "未连接：IPv6 未启用" : f.failure.isEmpty ? nil : FlowPresentation.failure(f.failure),
      establishmentMS: TrafficFormatting.responseLatency(f),
      upload: f.upload, download: f.download,
      hit: .init(
        condition: f.rule.isEmpty ? "未记录" : f.rule, kind: "建立时依据",
        source: f.ruleSource.isEmpty ? "来源不可定位" : f.ruleSource),
      port: f.port > 0 && f.port <= UInt16.max ? UInt16(f.port) : nil)
    sourceNote =
      f.sourceKind == "system"
      ? "调用应用未记录"
      : f.sourceKind == "tool" ? "具体脚本或调用者未采集；保留直接发起连接的进程" : ""
    if f.ruleSource == "Pax 包：Geo 基础包", f.rule == "builtin=局域网与私有网络地址", f.ipSource == "connected" {
      sourceNote +=
        (sourceNote.isEmpty ? "" : "；")
        + "规则条件为当时记录；此处IP为最终连接地址，未保存匹配时地址，无法仅据最终IP复核私网命中"
    }
    stateUnknown = f.stateUnknown
    latencyNote = FlowPresentation.latencyReason(f)
  }
}
extension WorkbenchTrafficDetail {
  /// 与列表第一列相同的图标身份：系统行沿用应用图标，演示数据以名称兜底。
  var iconObject: (kind: WorkbenchObjectIcons.Kind, id: String) {
    let id = aggregate?.summary.objectID ?? ""
    switch dimension {
    case .application: return (.application, id.isEmpty ? name : id)
    case .system: return (.application, id.isEmpty ? self.id : id)
    case .domain: return (.domain, id.isEmpty ? name : id)
    case .ip: return (.ip, id.isEmpty ? name : id)
    }
  }
  init(detail: TrafficDetail, dimension: TrafficDimension) {
    self.init(
      id: detail.summary.id, name: detail.summary.name,
      identity: dimension == .application
        ? "应用" : dimension == .domain ? "域名" : dimension == .system ? "系统" : "IP",
      symbol: dimension == .application
        ? "app" : dimension == .domain ? "globe" : dimension == .system ? "gearshape" : "network",
      dimension: dimension, capturedAt: detail.asOf, lowerBound: detail.from,
      periodTitle: ["today": "今日", "yesterday": "昨日", "week": "近7天", "month": "近30天"][detail.window]
        ?? (detail.asOf.timeIntervalSince(detail.from) > 3600
          ? "最近 24 小时" : detail.asOf.timeIntervalSince(detail.from) > 60 ? "最近 1 小时" : "最近 1 分钟"),
      interruption: nil,
      connections: detail.items.map(WorkbenchTrafficRecord.init))
    aggregate = detail
  }
}
