import Foundation
import PaxCore

extension RoutingWorkbenchStore {
  /// 将已筛选行的连接事实投影为共享详情；保留原时间窗与历史来源 ID。
  func trafficDetail(for row: TrafficRow) -> WorkbenchTrafficDetail {
    let lower = now.addingTimeInterval(-period.seconds)
    let hits = row.hits
    let records = row.connections.map { connection in
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
    return WorkbenchTrafficDetail(
      id: row.id, name: row.name, identity: dimension.title + " · " + row.subtitle,
      symbol: row.symbol,
      dimension: dimension == .system ? .system : dimension == .domain ? .domain : .application,
      capturedAt: now, lowerBound: lower, periodTitle: period.title, interruption: nil,
      recordingMessage: stateMessage, connections: records)
  }
}
