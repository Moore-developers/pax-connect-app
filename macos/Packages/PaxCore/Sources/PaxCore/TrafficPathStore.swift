import Foundation
import Observation

/// Diagnostic snapshots expire on dismissal; refresh errors never masquerade as
/// a current result. Closing the panel cancels observation, not the connection.
@MainActor @Observable
public final class TrafficPathStore {
  public private(set) var report: TrafficPathDiagnostics?
  public private(set) var busy = false
  public private(set) var error: ClientError?
  public let network: NetworkDiagnosticStore?
  private let load: @Sendable () async throws -> TrafficPathDiagnostics
  private let recheck: (@Sendable () async throws -> TrafficPathDiagnostics)?
  private var generation: UInt64 = 0
  public init(
    network: NetworkDiagnosticStore? = nil,
    load: @escaping @Sendable () async throws -> TrafficPathDiagnostics,
    recheck: (@Sendable () async throws -> TrafficPathDiagnostics)? = nil
  ) {
    self.network = network
    self.load = load
    self.recheck = recheck
  }
  public func refresh(recheck: Bool = false) async {
    guard !busy else { return }
    generation &+= 1
    let current = generation
    busy = true
    report = nil
    error = nil
    defer { if current == generation { busy = false } }
    do {
      let result = try await (recheck ? (self.recheck ?? load) : load)()
      guard current == generation, !Task.isCancelled else { return }
      report = result
    } catch {
      guard current == generation, !Task.isCancelled else { return }
      self.error = (error as? ClientError) ?? .unavailable
    }
  }
  public func clear() {
    generation &+= 1
    report = nil
    error = nil
    busy = false
  }
}
