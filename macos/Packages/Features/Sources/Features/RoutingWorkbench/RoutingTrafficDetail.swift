import Foundation
import PaxCore

extension RoutingWorkbenchStore {
  /// 将已筛选行的连接事实投影为共享详情；保留原时间窗与历史来源 ID。
  func trafficDetail(for row: TrafficRow) -> WorkbenchTrafficDetail {
    let lower = now.addingTimeInterval(-period.seconds)
    let hits = row.hits
    var records = row.connections.map { connection in
      let application = applications.first { $0.id == connection.applicationID }
      let transfers = connection.transfers.filter { $0.at > lower && $0.at <= now }
      let hit = connection.hit
      let parts = hit.basis.components(separatedBy: "：")
      return WorkbenchTrafficRecord(
        id: connection.id,
        applicationID: connection.applicationID ?? "unknown:" + connection.process,
        application: application?.name ?? "未识别进程", process: connection.process,
        domain: connection.domain, ip: connection.ip, routeID: connection.routeID ?? "direct",
        route: connection.routeName, direction: connection.direction.title,
        started: connection.started, transport: "协议未记录",
        ended: connection.ended.map { $0 <= now } ?? false,
        failure: nil, establishmentMS: connection.establishmentMS,
        upload: transfers.reduce(0) { $0 + $1.upload },
        download: transfers.reduce(0) { $0 + $1.download },
        hit: .init(
          condition: hit.sourceID == nil
            ? "未命中规则"
            : parts.count > 1 ? parts.dropFirst().joined(separator: "：") : hit.basis,
          kind: hit.sourceID == nil ? "走当前线路" : parts.count > 1 ? parts[0] : hit.category,
          source: hit.sourceID == nil ? "无" : hit.title,
          sourceKey: hit.sourceID != nil ? hits.firstIndex(of: hit).map(String.init) : nil))
    }
    let statusDemo =
      telemetry == nil && ProcessInfo.processInfo.environment["PAX_DEMO_TARGET_STATUS"] == "1"
    if statusDemo, let template = records.first {
      records = Self.demoTargetStatuses(template: template, at: now)
    }
    return WorkbenchTrafficDetail(
      id: row.id, name: row.name + (statusDemo ? " · 模拟数据" : ""),
      identity: dimension.title + " · " + row.subtitle,
      symbol: row.symbol,
      dimension: dimension == .system ? .system : dimension == .domain ? .domain : .application,
      capturedAt: now, lowerBound: lower, periodTitle: period.title, interruption: nil,
      recordingMessage: stateMessage, connections: records)
  }
}

extension RoutingWorkbenchStore {
  /// Opt-in offline fixture for the real target table, including mixed states on one target.
  private static func demoTargetStatuses(template: WorkbenchTrafficRecord, at: Date)
    -> [WorkbenchTrafficRecord]
  {
    let targets: [(name: String, count: Int, failed: Int, active: Int)] = [
      ("download.deepseek.com", 45, 10, 1),
      ("cdn.jjdsn.site", 12, 3, 2),
      ("api.example.com", 6, 0, 6),
      ("failed.example.com", 4, 4, 0),
    ]
    return targets.flatMap { target in
      (0..<target.count).map { index in
        let failed = index < target.failed
        let active = index >= target.failed && index < target.failed + target.active
        return WorkbenchTrafficRecord(
          id: "target-status-\(target.name)-\(index)",
          applicationID: template.applicationID, application: template.application,
          process: template.process,
          domain: target.name, ip: "203.0.113.8", routeID: template.routeID, route: template.route,
          direction: template.direction, started: at.addingTimeInterval(-Double(10 + index)),
          transport: "TCP", ended: !active, failure: failed ? "模拟连接失败" : nil,
          establishmentMS: failed ? nil : 86, upload: failed ? 0 : 1024,
          download: failed ? 0 : 2048,
          hit: template.hit, port: 443)
      }
    }
  }
}
