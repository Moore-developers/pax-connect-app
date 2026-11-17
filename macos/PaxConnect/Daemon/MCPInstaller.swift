import Features
import Foundation

/// App-owned installation I/O. Features receives only an explicit action.
@MainActor
enum MCPInstaller {
  static func accessActions() -> MCPAccessActions {
    MCPAccessActions { action, client, allow, expected in
      guard DaemonInstaller.validate() == nil else { throw Failure.invalidInstallation }
      let bundle = Bundle.main.bundleURL.path
      return try await Task.detached {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: bundle)
          .appendingPathComponent("Contents/MacOS/pax-mcp-launcher")
        process.arguments = [
          "--clients", action, "--app-bundle", bundle, "--client", client,
          "--allow", allow, "--expected-state", expected,
        ]
        let output = Pipe()
        process.standardOutput = output
        process.standardError = FileHandle.nullDevice
        try process.run()
        let data = output.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()
        guard process.terminationStatus == 0 else { throw Failure.invalidInstallation }
        return try JSONDecoder().decode(MCPAccessReport.self, from: data)
      }.value
    }
  }

  static func refresh() async {
    do {
      _ = try await run(refresh: true)
    } catch {
      // A failed refresh stays visible when the user opens the installation action.
      NSLog("MCP entry refresh failed; install/repair it in Settings.")
    }
  }

  private static func run(refresh: Bool) async throws -> String {
    guard DaemonInstaller.validate() == nil else { throw Failure.invalidInstallation }
    let bundle = Bundle.main.bundleURL.path
    return try await Task.detached {
      let process = Process()
      process.executableURL = URL(fileURLWithPath: bundle)
        .appendingPathComponent("Contents/MacOS/pax-mcp-launcher")
      process.arguments = [refresh ? "--refresh" : "--install", "--app-bundle", bundle]
      let output = Pipe()
      process.standardOutput = output
      process.standardError = FileHandle.nullDevice
      try process.run()
      let data = output.fileHandleForReading.readDataToEndOfFile()
      process.waitUntilExit()
      guard process.terminationStatus == 0 else { throw Failure.invalidInstallation }
      return String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
    }.value
  }

  private enum Failure: Error { case invalidInstallation }
}
