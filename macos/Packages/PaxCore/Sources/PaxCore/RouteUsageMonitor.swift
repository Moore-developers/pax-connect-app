import Foundation
import Observation
import PaxClient

/// One batch read for the route catalog; independent of the visible page.
@MainActor @Observable
public final class RouteUsageMonitor {
  public private(set) var overview: RouteUsagePage?
  public private(set) var error: String?
  public private(set) var connected = false
  private let query: (@Sendable (String, String) async throws -> RouteUsagePage)?
  private var task: Task<Void, Never>?
  private var generation: UInt64 = 0
  private var reading = false

  public init(
    query: (@Sendable (String, String) async throws -> RouteUsagePage)?
  ) {
    self.query = query
  }
  isolated deinit { task?.cancel() }

  public func setConnected(_ value: Bool) {
    guard connected != value else { return }
    connected = value
    generation &+= 1
    task?.cancel()
    task = nil
    reading = false
    guard value, query != nil else { return }
    task = Task { [weak self] in
      while !Task.isCancelled {
        await self?.refresh()
        do { try await Task.sleep(for: .seconds(30)) } catch { return }
      }
    }
  }

  public func refresh() async {
    guard connected, !reading, let query else { return }
    let current = generation
    reading = true
    defer { if current == generation { reading = false } }
    do {
      let value = try await query("", "today")
      guard !Task.isCancelled, connected, current == generation else { return }
      if let failure = value.error { throw RoutesStore.DocumentSaveError.rejected(failure) }
      overview = value
      error = nil

    } catch {
      guard !Task.isCancelled, connected, current == generation else { return }
      self.error = ProductErrorPresentation.display(error)
    }
  }

  public func summary(_ id: String) -> RouteUsageSummary? {
    guard let overview, connected, error == nil else { return nil }
    return overview.routes.first { $0.routeID == id } ?? .init(routeID: id)
  }
  public func breakdown(_ id: String) async throws -> RouteUsagePage {
    guard connected, let query else { throw RoutesStore.DocumentSaveError.unavailable }
    let current = generation
    let value = try await query(id, "today")
    guard !Task.isCancelled, connected, current == generation else { throw CancellationError() }
    if let failure = value.error { throw RoutesStore.DocumentSaveError.rejected(failure) }
    return value
  }
}
