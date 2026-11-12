import Observation

public enum AppSessionFailure: Error, Equatable, Sendable {
  case passwordRequired, authenticationFailed, invalidBundle, legacyService, anotherSession
  case launchFailed, shutdownFailed, residueUnknown, cleanupFailed, recoveryStorageUnavailable
  public var message: String {
    switch self {
    case .passwordRequired: "启动后台需要管理员授权"
    case .authenticationFailed: "密码未通过，或当前账户没有管理员权限"
    case .invalidBundle: "应用文件校验失败，请重新安装 Pax Connect"
    case .legacyService: "旧版后台服务仍已登记，请先停用旧版后台后重试"
    case .anotherSession: "另一个 Pax Connect 实例正在使用后台，请先退出它"
    case .residueUnknown: "无法确认旧 Pax 资源归属，已暂停启动；请重试或查看诊断"
    case .cleanupFailed: "旧 Pax 后台或网络资源尚未释放，已暂停启动，请稍后重试"
    case .recoveryStorageUnavailable: "后台恢复存储不可用，启动已暂停；请更新 App 后重试，无需反复输入密码"
    case .launchFailed: "后台未能启动，请重试"
    case .shutdownFailed: "后台尚未确认关闭，App 已保留运行，请重试退出"
    }
  }
}

/// Owns authorization and shutdown state without retaining credentials or touching the system.
@MainActor @Observable
public final class AppSessionStore {
  public enum State: Equatable { case stopped, starting, needsPassword, running, stopping }
  public private(set) var state: State = .stopped
  public private(set) var failure: AppSessionFailure?
  private let launch: @MainActor (String?) async throws -> Void
  private let shutdown: @MainActor () async throws -> Void
  public init(
    start: @escaping @MainActor (String?) async throws -> Void,
    stop: @escaping @MainActor () async throws -> Void
  ) {
    launch = start
    shutdown = stop
  }
  public func start(password: String? = nil) async {
    guard state != .starting && state != .stopping && state != .running else { return }
    state = .starting
    failure = nil
    do {
      try await launch(password)
      state = .running
    } catch {
      failure = error as? AppSessionFailure ?? .launchFailed
      state =
        failure == .passwordRequired || failure == .authenticationFailed ? .needsPassword : .stopped
    }
  }
  public func markUnavailable() {
    guard state == .running else { return }
    state = .stopped
    failure = .launchFailed
  }
  public func stop() async {
    guard state != .starting && state != .stopping else { return }
    state = .stopping
    failure = nil
    do {
      try await shutdown()
      state = .stopped
    } catch {
      failure = .shutdownFailed
      state = .running
    }
  }
}
