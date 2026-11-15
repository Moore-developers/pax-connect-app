import Foundation

/// Read-only snapshot of where this Mac can still expose location. It never
/// asks for authorization and never changes a system setting.
public struct LocationExposure: Sendable, Equatable, Codable {
  public enum Authorization: String, Sendable, Equatable, Codable { case allowed, denied, none }
  public enum Setting: String, Sendable, Equatable, Codable { case block, ask, allow }

  public struct Browser: Sendable, Equatable, Codable {
    public let name: String
    public let bundleID: String
    public let authorization: Authorization
    public init(name: String, bundleID: String, authorization: Authorization) {
      self.name = name
      self.bundleID = bundleID
      self.authorization = authorization
    }
  }

  public struct App: Sendable, Equatable, Codable {
    public let name: String
    public let bundleID: String
    public let path: String
    public let allowed: Bool
    public init(name: String, bundleID: String, path: String, allowed: Bool) {
      self.name = name
      self.bundleID = bundleID
      self.path = path
      self.allowed = allowed
    }
  }

  /// nil: the global switch could not be read.
  public let systemEnabled: Bool?
  /// false when clients.plist or the current user's identity could not be read.
  public let authorizationsReadable: Bool
  public let browsers: [Browser]
  public let apps: [App]

  public init(
    systemEnabled: Bool?, authorizationsReadable: Bool, browsers: [Browser], apps: [App]
  ) {
    self.systemEnabled = systemEnabled
    self.authorizationsReadable = authorizationsReadable
    self.browsers = browsers
    self.apps = apps
  }

  public enum State: Sendable, Equatable { case open, closed }

  /// Browser verdict: system location authorization only. Site settings inside the
  /// browser are not readable under macOS App Data protection.
  public static func state(_ browser: Browser) -> State {
    browser.authorization == .allowed ? .open : .closed
  }
}
