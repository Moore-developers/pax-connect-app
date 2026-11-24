import Foundation
import Observation
import PaxClient

public enum TrafficAlertDeliveryFailure: Error { case denied }

/// Settings and notification delivery share the authoritative daemon projection.
/// Only successful system submissions enter the small per-episode delivery ledger.
@MainActor @Observable public final class TrafficAlertsStore {
  public private(set) var state: TrafficAlertState?
  public private(set) var connected = false
  public private(set) var supported = false
  public private(set) var busy = false
  public private(set) var error: (any Error)?
  public private(set) var notificationDenied = false
  public private(set) var notificationError: String?
  private let read: @Sendable () async throws -> TrafficAlertState
  private let write: @Sendable (TrafficAlertSettings) async throws -> TrafficAlertState
  private let authorize: @MainActor () async throws -> Bool
  private let deliver: @MainActor (TrafficAlertNotice) async throws -> Void
  private let persist: @MainActor ([String]) -> Void
  private var delivered: Set<String>
  private var delivery: Task<Void, Never>?
  private var deliveryGeneration: UInt64 = 0
  private var generation: UInt64 = 0

  public init(
    read: @escaping @Sendable () async throws -> TrafficAlertState,
    write: @escaping @Sendable (TrafficAlertSettings) async throws -> TrafficAlertState,
    authorize: @escaping @MainActor () async throws -> Bool = { true },
    deliver: @escaping @MainActor (TrafficAlertNotice) async throws -> Void = { _ in },
    deliveredIDs: [String] = [], persist: @escaping @MainActor ([String]) -> Void = { _ in }
  ) {
    self.read = read
    self.write = write
    self.authorize = authorize
    self.deliver = deliver
    self.delivered = Set(deliveredIDs)
    self.persist = persist
  }
  isolated deinit { delivery?.cancel() }

  public func consume(_ update: ConnectionUpdate) {
    switch update {
    case .ready(let handshake, let snapshot):
      connected = true
      supported = handshake.capabilities.contains("traffic.alerts.v1")
      state = nil
      if supported, let value = snapshot.trafficAlerts { apply(value) }
    case .events(let events):
      guard connected, supported else { return }
      for event in events {
        switch event {
        case .trafficAlerts(let value): apply(value)
        case .snapshot(let value): if let value = value.trafficAlerts { apply(value) }
        default: break
        }
      }
    case .connecting, .retrying, .failed, .disconnected:
      connected = false
      generation &+= 1
      cancelDelivery()
    }
  }
  private func apply(_ value: TrafficAlertState) {
    guard value.settings.revision >= (state?.settings.revision ?? 0) else { return }
    guard value.revision >= (state?.revision ?? 0) else { return }
    if state?.settings.revision != value.settings.revision || state?.notices != value.notices {
      cancelDelivery()
    }
    state = value
    let retained = delivered.intersection(value.activeNoticeIDs)
    if retained != delivered {
      delivered = retained
      persist(delivered.sorted())
    }
    scheduleDelivery()
  }

  public func refresh() async {
    guard connected, supported, !busy else { return }
    busy = true
    let token = generation
    defer { busy = false }
    do {
      let value = try await read()
      guard connected, token == generation else { return }
      error = nil
      apply(value)
    } catch { if token == generation { self.error = error } }
  }
  public func save(_ draft: TrafficAlertSettings) async -> Bool {
    guard connected, supported, !busy else { return false }
    busy = true
    let token = generation
    defer { busy = false }
    do {
      if draft.enabled { notificationDenied = try await !authorize() }
      guard connected, token == generation else { return false }
      let value = try await write(draft)
      guard connected, token == generation else { return false }
      error = nil
      // A Watch frame may already contain the reevaluated state at this revision.
      apply(value)
      scheduleDelivery()
      return true
    } catch {
      if token == generation { self.error = error }
      return false
    }
  }
  public func setNotificationPermission(_ allowed: Bool) {
    notificationDenied = !allowed
    if allowed {
      notificationError = nil
      scheduleDelivery()
    }
  }
  private func scheduleDelivery() {
    guard delivery == nil, connected, supported, state?.settings.enabled == true else { return }
    let token = generation
    let owner = deliveryGeneration
    delivery = Task { [weak self] in
      guard let self else { return }
      defer { if owner == deliveryGeneration { self.delivery = nil } }
      while !Task.isCancelled, connected, token == generation,
        let notice = state?.notices.first(where: { !delivered.contains($0.id) })
      {
        do {
          try await deliver(notice)
          if state?.activeNoticeIDs.contains(notice.id) == true {
            delivered.insert(notice.id)
            persist(delivered.sorted())
          }
          notificationDenied = false
          notificationError = nil
        } catch {
          if Task.isCancelled { return }
          if error is TrafficAlertDeliveryFailure {
            notificationDenied = true
          } else {
            notificationError = error.localizedDescription
          }
          return
        }
      }
    }
  }
  private func cancelDelivery() {
    deliveryGeneration &+= 1
    delivery?.cancel()
    delivery = nil
  }
}
