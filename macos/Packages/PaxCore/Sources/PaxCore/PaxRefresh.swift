import Foundation
import Observation
import PaxClient

/// Injectable time source: `now` for interval math, `after` for the deferred fetch.
public struct RefreshClock: Sendable {
  public var now: @Sendable () -> Date
  /// Runs `action` on the main actor after `delay`; the returned closure cancels it.
  public var after:
    @Sendable (TimeInterval, @escaping @MainActor @Sendable () -> Void) -> @Sendable () -> Void
  public init(
    now: @escaping @Sendable () -> Date,
    after:
      @escaping @Sendable (TimeInterval, @escaping @MainActor @Sendable () -> Void) ->
      @Sendable () -> Void
  ) {
    self.now = now
    self.after = after
  }
  public static let system = RefreshClock(
    now: { Date() },
    after: { delay, action in
      let task = Task {
        try? await Task.sleep(for: .seconds(delay))
        guard !Task.isCancelled else { return }
        await action()
      }
      return { task.cancel() }
    })
}

/// Data domains a page can follow; each has a (version, generation) signal.
public enum RefreshDomain: Hashable, Sendable, CaseIterable {
  case logs, traffic, trafficData, routeHistory, aiUsage, aiSessionCatalog
  /// Driven by the caller through `PaxRefresh.poke` (CF task in progress).
  case cfTask
}

/// `version` moves on any change; a changed `generation` invalidates held data and bypasses
/// the minimum interval. Route history has no generation: a version change reloads the window.
public struct RefreshSignal: Equatable, Sendable {
  public var version: UInt64
  public var generation: UInt64
  public init(version: UInt64, generation: UInt64 = 0) {
    self.version = version
    self.generation = generation
  }
}

public enum RefreshPage: String, CaseIterable, Hashable, Sendable {
  case dashboard, traffic, logs, aiOverview, aiSessions, aiLogs, aiProfiles, routeDetail, cfTask
}

/// The single refresh policy table (ticket 107).
public struct RefreshPolicy: Sendable {
  public let domains: Set<RefreshDomain>
  public let minInterval: TimeInterval
  /// `SetViewVisibility` id; nil for pages the daemon does not track.
  public let viewID: String?
  public static func of(_ page: RefreshPage) -> RefreshPolicy {
    switch page {
    case .dashboard:
      return .init(
        domains: [.traffic, .logs, .routeHistory, .aiUsage], minInterval: 10, viewID: "dashboard")
    case .traffic: return .init(domains: [.traffic, .logs], minInterval: 60, viewID: "traffic")
    case .logs: return .init(domains: [.logs], minInterval: 5, viewID: "logs")
    case .aiOverview: return .init(domains: [.aiUsage], minInterval: 5, viewID: "ai_overview")
    case .aiSessions:
      return .init(domains: [.aiUsage, .aiSessionCatalog], minInterval: 3, viewID: "ai_sessions")
    case .aiLogs: return .init(domains: [.aiUsage], minInterval: 10, viewID: "ai_logs")
    case .aiProfiles: return .init(domains: [], minInterval: 0, viewID: "ai_profiles")
    case .routeDetail: return .init(domains: [.traffic], minInterval: 60, viewID: nil)
    case .cfTask: return .init(domains: [.cfTask], minInterval: 1, viewID: nil)
    }
  }
}

/// Dirty flag + minimum interval + deferred fire for one consumer.
@MainActor public final class RefreshGate {
  private let minInterval: TimeInterval
  private let clock: RefreshClock
  private let fire: @MainActor () -> Void
  private var dirty = false
  private var bypass = false
  private var active = false
  private var last: Date?
  private var cancelTimer: (@Sendable () -> Void)?
  public init(
    minInterval: TimeInterval, clock: RefreshClock, fire: @escaping @MainActor () -> Void
  ) {
    self.minInterval = minInterval
    self.clock = clock
    self.fire = fire
  }
  public var isDirty: Bool { dirty }
  /// Records pending work; `bypass` skips the minimum interval (entry, invalidation).
  public func markDirty(bypass: Bool = false) {
    dirty = true
    self.bypass = self.bypass || bypass
    attempt()
  }
  /// Inactive gates only record dirt; becoming active fetches pending work at once.
  public func setActive(_ value: Bool) {
    guard active != value else { return }
    active = value
    if value {
      if dirty { bypass = true }
      attempt()
    } else {
      cancelTimer?()
      cancelTimer = nil
    }
  }
  private func attempt() {
    guard dirty, active else { return }
    let elapsed = last.map { clock.now().timeIntervalSince($0) } ?? .infinity
    let wait = bypass ? 0 : max(0, minInterval - elapsed)
    if wait <= 0 {
      cancelTimer?()
      cancelTimer = nil
      dirty = false
      bypass = false
      last = clock.now()
      fire()
    } else if cancelTimer == nil {
      cancelTimer = clock.after(wait) { [weak self] in
        self?.cancelTimer = nil
        self?.attempt()
      }
    }
  }
}

/// Drives page refreshes from Watch signals: visible pages only, hidden pages stay dirty,
/// and the visible set is reported to the daemon on every change and on every Watch first frame.
@MainActor public final class PaxRefresh {
  private let clock: RefreshClock
  private let report: @Sendable ([String]) async -> Void
  private let signals: @MainActor () -> [RefreshDomain: RefreshSignal]
  private var gates: [RefreshPage: RefreshGate] = [:]
  private var consumers: [RefreshPage: [String: RefreshConsumer]] = [:]
  private var shown: Set<RefreshPage> = []
  private var seen: [RefreshDomain: RefreshSignal] = [:]
  private var cfVersion: UInt64 = 0
  private var reportTail: Task<Void, Never>?
  public private(set) var connected = false
  public private(set) var windowVisible = true

  /// `signals` reads the current per-domain signals (usually `TelemetryStore.signals`).
  public init(
    clock: RefreshClock = .system,
    signals: @escaping @MainActor () -> [RefreshDomain: RefreshSignal],
    report: @escaping @Sendable ([String]) async -> Void
  ) {
    self.clock = clock
    self.signals = signals
    self.report = report
  }
  public convenience init(connection: DaemonConnection, telemetry: TelemetryStore) {
    self.init(
      signals: { telemetry.signals },
      report: { try? await connection.setViewVisibility($0) })
  }

  /// Registers the fetch for a page; it runs on entry, on signal changes and on return to
  /// visibility, never while the page is hidden. The fetch must not block.
  public func bind(_ page: RefreshPage, fetch: @escaping @MainActor () -> Void) {
    gates[page]?.setActive(false)
    gates[page] = RefreshGate(
      minInterval: RefreshPolicy.of(page).minInterval, clock: clock, fire: fetch)
    gates[page]?.setActive(isActive(page))
  }
  /// Independent consumers share page visibility and policy, each with its own interval and flight.
  public func bind(
    _ page: RefreshPage, consumer: String, domains: Set<RefreshDomain>,
    fetch: @escaping @MainActor () async -> Void
  ) {
    consumers[page]?[consumer]?.setActive(false)
    let value = RefreshConsumer(
      domains: domains, minInterval: RefreshPolicy.of(page).minInterval, clock: clock, fetch: fetch)
    consumers[page, default: [:]][consumer] = value
    value.setActive(isActive(page))
  }
  /// A changed query identity cancels old work; an ordinary update coalesces behind it.
  public func request(_ page: RefreshPage, consumer: String, restart: Bool = false) {
    consumers[page]?[consumer]?.markDirty(bypass: true, restart: restart)
  }
  /// `refreshOnEntry: false` resumes the same selected page after a window lifecycle;
  /// its cancelled/changed consumers remain dirty, without inventing new work.
  public func pageDidAppear(_ page: RefreshPage, refreshOnEntry: Bool = true) {
    guard shown.insert(page).inserted else { return }
    if refreshOnEntry {
      gates[page]?.markDirty(bypass: true)
      for consumer in consumers[page, default: [:]].values { consumer.markDirty(bypass: true) }
    }
    gates[page]?.setActive(isActive(page))
    for consumer in consumers[page, default: [:]].values { consumer.setActive(isActive(page)) }
    reportVisibility()
  }
  public func pageDidDisappear(_ page: RefreshPage) {
    guard shown.remove(page) != nil else { return }
    gates[page]?.setActive(false)
    for consumer in consumers[page, default: [:]].values { consumer.setActive(false) }
    reportVisibility()
  }
  public func setWindowVisible(_ value: Bool) {
    guard windowVisible != value else { return }
    windowVisible = value
    syncActivity()
    reportVisibility()
  }
  /// Feeds connection state and Watch frames; call after `TelemetryStore.consume`.
  public func consume(_ update: ConnectionUpdate) {
    switch update {
    case .ready:
      // First frame of every Watch (reconnects too): the daemon may have dropped the set.
      seen = signals()
      connected = true
      for page in shown {
        gates[page]?.markDirty(bypass: true)
        for consumer in consumers[page, default: [:]].values {
          consumer.markDirty(bypass: true, restart: true)
        }
      }
      syncActivity()
      reportVisibility()
    case .events:
      observe(signals())
    case .connecting, .retrying, .failed, .disconnected:
      guard connected else { return }
      connected = false
      seen = [:]
      syncActivity()
    }
  }
  /// Signals a caller-driven domain (CF task progress) that has new work.
  public func poke(_ domain: RefreshDomain) {
    cfVersion &+= 1
    seen[domain] = .init(version: cfVersion)
    mark(domain, bypass: false)
  }
  /// The ids the daemon is told are visible (empty while the window is hidden).
  public var visibleViewIDs: [String] {
    guard windowVisible else { return [] }
    return shown.compactMap { RefreshPolicy.of($0).viewID }.sorted()
  }

  private func isActive(_ page: RefreshPage) -> Bool {
    shown.contains(page) && windowVisible && connected
  }
  private func syncActivity() {
    for (page, gate) in gates { gate.setActive(isActive(page)) }
    for (page, values) in consumers {
      for consumer in values.values { consumer.setActive(isActive(page)) }
    }
  }
  private func observe(_ next: [RefreshDomain: RefreshSignal]) {
    for (domain, signal) in next where seen[domain] != signal {
      let old = seen[domain]
      seen[domain] = signal
      mark(domain, bypass: old.map { $0.generation != signal.generation } ?? false)
    }
  }
  private func mark(_ domain: RefreshDomain, bypass: Bool) {
    for values in consumers.values {
      for consumer in values.values where consumer.domains.contains(domain) {
        consumer.markDirty(bypass: bypass, restart: bypass)
      }
    }
    for page in RefreshPage.allCases where RefreshPolicy.of(page).domains.contains(domain) {
      gates[page]?.markDirty(bypass: bypass)
    }
  }
  private func reportVisibility() {
    let ids = visibleViewIDs
    let report = report
    let previous = reportTail
    reportTail = Task {
      await previous?.value
      await report(ids)
    }
  }
}
