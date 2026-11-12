import AppKit
import OSLog
import PaxCore

private let backendLog = Logger(subsystem: "dev.moore.PaxConnect", category: "backend-recovery")

extension AppDelegate {
  /// Startup and explicit/automatic recovery share one task and one command transport.
  func recoverBackend(automatic: Bool = false) {
    guard terminationTask == nil, startupTask == nil else { return }
    if automatic {
      guard automaticBackendRecoveryAvailable else {
        stopBackendConnection(.unavailable)
        return
      }
      automaticBackendRecoveryAvailable = false
    }
    stableBackendTask?.cancel()
    let previous = connectionTask
    stopBackendConnection(nil)
    store.setBackendRecoveryInProgress(true)
    startupTask = Task { [weak self] in
      guard let self else { return }
      let started = ContinuousClock.now
      defer { self.startupTask = nil }
      await previous?.value
      do {
        try Task.checkCancellation()
        let path: String
        if let debugSocket = self.debugSocket {
          path = debugSocket
        } else {
          guard DaemonInstaller.validate() == nil, let fingerprint = DaemonInstaller.fingerprint
          else { throw ClientError.installationInvalid }
          path = try await self.launcher.start(fingerprint: fingerprint)
        }
        try Task.checkCancellation()
        self.paxdSocket = path
        await self.commandConnection.useSocket(path)
        if self.windowController == nil { self.showLiveWindow() }
        self.connectBackend()
        backendLog.info(
          "connection_started automatic=\(automatic) elapsed=\(String(describing: started.duration(to: .now)), privacy: .public)"
        )
      } catch {
        guard !Task.isCancelled else { return }
        let failure = error as? ClientError ?? .unknown
        self.publishBackend(.failed(failure))
        self.store.setBackendRecoveryInProgress(false)
        if self.windowController == nil { self.showLiveWindow() }
        backendLog.error(
          "recovery_failed automatic=\(automatic) reason=\(String(describing: failure), privacy: .public)"
        )
      }
    }
  }

  func backendExited(_ status: Int32) {
    guard terminationTask == nil else { return }
    backendLog.error("child_exited status=\(status)")
    // A launch that exits before Hello is handled by that launch's error path.
    guard startupTask == nil else { return }
    recoverBackend(automatic: true)
  }

  func stopBackendConnection(_ error: ClientError?) {
    stableBackendTask?.cancel()
    connectionGeneration &+= 1
    connectionTask?.cancel()
    publishBackend(error.map(ConnectionUpdate.failed) ?? .disconnected)
    store.setBackendRecoveryInProgress(false)
  }

  func publishBackend(_ update: ConnectionUpdate) {
    store.consume(update)
    telemetry.consume(update)
    aiUsageStore.consume(update)
    aiProfilesStore.consume(update)
    switch update {
    case .retrying, .failed, .disconnected:
      authorizationTask?.cancel()
      authorizationController?.dismiss(nil)
      authorizationController = nil
    case .connecting, .ready, .events: break
    }
    refresh.consume(update)
  }

  private func connectBackend() {
    let token = connectionGeneration
    let session = ConnectionSession(transport: commandConnection)
    connectionTask = Task { [weak self] in
      await session.run { [weak self] update in
        await self?.consumeBackend(update, generation: token)
      }
    }
  }

  private func consumeBackend(_ update: ConnectionUpdate, generation: UInt64) {
    guard connectionGeneration == generation, terminationTask == nil else { return }
    publishBackend(update)
    switch update {
    case .ready:
      store.setBackendRecoveryInProgress(false)
      stableBackendTask?.cancel()
      stableBackendTask = Task { [weak self] in
        do { try await Task.sleep(for: .seconds(60)) } catch { return }
        guard let self, self.connectionGeneration == generation,
          self.store.connection == .connected
        else { return }
        self.automaticBackendRecoveryAvailable = true
      }
      backendLog.info("connection_ready")
    case .retrying, .failed, .disconnected:
      stableBackendTask?.cancel()
      store.setBackendRecoveryInProgress(false)
    case .connecting, .events: break
    }
  }
}
