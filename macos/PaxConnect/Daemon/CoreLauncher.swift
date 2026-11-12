import Darwin
import Foundation
import PaxCore

@MainActor
protocol CoreLaunchChild: AnyObject {
  var isRunning: Bool { get }
  var exitStatus: Int32? { get }
}

@MainActor
private final class CoreSudoProcess: CoreLaunchChild {
  let process = Process()
  init(executable: URL, ownerPID: Int32, password: String?) throws {
    if let password,
      password.utf8.count > 1024 || password.contains("\n") || password.contains("\r")
    {
      throw AppSessionFailure.authenticationFailed
    }
    process.executableURL = URL(fileURLWithPath: "/usr/bin/sudo")
    process.arguments =
      (password == nil ? ["-n"] : ["-S", "-p", ""])
      + [
        "--", executable.path, "--replace", "--owner-pid", String(ownerPID), "--owner-uid",
        String(getuid()),
        "--resources",
        executable.deletingLastPathComponent().deletingLastPathComponent()
          .appendingPathComponent("Resources/rulesets").path, "--mixed-port", "17890",
      ]
    let input = Pipe()
    process.standardInput = input
    process.standardOutput = FileHandle.nullDevice
    process.standardError = FileHandle.nullDevice
    try process.run()
    defer { try? input.fileHandleForWriting.close() }
    if let password {
      var data = Data((password + "\n").utf8)
      defer { data.resetBytes(in: data.startIndex..<data.endIndex) }
      try input.fileHandleForWriting.write(contentsOf: data)
    }
  }
  var isRunning: Bool { process.isRunning }
  var exitStatus: Int32? { process.isRunning ? nil : process.terminationStatus }
}

/// Starts the root core only on an explicit proxy ON continuation. Passwords
/// are passed directly to sudo stdin and never become launcher state.
@MainActor
final class CoreLauncher {
  private let spawn: (String?) throws -> any CoreLaunchChild
  private let check: () async -> Bool
  private let shutdown: () async -> Bool
  private let pause: () async throws -> Void
  private let now: () -> ContinuousClock.Instant
  private var child: (any CoreLaunchChild)?

  init(bundleURL: URL, ownerPID: Int32) {
    let executable = bundleURL.appendingPathComponent("Contents/MacOS/pax-core")
    let daemon = bundleURL.appendingPathComponent("Contents/MacOS/pax-connectd")
    spawn = { try CoreSudoProcess(executable: executable, ownerPID: ownerPID, password: $0) }
    check = { await Self.query(daemon, ownerPID: ownerPID, shutdown: false) }
    shutdown = { await Self.query(daemon, ownerPID: ownerPID, shutdown: true) }
    pause = { try await Task.sleep(for: .milliseconds(100)) }
    now = { ContinuousClock.now }
  }

  init(
    spawn: @escaping (String?) throws -> any CoreLaunchChild,
    check: @escaping () async -> Bool, pause: @escaping () async throws -> Void,
    now: @escaping () -> ContinuousClock.Instant
  ) {
    self.spawn = spawn
    self.check = check
    self.shutdown = { true }
    self.pause = pause
    self.now = now
  }

  nonisolated private static func query(_ daemon: URL, ownerPID: Int32, shutdown: Bool) async
    -> Bool
  {
    await Task.detached {
      let query = Process()
      query.executableURL = daemon
      query.arguments = [
        shutdown ? "--shutdown-core" : "--check-core", "--owner-pid", String(ownerPID),
      ]
      query.standardInput = FileHandle.nullDevice
      query.standardOutput = FileHandle.nullDevice
      query.standardError = FileHandle.nullDevice
      do {
        try query.run()
        query.waitUntilExit()
        return query.terminationStatus == 0
      } catch { return false }
    }.value
  }

  func stop() async throws {
    guard child != nil else { return }
    guard await shutdown() else { throw AppSessionFailure.shutdownFailed }
  }

  func start(password: String?) async throws {
    try Task.checkCancellation()
    if await check() { return }
    // Root --replace verifies its private owner/start-time record. A live but
    // unresponsive sudo child must not prevent that authenticated replacement.
    let launched = try spawn(password)
    child = launched
    let deadline = now().advanced(by: .seconds(15))
    while now() < deadline {
      try Task.checkCancellation()
      if await check() { return }
      if !launched.isRunning {
        child = nil
        if launched.exitStatus == 1 {
          throw password == nil ? AppSessionFailure.passwordRequired : .authenticationFailed
        }
        throw AppSessionFailure.launchFailed
      }
      try await pause()
    }
    throw AppSessionFailure.launchFailed
  }
}
