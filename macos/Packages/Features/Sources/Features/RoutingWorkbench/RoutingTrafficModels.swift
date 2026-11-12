import Foundation
import PaxUI

extension RoutingWorkbenchStore {
  struct Transfer: Equatable {
    let at: Date
    let upload: UInt64
    let download: UInt64
  }
  struct Connection: Equatable, Identifiable {
    let id: String
    let applicationID: String?
    let process: String
    let domain: String?
    let ip: String
    let routeID: String?
    let routeName: String
    let direction: Direction
    let hit: Hit
    let started: Date
    let ended: Date?
    let establishmentMS: Double?
    let transfers: [Transfer]
  }
  struct TrafficRow: Equatable, Identifiable {
    let id: String
    let name: String
    let subtitle: String
    let symbol: String
    let route: String
    let direction: Direction
    let connections: [Connection]
    let upload: UInt64
    let download: UInt64
    let establishment: [PaxTrendSample]
    var aggregateCount: Int? = nil
    var aggregateBasis: String? = nil
    var aggregatePeak: Double? = nil
    var latestLatency: Double? = nil
    var total: UInt64 { upload + download }
    var count: Int { aggregateCount ?? connections.count }
    var hits: [Hit] {
      var result: [Hit] = []
      for connection in connections where !result.contains(connection.hit) {
        result.append(connection.hit)
      }
      return result
    }
    var slowest: Double? { aggregatePeak ?? establishment.compactMap(\.value).max() }
    private var categoryCounts: [String: Int] {
      Dictionary(grouping: connections, by: { $0.hit.category }).mapValues(\.count)
    }
    var basisTitle: String {
      if let aggregateBasis { return aggregateBasis }
      guard hits.count > 1 else { return hits.first?.basis ?? "暂无分流依据" }
      let counts = categoryCounts
      return counts.count > 1
        ? "\(counts.count) 种分流依据"
        : (counts.keys.first ?? "规则") + " · \(hits.count) 个匹配条件"
    }
    var basisSubtitle: String {
      guard hits.count > 1 else { return hits.first?.basisSource ?? "" }
      let counts = categoryCounts
      return counts.keys.sorted {
        let left = counts[$0] ?? 0
        let right = counts[$1] ?? 0
        return left == right ? $0 < $1 : left > right
      }.map { "\($0) \(counts[$0] ?? 0) 条" }.joined(separator: " · ")
    }
  }
}
