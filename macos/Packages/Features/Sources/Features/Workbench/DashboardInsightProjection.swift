import Foundation
import PaxCore
import PaxUI

/// 行动卡、「需要关注」「最近变化」的投影。沿用 Snapshot 的问题归并（`ConnectionFeedback`）与
/// 最近日志（problems / operations 视图），不新增 RPC。
enum DashboardInsightProjection {
  /// 行动卡种类；多个条件同时成立时只取最高优先级：无线路 > 通道检查失败 > 需系统授权。
  enum Action: Equatable { case addRoute, channelFailed, authorize }

  static func action(hasRoutes: Bool, channelFailed: Bool, needsAuthorization: Bool) -> Action? {
    if !hasRoutes { return .addRoute }
    if channelFailed { return .channelFailed }
    if needsAuthorization { return .authorize }
    return nil
  }

  static func actionModel(_ action: Action) -> PaxActionModel {
    switch action {
    case .addRoute:
      return .init(
        id: "add-route", symbol: "plus.circle", tone: .info, title: "还没有线路",
        detail: "添加线路后即可开启代理", primary: "添加线路")
    case .channelFailed:
      return .init(
        id: "channel-failed", symbol: "exclamationmark.triangle", tone: .error,
        title: "当前通道检查失败", detail: "无法通过当前线路访问网络，请重新验证或诊断", primary: "线路诊断",
        secondary: "重新验证")
    case .authorize:
      return .init(
        id: "authorize", symbol: "lock.shield", tone: .warning, title: "启动代理需要系统授权",
        detail: "授权后才能接管系统网络", primary: "授权连接")
    }
  }

  static let limit = PaxTheme.Dashboard.insightRows
  private static let errorFeedback: Set<String> = [
    "proxy", "connection", "selection", "ingress", "current-path", "degraded",
  ]

  /// 今日未解决问题：先放实时问题归并（订阅更新失败、线路测量未完成…），再用今日问题日志补足，至多 3 项。
  static func attention(
    feedback: [ConnectionFeedback], problems: [ProductLog], calendar: Calendar = .current
  ) -> [PaxInsightItem] {
    var items = feedback.map { issue -> PaxInsightItem in
      let failed = errorFeedback.contains(issue.id) || issue.id.hasPrefix("degraded")
      return PaxInsightItem(
        id: "feedback:" + issue.id, symbol: failed ? "exclamationmark.triangle" : "clock",
        title: issue.title, detail: issue.detail, badge: failed ? "错误" : "待处理",
        badgeTone: failed ? .error : .warning)
    }
    for log in problems.sorted(by: { $0.at > $1.at }) {
      guard !items.contains(where: { $0.title == log.message }) else { continue }
      let failed = log.level == .error
      var detail = DashboardFormat.clock(log.lastAt ?? log.at, calendar: calendar)
      if log.occurrences > 1 { detail += " · \(log.occurrences) 次" }
      if !log.routeName.isEmpty { detail += " · " + log.routeName }
      items.append(
        PaxInsightItem(
          id: "problem:" + (log.problemID.isEmpty ? log.recordID : log.problemID),
          symbol: failed ? "exclamationmark.triangle" : "clock", title: log.message, detail: detail,
          badge: failed ? "错误" : "待处理", badgeTone: failed ? .error : .warning))
    }
    return Array(items.prefix(limit))
  }

  /// Pax 操作与线路 / 订阅事件，最新在前，至多 3 项。错误事件不被成功事件挤掉：
  /// 前 3 项里没有错误而更早有错误时，用最近的错误替换最旧的一项。
  static func changes(operations: [ProductLog], calendar: Calendar = .current) -> [PaxInsightItem] {
    let sorted = operations.sorted { $0.at > $1.at }
    var picked = Array(sorted.prefix(limit))
    if !picked.contains(where: { $0.level == .error }),
      let error = sorted.first(where: { $0.level == .error }), !picked.isEmpty
    {
      picked[picked.count - 1] = error
      picked.sort { $0.at > $1.at }
    }
    return picked.map { log in
      let (detail, tone): (String, PaxToneBadge.Tone) =
        switch log.level {
        case .error: ("失败", .error)
        case .warning: ("需注意", .warning)
        default: ("成功", .success)
        }
      return PaxInsightItem(
        id: "change:"
          + (log.recordID.isEmpty
            ? log.operationID + "\(log.at.timeIntervalSince1970)" : log.recordID),
        time: DashboardFormat.clock(log.at, calendar: calendar), title: log.message, detail: detail,
        detailTone: tone)
    }
  }
}
