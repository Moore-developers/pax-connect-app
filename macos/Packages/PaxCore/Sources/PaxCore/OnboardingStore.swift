import Foundation
import Observation

/// Only guidance preferences are persisted. Product state remains owned by its existing stores.
@MainActor @Observable public final class OnboardingStore {
  public enum Path: String, Codable, Sendable { case welcome, network, models }
  public enum NetworkStep: Equatable, Sendable {
    case addRoute, selectRoute, connect, waiting, complete
  }
  private struct Preferences: Codable {
    var path: Path = .welcome
    var hidden = false
    var networkCompleted = false
    var modelsCompleted = false
  }
  private var preferences: Preferences?
  private let defaults: UserDefaults
  private let key: String
  public var path: Path? { preferences?.hidden == false ? preferences?.path : nil }
  public var networkCompleted: Bool { preferences?.networkCompleted == true }
  public var modelsCompleted: Bool { preferences?.modelsCompleted == true }
  public init(defaults: UserDefaults = .standard, key: String = "pax.first-run.v1") {
    self.defaults = defaults
    self.key = key
    if let data = defaults.data(forKey: key) {
      preferences = try? JSONDecoder().decode(Preferences.self, from: data)
    }
  }
  /// Called once a compatible daemon has supplied its pre-startup identity.
  public func identify(initialInstall: Bool) {
    guard preferences == nil else { return }
    preferences = Preferences(hidden: !initialInstall)
    persist()
  }
  public func reopen() {
    if preferences == nil { preferences = Preferences() }
    preferences?.hidden = false
    preferences?.path = .welcome
    persist()
  }
  public func choose(_ path: Path) {
    if preferences == nil { preferences = Preferences() }
    preferences?.path = path
    preferences?.hidden = false
    persist()
  }
  public func dismiss() {
    preferences?.hidden = true
    persist()
  }
  public func recordNetwork(connected: Bool, hasRecords: Bool) {
    guard preferences != nil, connected, hasRecords, !networkCompleted else { return }
    preferences?.networkCompleted = true
    persist()
  }
  public func recordModels(hasRecords: Bool) {
    guard preferences != nil, hasRecords, !modelsCompleted else { return }
    preferences?.modelsCompleted = true
    persist()
  }
  public func networkStep(hasRoutes: Bool, hasSelection: Bool, connected: Bool) -> NetworkStep {
    if networkCompleted { return .complete }
    if !hasRoutes { return .addRoute }
    if !hasSelection { return .selectRoute }
    return connected ? .waiting : .connect
  }
  private func persist() {
    guard let preferences, let data = try? JSONEncoder().encode(preferences) else { return }
    defaults.set(data, forKey: key)
  }
}
