import Foundation
import Observation
import PaxClient

/// Shared presentation of daemon-owned profiles; native configuration and credentials remain in Go.
@MainActor @Observable
public final class AIProfilesStore {
  public private(set) var workspace = AIProfilesWorkspace()
  public private(set) var loading = false
  public private(set) var error: String?
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
      error = nil
      available = true
    } catch {
      guard token == generation, !Task.isCancelled else { return }
      self.error = Self.message(error)
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
      error = "后台连接已断开，请恢复后台后重试"
      return false
    }
    guard !loading, !mutating else {
      error = "配置正在更新，请稍后重试"
      return false
    }
    generation += 1
    let token = generation
    mutating = true
    loading = true
    error = nil
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
      self.error = Self.message(error)
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
      error = "配置服务未连接"
      return false
    }
    guard !p.name.isEmpty, p.kind == "native" || URL(string: p.endpoint)?.host != nil,
      p.multiplier.isFinite,
      (0...100).contains(p.multiplier),
      !workspace.profiles.contains(where: { $0.id != p.id && $0.name == p.name })
    else {
      error = "请填写有效名称、地址和倍率"
      return false
    }
    if let existing = workspace.profiles.first(where: { $0.id == p.id }),
      existing.revision != p.revision
    {
      error = "配置已更新，请刷新后重试"
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
    error = nil
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
      error = "此 Profile 仍在使用，请先切换"
      return false
    }
    workspace.profiles.removeAll { $0.id == p.id }
    error = nil
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
      error = "后台连接已断开，请恢复后台后重试"
      return nil
    }
    guard cli != .harness, let connection, let binding = workspace.bindings[cli] else {
      error = "当前客户端配置不可用，请重新检查"
      return nil
    }
    guard !loading, !mutating else {
      error = "配置正在更新，请稍后重试"
      return nil
    }
    error = nil
    let token = backendGeneration
    do {
      let plan = try await connection.planAIProfile(
        cli, profile: profile, binding: binding, restore: restore, initialize: initialize,
        modelID: modelID)
      guard token == backendGeneration, backendConnected else {
        self.error = "后台连接已变更，请重新检查后重试"
        return nil
      }
      return plan
    } catch {
      guard token == backendGeneration, backendConnected else {
        self.error = "后台连接已变更，请重新检查后重试"
        return nil
      }
      self.error = Self.message(error)
      return nil
    }
  }
  public func apply(_ plan: AIProfilePlan) async -> Bool {
    guard plan.cli != .harness, let connection else { return false }
    let success = await commit { try await connection.applyAIProfile(plan) }
    if !success {
      let message = error
      await refresh()
      error = message
    }
    return success
  }
  public func previewBind(_ cli: AIClient, profile: AIProfile) {
    guard cli != .harness, connection == nil, available else { return }
    workspace.bindings[cli] = .init(profileID: profile.id, state: "applied")
  }
}
