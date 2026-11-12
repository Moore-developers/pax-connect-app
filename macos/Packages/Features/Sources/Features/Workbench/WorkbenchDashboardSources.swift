import Foundation
import PaxClient
import PaxCore

/// 概览读取的两个独立数据源，由 App 装配；Features 不持有连接，测试可注入替身。
public struct DashboardSources: Sendable {
  /// 某线路的自动延迟历史（`metric = latency_auto`，最近 120 条，未过滤）。
  public var autoLatency: @Sendable (String) async throws -> [RouteHistorySample]
  /// 区间内的 AI 用量小时桶（`QueryAIUsageBuckets`），App 端再按天 / 模型 / CLI 聚合。
  public var aiUsage: @Sendable (Date, Date) async throws -> AIUsageBucketPage

  public init(
    autoLatency: @escaping @Sendable (String) async throws -> [RouteHistorySample],
    aiUsage: @escaping @Sendable (Date, Date) async throws -> AIUsageBucketPage
  ) {
    self.autoLatency = autoLatency
    self.aiUsage = aiUsage
  }

  public init(connection: DaemonConnection) {
    self.init(
      autoLatency: { id in
        try await Self.collectLatest(DashboardRouteProjection.capacity) { cursor in
          try await connection.routeHistory(
            id, metric: DashboardRouteProjection.metric, cursor: cursor)
        }
      },
      aiUsage: { from, to in
        try await connection.aiUsageBuckets(from: from, to: to, granularity: "hour", revision: nil)
      })
  }

  /// 单页上限 100 而自动延迟保留 120 条：按游标翻页取到 `limit` 条（daemon 按时间倒序）。
  /// 翻页期间历史版本变化会使游标作废，此时整窗重拉一次，仍失败才抛出。
  static func collectLatest(
    _ limit: Int, page: (String) async throws -> RouteHistoryPage
  ) async throws -> [RouteHistorySample] {
    func run() async throws -> [RouteHistorySample] {
      var items: [RouteHistorySample] = []
      var cursor = ""
      repeat {
        let next = try await page(cursor)
        items += next.items
        cursor = next.nextCursor
      } while !cursor.isEmpty && items.count < limit
      return Array(items.prefix(limit))
    }
    do { return try await run() } catch is CancellationError { throw CancellationError() } catch {
      return try await run()
    }
  }
}
