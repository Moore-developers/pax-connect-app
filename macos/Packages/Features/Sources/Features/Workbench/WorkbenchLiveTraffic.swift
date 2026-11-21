import Foundation
import PaxCore

extension WorkbenchTrafficRecord {
  init(_ f: FlowFact, context: PresentationContext = .chinese) {
    self.init(
      id: f.key, applicationID: f.applicationID,
      application: f.sourceKind == "unknown"
        ? context.text("traffic_details.5273e10d6e")
        : FlowPresentation.application(f, context: context),
      process: f.process, domain: f.domain.isEmpty ? nil : f.domain,
      ip: FlowPresentation.ip(f),
      routeID: f.routeID,
      route: f.direction == "proxy"
        ? TrafficFormatting.actualConnection(
          direction: f.direction, route: f.routeName, context: context)
        : f.routeName,
      direction: f.direction == "proxy"
        ? context.text("traffic_details.5e84ea61e8")
        : f.direction == "direct"
          ? context.text("traffic_details.b06325c566")
          : FlowPresentation.connection(f, context: context),
      started: f.startedAt,
      transport: f.transport.isEmpty
        ? context.text("traffic_details.3ffc614a54") : f.transport.uppercased(),
      ended: f.closedAt != nil,
      failure: FlowPresentation.failure(f, context: context),
      establishmentMS: TrafficFormatting.responseLatency(f),
      upload: f.upload, download: f.download,
      hit: .init(
        condition: f.rule, kind: context.text("traffic_details.3fcb97b8dc"),
        source: f.ruleSource),
      port: f.port > 0 && f.port <= UInt16.max ? UInt16(f.port) : nil)
    sourceNote =
      f.sourceKind == "system"
      ? context.text("traffic_details.1f4b662752")
      : f.sourceKind == "tool" ? context.text("traffic_details.534e3e938c") : ""
    if f.ruleSource == "Pax 包：Geo 基础包", f.rule == "builtin=局域网与私有网络地址", f.ipSource == "connected" {
      sourceNote +=
        (sourceNote.isEmpty ? "" : "；")
        + context.text("traffic_details.261fa43895")
    }
    actualConnectionKey = f.direction + "\t" + f.routeID + "\t" + f.routeName
    directionCode = f.direction
    stateUnknown = f.stateUnknown
    latencyNote = FlowPresentation.latencyReason(f, context: context)
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
  init(detail: TrafficDetail, dimension: TrafficDimension, context: PresentationContext = .chinese)
  {
    self.init(
      id: detail.summary.id, name: detail.summary.name,
      identity: dimension == .application
        ? context.text("traffic_details.63c73c4730")
        : dimension == .domain
          ? context.text("traffic_details.2229524311")
          : dimension == .system ? context.text("traffic_details.5b50d7c4b5") : "IP",
      symbol: dimension == .application
        ? "app" : dimension == .domain ? "globe" : dimension == .system ? "gearshape" : "network",
      dimension: dimension, capturedAt: detail.asOf, lowerBound: detail.from,
      periodTitle: [
        "today": context.text("traffic_details.5c9cf5e0dd"),
        "yesterday": context.text("traffic_details.655d11bd2a"),
        "week": context.text("traffic_details.6082bd2a63"),
        "month": context.text("traffic_details.afcd5fc44b"),
      ][detail.window]
        ?? (detail.asOf.timeIntervalSince(detail.from) > 3600
          ? context.text("traffic_details.ab2c18802e")
          : detail.asOf.timeIntervalSince(detail.from) > 60
            ? context.text("traffic_details.03b4c3a9f0")
            : context.text("traffic_details.2bf8a3a26f")),
      interruption: nil,
      connections: detail.items.map { WorkbenchTrafficRecord($0, context: context) })
    self.context = context
    aggregate = detail
  }
}
