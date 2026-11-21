import Observation

public enum AppSessionFailure: Error, Equatable, Sendable {
  case passwordRequired, authenticationFailed, invalidBundle, legacyService, anotherSession
  case launchFailed, shutdownFailed, residueUnknown, cleanupFailed, recoveryStorageUnavailable
  public var message: String { message(context: .chinese) }
  public func message(context: PresentationContext) -> String {
    switch self {
    case .passwordRequired: context.text("app_session.failure.password_required")
    case .authenticationFailed: context.text("app_session.failure.authentication_failed")
    case .invalidBundle: context.text("app_session.failure.invalid_bundle")
    case .legacyService: context.text("app_session.failure.legacy_service")
    case .anotherSession: context.text("app_session.failure.another_session")
    case .residueUnknown: context.text("app_session.failure.residue_unknown")
    case .cleanupFailed: context.text("app_session.failure.cleanup_failed")
    case .recoveryStorageUnavailable:
      context.text("app_session.failure.recovery_storage_unavailable")
    case .launchFailed: context.text("app_session.failure.launch_failed")
    case .shutdownFailed: context.text("app_session.failure.shutdown_failed")
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
