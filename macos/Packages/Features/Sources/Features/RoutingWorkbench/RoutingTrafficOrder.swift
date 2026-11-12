import Foundation

extension RoutingWorkbenchStore {
  /// 示例数据的排序规则，与后台一致：最近活动从新到旧；同一时间按上传＋下载降序，再按 id。
  /// 活动时间取窗口内最近的一次传输、连接开始或结束。
  static func newestFirst(lower: Date, now: Date) -> (TrafficRow, TrafficRow) -> Bool {
    { a, b in
      let left = lastActive(a, lower: lower, now: now)
      let right = lastActive(b, lower: lower, now: now)
      if left != right { return left > right }
      if a.total != b.total { return a.total > b.total }
      return a.id < b.id
    }
  }
  static func lastActive(_ row: TrafficRow, lower: Date, now: Date) -> Date {
    var latest = Date.distantPast
    for connection in row.connections {
      let times =
        connection.transfers.map(\.at) + [connection.started] + [connection.ended].compactMap { $0 }
      for time in times where time > lower && time <= now && time > latest { latest = time }
    }
    return latest
  }
}
