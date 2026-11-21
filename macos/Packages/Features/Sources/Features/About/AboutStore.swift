import Foundation
import Observation
import PaxCore

/// 关于页只读随包元数据与后台握手，不持久化或改变网络状态。
@MainActor @Observable
final class AboutStore {
  static let licenses: [(name: String, detail: String, license: String)] = [
    ("sing-box", "about.kernel", "GPL v3"),
    ("domain-list-community", "about.domain_rules", "MIT"),
    ("sing-geodata", "about.geodata_rules", "about.gpl_later"),
    ("GeoLite2 / MaxMind", "about.geographic_data", "about.geolite_terms"),
  ]
  let connection: ConnectionStore?
  let bundle: Bundle
  let version: String
  let build: String
  let bundledDaemon: String
  let kernel: String
  let system: String
  let architecture: String
  let preview: Bool

  init(
    connection: ConnectionStore? = nil, preview: Bool = false,
    bundle: Bundle = .main
  ) {
    self.connection = connection
    self.preview = preview
    self.bundle = bundle
    version =
      bundle.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? ""
    build = bundle.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? ""
    bundledDaemon = Self.resource("daemon-version", bundle: bundle)
    kernel = Self.resource("kernel-version", bundle: bundle)
    let os = ProcessInfo.processInfo.operatingSystemVersion
    system = "macOS \(os.majorVersion).\(os.minorVersion).\(os.patchVersion)"
    #if arch(arm64)
      architecture = "arm64"
    #else
      architecture = "x86_64"
    #endif
  }
  var connected: Bool { connection?.connection == .connected }
  func runningDaemon(context: PresentationContext) -> String {
    preview
      ? context.text("about.demo")
      : connected
        ? (connection?.daemonVersion ?? context.text("common.unknown"))
        : context.text("about.disconnected")
  }
  private static func resource(_ name: String, bundle: Bundle) -> String {
    guard let url = bundle.url(forResource: name, withExtension: "txt"),
      let value = try? String(contentsOf: url, encoding: .utf8)
        .trimmingCharacters(in: .whitespacesAndNewlines), !value.isEmpty
    else { return "" }
    return value
  }
}
