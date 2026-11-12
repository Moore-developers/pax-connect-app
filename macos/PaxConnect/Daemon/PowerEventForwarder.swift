import Foundation

/// Delivers the latest App-lifetime device state until acknowledged. New events
/// cancel obsolete retries; terminating the App prevents delayed sends.
@MainActor
public final class PowerEventForwarder {
  private var sequence: UInt64 = 0
  private var stopped = false
  private var delivery: Task<Void, Never>?
  private let status: @MainActor (Bool) -> Void
  private let send: @MainActor (Bool, UInt64) async throws -> Void

  public init(
    status: @escaping @MainActor (Bool) -> Void,
    send: @escaping @MainActor (Bool, UInt64) async throws -> Void
  ) {
    self.status = status
    self.send = send
  }
  public func notify(sleeping: Bool) {
    guard !stopped else { return }
    sequence += 1
    let current = sequence
    delivery?.cancel()
    delivery = Task { [weak self] in
      while let self, !self.stopped, !Task.isCancelled, current == self.sequence {
        do {
          try await self.send(sleeping, current)
          if current == self.sequence { self.status(false) }
          return
        } catch {
          if Task.isCancelled || self.stopped || current != self.sequence { return }
          self.status(true)
          do { try await Task.sleep(for: .milliseconds(500)) } catch { return }
        }
      }
    }
  }
  public func stop() {
    stopped = true
    delivery?.cancel()
    delivery = nil
  }
}
