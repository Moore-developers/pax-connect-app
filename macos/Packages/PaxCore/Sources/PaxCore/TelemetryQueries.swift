import Foundation
import PaxClient

public enum TelemetryPage: Sendable { case dashboard, traffic, logs }

/// Private query lifetime shared by the three real history consumers.
@MainActor
final class TelemetryQueries {
  enum Key: Hashable {
    case logs(LogFilter)
    case objects(LogFilter)
    case traffic(TrafficFilter)
  }
  enum Value: Sendable {
    case logs(HistoryLogPage)
    case objects(HistoryObjectPage)
    case traffic(TrafficPage)
    var size: Int {
      switch self {
      case .logs(let p):
        let legacy = p.items.reduce(2048) { $0 + $1.message.utf8.count * 2 + 2048 }
        return p.records.reduce(legacy) { total, record in
          let flow =
            record.flow.map {
              [
                $0.key, $0.applicationID, $0.application, $0.process, $0.domain, $0.ip, $0.routeID,
                $0.routeName, $0.rule, $0.ruleSource, $0.sourceLabel, $0.sourceEvidence,
              ].reduce(512) { $0 + $1.utf8.count * 2 }
            } ?? 0
          return total + 2048 + flow + (record.systemEvent?.telemetryCacheBytes ?? 0)
            + [
              record.recordID, record.eventID, record.title, record.source, record.metrics.reason,
              record.systemCategory, record.categoryName, record.categorySymbol,
              record.operationFrom, record.operationTo,
            ]
            .reduce(0) { $0 + $1.utf8.count * 2 }
        }
      case .objects(let p):
        return p.items.reduce(2048) { $0 + ($1.name.utf8.count + $1.id.utf8.count) * 2 + 256 }
      case .traffic(let p):
        let rows = p.items + [p.unknownSource, p.dnsSummary].compactMap { $0 }
        return rows.reduce(p.trend.count * 128 + 2048) { total, row in
          total + 2048 + row.latencySamples.count * 128
            + row.exits.reduce(0) {
              $0 + 128 + ($1.routeID.utf8.count + $1.routeName.utf8.count + $1.direction.utf8.count)
                * 2
            }
            + row.recentResponses.reduce(0) { $0 + 128 + $1.connectionKey.utf8.count * 2 }
            + [
              row.id, row.objectID, row.name, row.routeID, row.routeName, row.direction, row.basis,
              row.sourceLabel, row.sourceEvidence,
            ].reduce(0) { $0 + $1.utf8.count * 2 }
        }
      }
    }
  }
  struct Flight {
    let id: UUID
    let task: Task<Value, any Error>
    var consumers: Set<UUID>
    var finished = false
  }
  struct Cached {
    let value: Value
    let at: Date
    var used: Date
    let ttl: TimeInterval
  }
  private var flights: [Key: Flight] = [:]
  private var cache: [Key: Cached] = [:]
  private(set) var requests = 0
  private(set) var cancellations = 0
  func load(
    _ key: Key, ttl: TimeInterval, force: Bool,
    request: @escaping @Sendable () async throws -> Value
  ) async throws -> Value {
    try Task.checkCancellation()
    let today: Bool
    if case .traffic(let filter) = key { today = filter.window == "today" } else { today = false }
    if !force, var saved = cache[key], Date().timeIntervalSince(saved.at) < saved.ttl,
      !today || Calendar.current.isDate(saved.at, inSameDayAs: Date())
    {
      saved.used = Date()
      cache[key] = saved
      return saved.value
    }
    let consumer = UUID()
    let id: UUID
    let task: Task<Value, any Error>
    if var current = flights[key] {
      current.consumers.insert(consumer)
      flights[key] = current
      id = current.id
      task = current.task
    } else {
      id = UUID()
      requests += 1
      task = Task {
        try Task.checkCancellation()
        return try await request()
      }
      flights[key] = Flight(id: id, task: task, consumers: [consumer])
    }
    return try await withTaskCancellationHandler {
      defer { release(key, consumer: consumer) }
      do {
        let value = try await task.value
        try Task.checkCancellation()
        guard flights[key]?.id == id else { throw CancellationError() }
        if flights[key]?.id == id {
          flights[key]?.finished = true
          if ttl > 0 {
            cache[key] = Cached(value: value, at: Date(), used: Date(), ttl: ttl)
            prune()
          }
        }
        return value
      } catch { throw error }
    } onCancel: {
      Task { @MainActor [weak self] in self?.release(key, consumer: consumer) }
    }
  }
  private func release(_ key: Key, consumer: UUID) {
    guard var current = flights[key], current.consumers.contains(consumer) else { return }
    current.consumers.remove(consumer)
    if current.consumers.isEmpty {
      if !current.finished {
        current.task.cancel()
        cancellations += 1
      }
      flights[key] = nil
    } else {
      flights[key] = current
    }
  }
  func invalidate(
    logs: Bool, traffic: Bool, trafficFacts: Bool = true, logsRemovedBefore: Date? = nil
  ) {
    for key in Set(cache.keys).union(flights.keys) {
      let remove: Bool
      switch key {
      case .logs(let q), .objects(let q):
        if let before = logsRemovedBefore, let from = q.from {
          remove = logs && from < before
        } else {
          remove = logs
        }
      case .traffic(let q):
        // This unpaginated overview query uses traffic.db only. Analysis still loses its
        // tokens and recent-response evidence on every log invalidation.
        let overview =
          q.dimension == "network" && q.window == "today"
          && q.readToken.isEmpty && q.cursor.isEmpty
        remove = overview ? trafficFacts : traffic
      }
      if remove {
        cache[key] = nil
        flights[key]?.task.cancel()
        flights[key] = nil
      }
    }
  }
  private func prune() {
    while cache.count > 8 || cache.values.reduce(0, { $0 + $1.value.size }) > 16 * 1024 * 1024 {
      guard let oldest = cache.min(by: { $0.value.used < $1.value.used })?.key else { return }
      cache[oldest] = nil
    }
  }
}
