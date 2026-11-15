import CoreLocation
import Foundation
import OpenDirectory

/// Pure parsers take bytes and values so tests use fixtures; `current()` is the
/// only place that touches the real machine.
public enum LocationExposureScanner {
  public struct BrowserApp: Sendable, Equatable {
    public let name: String
    public let bundleID: String
    public init(name: String, bundleID: String) {
      self.name = name
      self.bundleID = bundleID
    }
  }

  public static func scan(
    clients: Data?, userUID: String?, systemEnabled: Bool?, browsers: [BrowserApp],
    displayName: (String) -> String? = { _ in nil }
  ) -> LocationExposure {
    let entries = userUID.flatMap { uid in clients.flatMap { parseClients($0, userUID: uid) } }
    let browserIDs = Set(browsers.map(\.bundleID))
    let browserRows = browsers.map { app in
      let auth = entries?.first { $0.bundleID == app.bundleID }?.authorization ?? .none
      return LocationExposure.Browser(
        name: app.name, bundleID: app.bundleID, authorization: auth)
    }
    var apps: [LocationExposure.App] = []
    var seen = Set<String>()
    for entry in entries ?? [] where !browserIDs.contains(entry.bundleID) {
      guard entry.authorization != .none else { continue }
      let key = entry.bundleID.isEmpty ? entry.path : entry.bundleID
      guard seen.insert(key).inserted else { continue }
      let name =
        displayName(entry.path)
        ?? (entry.path.isEmpty
          ? entry.bundleID
          : ((entry.path as NSString).lastPathComponent as NSString).deletingPathExtension)
      apps.append(
        .init(
          name: name, bundleID: entry.bundleID, path: entry.path,
          allowed: entry.authorization == .allowed))
    }
    apps.sort { ($0.name, $0.bundleID) < ($1.name, $1.bundleID) }
    return LocationExposure(
      systemEnabled: systemEnabled, authorizationsReadable: entries != nil, browsers: browserRows,
      apps: apps)
  }

  struct ClientEntry {
    let bundleID: String
    let path: String
    let authorization: LocationExposure.Authorization
  }

  /// Keys look like `<GeneratedUID>:i<bundle id>:` or `<GeneratedUID>:e/<path>`; `root:` and
  /// `:p/` framework entries, system services and locationd bundles are not apps.
  static func parseClients(_ data: Data, userUID: String) -> [ClientEntry]? {
    guard
      let root = try? PropertyListSerialization.propertyList(from: data, format: nil)
        as? [String: Any]
    else { return nil }
    let prefix = userUID + ":"
    var result: [ClientEntry] = []
    for (key, value) in root where key.hasPrefix(prefix) {
      let rest = key.dropFirst(prefix.count)
      guard !rest.hasPrefix("p/"), let dict = value as? [String: Any] else { continue }
      if dict["isSystemService"] as? Bool == true { continue }
      var bundleID = dict["BundleId"] as? String ?? ""
      if bundleID.isEmpty, rest.hasPrefix("i") {
        bundleID = String(rest.dropFirst().prefix { $0 != ":" })
      }
      if bundleID.hasPrefix("com.apple.locationd.bundle-") { continue }
      var path = dict["BundlePath"] as? String ?? ""
      if path.isEmpty, rest.hasPrefix("e/") { path = String(rest.dropFirst(1)) }
      let auth: LocationExposure.Authorization =
        (dict["Authorized"] as? Bool).map { $0 ? .allowed : .denied } ?? .none
      result.append(.init(bundleID: bundleID, path: path, authorization: auth))
    }
    return result
  }

  // MARK: real machine

  /// Blocking file and system reads: call off the main thread.
  public static func current() -> LocationExposure {
    let clients = try? Data(contentsOf: URL(filePath: "/var/db/locationd/clients.plist"))
    return scan(
      clients: clients, userUID: currentUserGeneratedUID(),
      systemEnabled: CLLocationManager.locationServicesEnabled(), browsers: httpsBrowsers(),
      displayName: { $0.isEmpty ? nil : FileManager.default.displayName(atPath: $0) })
  }

  /// OpenDirectory returns the user's GeneratedUID without shelling out.
  static func currentUserGeneratedUID() -> String? {
    guard let pw = getpwuid(getuid()) else { return nil }
    let name = String(cString: pw.pointee.pw_name)
    guard
      let node = try? ODNode(
        session: ODSession.default(), type: ODNodeType(kODNodeTypeAuthentication)),
      let record = try? node.record(
        withRecordType: kODRecordTypeUsers, name: name, attributes: [kODAttributeTypeGUID]),
      let values = try? record.values(forAttribute: kODAttributeTypeGUID) as? [String]
    else { return nil }
    return values.first
  }

  @available(macOS, deprecated: 10.15)
  static func httpsBrowsers() -> [BrowserApp] {
    guard let url = URL(string: "https://example.com"),
      let urls = LSCopyApplicationURLsForURL(url as CFURL, .all)?.takeRetainedValue()
        as? [URL]
    else { return [] }
    var seen = Set<String>()
    return urls.compactMap { appURL in
      guard let bundle = Bundle(url: appURL), let id = bundle.bundleIdentifier,
        seen.insert(id).inserted
      else { return nil }
      return BrowserApp(
        name: FileManager.default.displayName(atPath: appURL.path)
          .replacingOccurrences(of: ".app", with: ""),
        bundleID: id)
    }
  }
}
