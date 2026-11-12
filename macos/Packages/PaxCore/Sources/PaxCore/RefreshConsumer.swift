import Foundation

/// A page-owned asynchronous reader. The gate admits work; this owner merges flights.
@MainActor final class RefreshConsumer {
  let domains: Set<RefreshDomain>
  private let fetch: @MainActor () async -> Void
  private var task: Task<Void, Never>?
  private var identity: UInt64 = 0
  private var pending = false
  private var pendingBypass = false
  private var active = false
  private var gate: RefreshGate?

  init(
    domains: Set<RefreshDomain>, minInterval: TimeInterval, clock: RefreshClock,
    fetch: @escaping @MainActor () async -> Void
  ) {
    self.domains = domains
    self.fetch = fetch
    gate = RefreshGate(minInterval: minInterval, clock: clock) { [weak self] in self?.start() }
  }
  func markDirty(bypass: Bool, restart: Bool = false) {
    if restart { cancel() }
    if task != nil {
      pending = true
      pendingBypass = pendingBypass || bypass
      return
    }
    gate?.markDirty(bypass: bypass)
  }
  func setActive(_ value: Bool) {
    guard active != value else { return }
    active = value
    if !value {
      let unfinished = task != nil || pending
      cancel()
      gate?.setActive(false)
      if unfinished { gate?.markDirty(bypass: true) }
    } else {
      gate?.setActive(true)
    }
  }
  private func cancel() {
    identity &+= 1
    task?.cancel()
    task = nil
    pending = false
    pendingBypass = false
  }
  private func start() {
    guard active else { return }
    guard task == nil else {
      pending = true
      return
    }
    identity &+= 1
    let request = identity
    task = Task { [weak self] in
      guard let self else { return }
      await fetch()
      guard request == identity else { return }
      task = nil
      if pending {
        pending = false
        let bypass = pendingBypass
        pendingBypass = false
        gate?.markDirty(bypass: bypass)
      }
    }
  }
}
