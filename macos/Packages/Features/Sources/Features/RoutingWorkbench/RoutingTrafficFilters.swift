import Foundation

extension RoutingWorkbenchStore {
  enum Dimension: Int, CaseIterable {
    case application, domain, system
    var title: String { ["应用", "域名", "系统与进程"][rawValue] }
  }
  enum Period: Int, CaseIterable {
    case day = 0
    case week = 1
    case month = 2
    case minute = 3
    case yesterday = 4
    static var allCases: [Period] { [.day, .yesterday, .week, .month] }
    var title: String { ["今日", "近7天", "近30天", "最近 1 分钟", "昨日"][rawValue] }
    var seconds: TimeInterval { [86_400.0, 604_800, 2_592_000, 60, 86_400][rawValue] }
    var window: String { ["today", "week", "month", "minute", "yesterday"][rawValue] }
  }
  enum Direction: Int, CaseIterable {
    case all, proxy, direct, unknown
    var title: String { ["全部", "代理", "直连", "未知去向"][rawValue] }
  }
}
