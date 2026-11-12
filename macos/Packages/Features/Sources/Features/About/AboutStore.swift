import Foundation
import Observation
import PaxCore

/// 关于页只读随包元数据与后台握手，不持久化或改变网络状态。
@MainActor @Observable
final class AboutStore {
  static let licenses: [(name: String, detail: String, license: String)] = [
    ("sing-box", "网络内核", "GPL v3"),
    ("domain-list-community", "域名规则来源", "MIT"),
    ("sing-geodata", "规则数据来源", "GPL v3 或后续版本"),
    ("GeoLite2 / MaxMind", "地理数据来源", "GeoLite2 数据条款"),
  ]
  let connection: ConnectionStore?
  let bundle: Bundle
  let applicationVersion: String
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
    let version =
      bundle.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "未知"
    let build = bundle.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "未知"
    applicationVersion = "\(version)（构建 \(build)）"
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
  var runningDaemon: String {
    preview ? "演示模式" : connected ? (connection?.daemonVersion ?? "未知") : "未连接"
  }
  private static func resource(_ name: String, bundle: Bundle) -> String {
    guard let url = bundle.url(forResource: name, withExtension: "txt"),
      let value = try? String(contentsOf: url, encoding: .utf8)
        .trimmingCharacters(in: .whitespacesAndNewlines), !value.isEmpty
    else { return "随包版本信息不可用" }
    return value
  }
}
