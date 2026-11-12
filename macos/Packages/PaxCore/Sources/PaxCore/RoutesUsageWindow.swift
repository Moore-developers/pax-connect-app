import Foundation
import PaxClient

extension RoutesStore {
  public func loadRouteData(_ id: String, more: Bool = false) async {
    await loadMeasurementHistory(id, more: more)
    await refreshRouteUsage(id, more: more)
  }

  public func refreshRouteUsage(_ id: String, more: Bool = false) async {
    guard connected, capabilities.contains("routes.usage") else { return }
    await queryUsageWindow(id, more: more)
  }
  public func setUsagePeriod(_ id: String, period: String) async {
    usagePeriod[id] = period
    await refreshRouteUsage(id)
  }
  public func setUsageWindow(_ id: String, from: Date?) async {
    usageFrom[id] = from
    usagePages[id] = 1
    await refreshRouteUsage(id)
  }

  func queryUsageWindow(_ id: String, more: Bool) async {
    let generation = sessionGeneration
    let request = (usageQueryGeneration[id] ?? 0) + 1
    usageQueryGeneration[id] = request
    let requestedWindow = usageFrom[id]
    let period = usagePeriod[id]
    let count =
      requestedWindow == nil
      ? min(22, (usagePages[id] ?? 1) + (more && !(usageCursors[id] ?? "").isEmpty ? 1 : 0)) : 10
    var cursor = ""
    var items: [RouteUsageBucket] = []
    var latest: RouteUsagePage?
    do {
      for _ in 0..<count {
        let page: RouteUsagePage
        if let period, let periodUsageQuery {
          // The period read takes no cursor, so it is one complete aggregated page.
          // Re-issuing it would append the same buckets again.
          page = try await periodUsageQuery(id, period)
          guard !Task.isCancelled, connected, generation == sessionGeneration,
            usageQueryGeneration[id] == request
          else { return }
          if let error = page.error { throw DocumentSaveError.rejected(error) }
          items = page.items
          latest = page
          cursor = ""
          break
        } else {
          page = try await usageQuery(id, cursor, requestedWindow)
        }
        guard !Task.isCancelled, connected, generation == sessionGeneration,
          usageQueryGeneration[id] == request
        else {
          return
        }
        if let error = page.error { throw DocumentSaveError.rejected(error) }
        items += page.items
        latest = page
        cursor = page.nextCursor
        if cursor.isEmpty { break }
      }
      guard let page = latest else { return }
      commitUsageWindow(id, items: items, page: page, cursor: cursor, count: count)
      commitUsageError(id, nil)
    } catch {
      guard !Task.isCancelled, connected, generation == sessionGeneration,
        usageQueryGeneration[id] == request
      else {
        return
      }
      commitUsageError(id, error)
    }
  }
}
