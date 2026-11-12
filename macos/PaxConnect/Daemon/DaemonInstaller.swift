import CryptoKit
import Foundation
import PaxCore
import Security

/// Validates the bundled executable and fingerprint before process launch.
@MainActor
final class DaemonInstaller {
  static let plistName = "dev.moore.pax-connect.daemon.plist"
  static var expectedVersion: String? {
    guard let url = Bundle.main.url(forResource: "daemon-version", withExtension: "txt") else {
      return nil
    }
    return try? String(contentsOf: url, encoding: .utf8).trimmingCharacters(
      in: .whitespacesAndNewlines)
  }

  static var fingerprint: String? {
    guard let url = Bundle.main.url(forResource: "daemon-fingerprint", withExtension: "txt"),
      let value = try? String(contentsOf: url, encoding: .utf8).trimmingCharacters(
        in: .whitespacesAndNewlines),
      value.count == 64, value.allSatisfy({ $0.isHexDigit })
    else { return nil }
    return value
  }

  static func validate() -> ClientError? { validateBundle() }

  private static func validateBundle() -> ClientError? {
    let contents = Bundle.main.bundleURL.appendingPathComponent("Contents")
    guard
      FileManager.default.isExecutableFile(
        atPath: contents.appendingPathComponent("MacOS/pax-connectd").path),
      FileManager.default.fileExists(
        atPath: contents.appendingPathComponent("Library/LaunchDaemons/\(plistName)").path)
    else { return .installationInvalid }
    guard let binary = try? Data(contentsOf: contents.appendingPathComponent("MacOS/pax-connectd")),
      let plist = try? Data(
        contentsOf: contents.appendingPathComponent("Library/LaunchDaemons/\(plistName)")),
      let fingerprint
    else { return .installationInvalid }
    var hash = SHA256()
    hash.update(data: binary)
    hash.update(data: plist)
    let actual = hash.finalize().map { String(format: "%02x", $0) }.joined()
    guard actual == fingerprint else { return .installationInvalid }
    guard let core = try? Data(contentsOf: contents.appendingPathComponent("MacOS/pax-core")),
      let expectedCore = try? String(
        contentsOf: contents.appendingPathComponent("Resources/core-fingerprint.txt"),
        encoding: .utf8
      )
      .trimmingCharacters(in: .whitespacesAndNewlines),
      SHA256.hash(data: core).map({ String(format: "%02x", $0) }).joined() == expectedCore
    else { return .installationInvalid }
    var code: SecStaticCode?
    guard SecStaticCodeCreateWithPath(Bundle.main.bundleURL as CFURL, [], &code) == errSecSuccess,
      let code,
      SecStaticCodeCheckValidity(
        code, SecCSFlags(rawValue: kSecCSCheckAllArchitectures | kSecCSCheckNestedCode), nil)
        == errSecSuccess
    else { return .installationInvalid }
    return nil
  }
}
