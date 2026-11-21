import Foundation
import Observation
import PaxClient

/// Shared presentation of daemon-owned profiles; native configuration and credentials remain in Go.
@MainActor @Observable
public final class AIProfilesStore {
  public private(set) var workspace = AIProfilesWorkspace()
  public private(set) var loading = false
  public private(set) var errorMeaning: ProductText?
  public var error: String? { error(context: .chinese) }
  public func error(context: PresentationContext) -> String? {
    errorMeaning.map { ProductTextPresentation.text($0, original: "—", context: context) }
  }
  public private(set) var available: Bool
  private let connection: DaemonConnection?
  private var backendConnected = true
  private var generation: UInt64 = 0
  private var backendGeneration: UInt64 = 0
  private var mutating = false
  private var refreshTask: Task<AIProfilesWorkspace, any Error>?
  public init(connection: DaemonConnection? = nil, preview: AIProfilesWorkspace? = nil) {
    self.connection = connection
    available = connection != nil || preview != nil
    if let preview { workspace = preview }
  }
  public func consume(_ update: ConnectionUpdate) {
    guard connection != nil else { return }
    switch update {
    case .ready:
      backendConnected = true
      available = true
    case .connecting, .retrying, .failed, .disconnected:
      backendGeneration += 1
      backendConnected = false
      available = false
      cancelRefresh()
    case .events: break
    }
  }
  public func refresh() async {
    guard let connection, backendConnected, !mutating else { return }
    let task: Task<AIProfilesWorkspace, any Error>
    if let current = refreshTask {
      task = current
    } else {
      generation += 1
      task = Task { try await connection.aiProfiles() }
      refreshTask = task
      loading = true
    }
    let token = generation
    defer {
      if token == generation {
        refreshTask = nil
        loading = false
      }
    }
    do {
      let result = try await task.value
      guard token == generation, !Task.isCancelled else { return }
      workspace = result
      errorMeaning = nil
      available = true
    } catch {
      guard token == generation, !Task.isCancelled else { return }
      self.errorMeaning = Self.failureMeaning(error)
      available = false
    }
  }
  public func cancelRefresh() {
    generation += 1
    refreshTask?.cancel()
    refreshTask = nil
    loading = mutating
  }
  private func commit(_ action: () async throws -> AIProfilesWorkspace) async -> Bool {
    guard backendConnected else {
      errorMeaning = ProductText(code: "profile_error.backend_disconnected")
      return false
    }
    guard !loading, !mutating else {
      errorMeaning = ProductText(code: "profile_error.updating")
      return false
    }
    generation += 1
    let token = generation
    mutating = true
    loading = true
    errorMeaning = nil
    defer {
      mutating = false
      loading = false
      if token != generation { Task { await self.refresh() } }
    }
    do {
      let result = try await action()
      guard token == generation else { return false }
      workspace = result
      return true
    } catch {
      guard token == generation else { return false }
      self.errorMeaning = Self.failureMeaning(error)
      return false
    }
  }
  public func save(_ p: AIProfile, key: String) async -> Bool {
    if let connection { return await commit { try await connection.saveAIProfile(p, key: key) } }
    return savePreview(p, key: key)
  }
  @discardableResult
  public func savePreview(_ p: AIProfile, key: String = "") -> Bool {
    guard connection == nil, available else {
      errorMeaning = ProductText(code: "profile_error.service_disconnected")
      return false
    }
    guard !p.name.isEmpty, p.kind == "native" || URL(string: p.endpoint)?.host != nil,
      p.multiplier.isFinite,
      (0...100).contains(p.multiplier),
      !workspace.profiles.contains(where: { $0.id != p.id && $0.name == p.name })
    else {
      errorMeaning = ProductText(code: "profile_error.invalid_values")
      return false
    }
    if let existing = workspace.profiles.first(where: { $0.id == p.id }),
      existing.revision != p.revision
    {
      errorMeaning = ProductText(code: "profile_error.ai_profile_conflict")
      return false
    }
    var value = p
    value.hasKey = p.hasKey || !key.isEmpty
    value.revision += 1
    if let i = workspace.profiles.firstIndex(where: { $0.id == p.id }) {
      workspace.profiles[i] = value
    } else {
      workspace.profiles.append(value)
    }
    errorMeaning = nil
    return true
  }
  public func delete(_ p: AIProfile) async -> Bool {
    if let connection { return await commit { try await connection.deleteAIProfile(p) } }
    return deletePreview(p)
  }
  @discardableResult
  public func deletePreview(_ p: AIProfile) -> Bool {
    guard connection == nil, available else { return false }
    guard !workspace.bindings.values.contains(where: { $0.profileID == p.id }) else {
      errorMeaning = ProductText(code: "profile_error.ai_profile_in_use")
      return false
    }
    workspace.profiles.removeAll { $0.id == p.id }
    errorMeaning = nil
    return true
  }
  public func importCurrent(
    _ cli: AIClient, name: String? = nil, multiplier: Double = 1, initialize: Bool = false
  ) async -> Bool {
    guard cli != .harness, let connection, let binding = workspace.bindings[cli] else {
      return false
    }
    return await commit {
      try await connection.importAIProfile(
        cli, inspection: binding.inspectionID,
        name: name ?? cli.rawValue + " · " + Date().formatted(date: .omitted, time: .standard),
        initialize: initialize, multiplier: multiplier)
    }
  }
  public func refreshMissingModels() async {
    guard connection != nil, !loading, !mutating else { return }
    let ids = workspace.profiles.filter { $0.kind != "native" && $0.modelsState.isEmpty }.map(\.id)
    for id in ids {
      guard !Task.isCancelled else { return }
      _ = await refreshModels(id)
    }
  }
  public func refreshModels(_ id: String) async -> Bool {
    guard let connection else { return false }
    return await commit { try await connection.refreshAIProfileModels(id) }
  }
  public func plan(
    _ cli: AIClient, profile: AIProfile?, restore: Bool = false, initialize: Bool = false,
    modelID: String = ""
  ) async
    -> AIProfilePlan?
  {
    guard backendConnected else {
      errorMeaning = ProductText(code: "profile_error.backend_disconnected")
      return nil
    }
    guard cli != .harness, let connection, let binding = workspace.bindings[cli] else {
      errorMeaning = ProductText(code: "profile_error.client_unavailable")
      return nil
    }
    guard !loading, !mutating else {
      errorMeaning = ProductText(code: "profile_error.updating")
      return nil
    }
    errorMeaning = nil
    let token = backendGeneration
    do {
      let plan = try await connection.planAIProfile(
        cli, profile: profile, binding: binding, restore: restore, initialize: initialize,
        modelID: modelID)
      guard token == backendGeneration, backendConnected else {
        self.errorMeaning = ProductText(code: "profile_error.backend_changed")
        return nil
      }
      return plan
    } catch {
      guard token == backendGeneration, backendConnected else {
        self.errorMeaning = ProductText(code: "profile_error.backend_changed")
        return nil
      }
      self.errorMeaning = Self.failureMeaning(error)
      return nil
    }
  }
  public func apply(_ plan: AIProfilePlan) async -> Bool {
    guard plan.cli != .harness, let connection else { return false }
    let success = await commit { try await connection.applyAIProfile(plan) }
    if !success {
      let meaning = errorMeaning
      await refresh()
      errorMeaning = meaning
    }
    return success
  }
  public func previewBind(_ cli: AIClient, profile: AIProfile) {
    guard cli != .harness, connection == nil, available else { return }
    workspace.bindings[cli] = .init(profileID: profile.id, state: "applied")
  }
}
