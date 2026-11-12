import Observation

/// 系统批准与协议连接是两个不同事实。
public enum BackgroundServiceState: Sendable, Equatable {
  case notRegistered, requiresApproval, enabled, notFound, unknown
}

/// 仅 App 的 ServiceManagement 适配器触及系统；测试可替换边界。
@MainActor
public protocol BackgroundService: AnyObject {
  var state: BackgroundServiceState { get }
  func register() throws
  func unregister() async throws
  func openSettings()
}

/// 安装引导状态；Features 只读取本 Store，系统操作通过意图执行。
@MainActor
@Observable
public final class DaemonSetupStore {
  public private(set) var state: BackgroundServiceState = .notRegistered
  public private(set) var error: ClientError?
  public private(set) var busy = false
  public private(set) var updating = false
  private var updateAttempted = false
  private let service: any BackgroundService
  private let expectedDaemonVersion: @MainActor () -> String?
  private let fingerprint: @MainActor () -> String?
  private let registeredFingerprint: @MainActor () -> String?
  private let saveFingerprint: @MainActor (String) -> Void
  private let beforeUpdate: @MainActor () async throws -> Void
  private let validate: @MainActor () -> ClientError?

  public init(
    service: any BackgroundService, validate: @escaping @MainActor () -> ClientError?,
    fingerprint: @escaping @MainActor () -> String? = { nil },
    expectedDaemonVersion: @escaping @MainActor () -> String? = { nil },
    registeredFingerprint: @escaping @MainActor () -> String? = { nil },
    saveFingerprint: @escaping @MainActor (String) -> Void = { _ in },
    beforeUpdate: @escaping @MainActor () async throws -> Void = {}
  ) {
    self.service = service
    self.validate = validate
    self.expectedDaemonVersion = expectedDaemonVersion
    self.fingerprint = fingerprint
    self.registeredFingerprint = registeredFingerprint
    self.saveFingerprint = saveFingerprint
    self.beforeUpdate = beforeUpdate
  }

  public var canConnect: Bool {
    state == .enabled && (error == nil || error == .proxyMustBeOff) && !busy
  }
  public var message: String {
    if let error { return error.message }
    if updating { return "正在更新后台服务" }
    if busy { return "正在登记后台服务" }
    switch state {
    case .notRegistered: return "启用后台服务后，由 macOS 管理连接服务"
    case .requiresApproval: return "请在系统设置中允许 Pax Connect 后台服务"
    case .enabled: return "后台服务已获批准"
    case .notFound: return "尚未登记后台服务，点击启用开始设置"
    case .unknown: return "无法确认后台服务状态，请重新检查"
    }
  }

  public func refresh() {
    guard !busy else { return }
    let validation = validate()
    state = service.state
    if let validation {
      error = validation
    } else if (state == .enabled || state == .requiresApproval) && error != .updateFailed
      && error != .serviceIdentityUnknown && error != .proxyMustBeOff
    {
      error = nil
    } else if error == .installationRequired || error == .installationInvalid {
      error = nil
    }
    if state == .unknown && error == nil { error = .unknown }
  }

  public func enable() async {
    guard !busy else { return }
    if let failure = validate() {
      error = failure
      return
    }
    if error == .updateFailed || error == .serviceIdentityUnknown {
      updateAttempted = false
      await reconcileUpdate(force: true)
      return
    }
    if service.state == .enabled || service.state == .requiresApproval {
      refresh()
      return
    }
    busy = true
    defer { busy = false }
    var failure: ClientError?
    do { try service.register() } catch { failure = .registrationFailed }
    state = service.state
    error = state == .enabled || state == .requiresApproval ? nil : failure ?? .registrationFailed
    if error == nil, let value = fingerprint() { saveFingerprint(value) }
  }

  /// Changed bundle content triggers one serial unregister/register transaction.
  public func reconcileUpdate(force: Bool = false) async {
    guard !busy, !updateAttempted, let current = fingerprint() else { return }
    guard force || (registeredFingerprint() != nil && registeredFingerprint() != current) else {
      return
    }
    if let failure = validate() {
      error = failure
      return
    }
    state = service.state
    updateAttempted = true
    busy = true
    updating = true
    error = nil
    defer {
      busy = false
      updating = false
    }
    do { try await beforeUpdate() } catch {
      self.error = error as? ClientError ?? .updateFailed
      updateAttempted = false
      return
    }
    guard !Task.isCancelled else {
      error = .updateFailed
      return
    }
    do {
      do { try await service.unregister() } catch {
        if service.state != .notRegistered && service.state != .notFound { throw error }
      }
      guard service.state == .notRegistered || service.state == .notFound else {
        throw ClientError.updateFailed
      }
      try Task.checkCancellation()
      do { try service.register() } catch {
        if service.state != .enabled && service.state != .requiresApproval { throw error }
      }
      state = service.state
      guard state == .enabled || state == .requiresApproval else { throw ClientError.updateFailed }
      saveFingerprint(current)
    } catch {
      state = service.state
      self.error = .updateFailed
    }
  }

  /// An unknown registration is adopted only after the running binary proves its identity.
  public func confirmIdentity(_ handshake: DaemonHandshake) async {
    guard !busy, let current = fingerprint() else { return }
    if handshake.serviceFingerprint == current {
      saveFingerprint(current)
      return
    }
    if handshake.serviceFingerprint.isEmpty {
      guard let expected = expectedDaemonVersion(), expected != handshake.daemonVersion else {
        error = .serviceIdentityUnknown
        return
      }
    }
    await reconcileUpdate(force: true)
  }

  public func openSettings() { service.openSettings() }
}
