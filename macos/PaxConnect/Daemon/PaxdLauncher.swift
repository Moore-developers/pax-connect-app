import Darwin
import Foundation
import PaxCore

/// Process I/O stays in the App layer; lifecycle tests replace the child and Hello.
@MainActor
protocol PaxdChild: AnyObject {
  var isRunning: Bool { get }
  var onExit: (@Sendable (Int32) -> Void)? { get set }
  func terminate()
  func kill()
}

@MainActor
private final class PaxdProcess: PaxdChild {
  let process: Process
  init(executable: URL, arguments: [String], output: Any = FileHandle.nullDevice) throws {
    process = Process()
    process.executableURL = executable
    process.arguments = arguments
    process.standardInput = FileHandle.nullDevice
    process.standardOutput = output
    process.standardError = FileHandle.nullDevice
    try process.run()
  }
  var onExit: (@Sendable (Int32) -> Void)? {
    didSet {
      let callback = onExit
      process.terminationHandler = { callback?($0.terminationStatus) }
    }
  }
  var isRunning: Bool { process.isRunning }
  func terminate() { if isRunning { process.terminate() } }
  func kill() { if isRunning { _ = Darwin.kill(process.processIdentifier, SIGKILL) } }
}

/// Owns an unprivileged bundled paxd. No sudo, launchctl, password or root receipt.
@MainActor
final class PaxdLauncher {
  typealias Spawn = ([String]) throws -> any PaxdChild
  private let ownerPID: Int32
  private let resolve: () async throws -> String
  private let spawn: Spawn
  private let hello: (String) async throws -> DaemonHandshake
  private let pause: () async throws -> Void
  private let now: () -> ContinuousClock.Instant
  private var child: (any PaxdChild)?
  private(set) var socketPath: String?
  var onUnexpectedExit: ((Int32) -> Void)?
  var isRunning: Bool { child?.isRunning == true }
  private var launchGeneration: UInt64 = 0
  private var stopping = false

  init(bundleURL: URL, ownerPID: Int32) {
    let executable = bundleURL.appendingPathComponent("Contents/MacOS/pax-connectd")
    self.ownerPID = ownerPID
    resolve = { try await Self.resolveSocket(executable) }
    spawn = { try PaxdProcess(executable: executable, arguments: $0) }
    hello = { try await DaemonConnection(socketPath: $0).handshake() }
    pause = { try await Task.sleep(for: .milliseconds(100)) }
    now = { ContinuousClock.now }
  }

  init(
    ownerPID: Int32, resolve: @escaping () async throws -> String,
    spawn: @escaping Spawn, hello: @escaping (String) async throws -> DaemonHandshake,
    pause: @escaping () async throws -> Void, now: @escaping () -> ContinuousClock.Instant
  ) {
    self.ownerPID = ownerPID
    self.resolve = resolve
    self.spawn = spawn
    self.hello = hello
    self.pause = pause
    self.now = now
  }

  static func accepts(_ handshake: DaemonHandshake, fingerprint: String?) -> Bool {
    guard let fingerprint, !fingerprint.isEmpty else { return false }
    return handshake.capabilities.contains("paxd.user")
      && handshake.serviceFingerprint == fingerprint
  }

  func start(fingerprint: String) async throws -> String {
    if isRunning, let socketPath { return socketPath }
    child = nil
    let path = try await resolve()
    try Task.checkCancellation()
    stopping = false
    launchGeneration &+= 1
    let token = launchGeneration
    let process = try spawn(["--user", "--owner-pid", String(ownerPID), "--socket", path])
    child = process
    socketPath = path
    process.onExit = { [weak self] code in
      Task { @MainActor [weak self] in
        guard let self, self.launchGeneration == token, !self.stopping else { return }
        self.onUnexpectedExit?(code)
      }
    }
    do {
      let deadline = now().advanced(by: .seconds(12))
      while now() < deadline {
        try Task.checkCancellation()
        guard process.isRunning else { throw ClientError.unavailable }
        // The real transport bounds each Hello to five seconds.
        do {
          let handshake = try await hello(path)
          guard Self.accepts(handshake, fingerprint: fingerprint),
            handshake.ownerAppPID == UInt32(ownerPID)
          else { throw ClientError.serviceIdentityUnknown }
          guard process.isRunning else { throw ClientError.unavailable }
          return path
        } catch let error as ClientError where !error.isRetryable {
          throw error
        } catch {
          try Task.checkCancellation()
        }
        try await pause()
      }
      throw ClientError.timeout
    } catch {
      try? await stop()
      throw error
    }
  }

  func stop() async throws {
    stopping = true
    launchGeneration &+= 1
    guard let child else { return }
    child.terminate()
    // SIGTERM enters the daemon's graceful shutdown; SIGKILL is the bounded fallback.
    for _ in 0..<40 {
      if !child.isRunning {
        self.child = nil
        return
      }
      try? await pause()
    }
    child.kill()
    for _ in 0..<10 {
      if !child.isRunning {
        self.child = nil
        return
      }
      try? await pause()
    }
    throw ClientError.unavailable
  }

  private static func resolveSocket(_ executable: URL) async throws -> String {
    // One canonical resolver lives in Go. This query does not start the daemon or touch stores.
    let pipe = Pipe()
    let query = try PaxdProcess(
      executable: executable, arguments: ["--print-user-socket"], output: pipe)
    defer {
      query.kill()
      try? pipe.fileHandleForReading.close()
      try? pipe.fileHandleForWriting.close()
    }
    for _ in 0..<30 {
      if !query.isRunning { break }
      try await Task.sleep(for: .milliseconds(100))
    }
    guard !query.isRunning, query.process.terminationStatus == 0 else { throw ClientError.timeout }
    try pipe.fileHandleForWriting.close()
    let data = pipe.fileHandleForReading.readData(ofLength: 4096)
    guard let path = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .newlines),
      path.hasPrefix("/"), !path.contains("\n"), path.utf8.count < 104
    else { throw ClientError.unavailable }
    return path
  }
}
