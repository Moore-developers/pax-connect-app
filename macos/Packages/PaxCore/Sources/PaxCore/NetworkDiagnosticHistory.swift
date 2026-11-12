import Foundation

/// A single private, local snapshot; browser session credentials are never encoded.
@MainActor public final class NetworkDiagnosticHistory {
  struct EnvironmentSnapshot: Codable {
    let report: EnvironmentDiagnosticReport
    let previous: EnvironmentDiagnosticReport?
    let local: NetworkDiagnosticEnvironment?
  }
  struct RouteSnapshot: Codable {
    let report: NetworkDiagnosticReport?
    let services: NetworkDiagnosticReport?
    let previous: NetworkDiagnosticReport?
    let environment: NetworkDiagnosticEnvironment?
  }
  private struct Archive: Codable {
    var version = 1
    var environment: EnvironmentSnapshot?
    var route: RouteSnapshot?
  }
  private static let maximumBytes = 2 * 1024 * 1024
  private let url: URL
  private var archive = Archive()
  private var readFailed = false
  public private(set) var message: String?
  var environment: EnvironmentSnapshot? { archive.environment }
  var route: RouteSnapshot? { archive.route }

  public init(url: URL) {
    self.url = url
    do {
      let values = try url.resourceValues(forKeys: [.fileSizeKey])
      guard let size = values.fileSize, size <= Self.maximumBytes else {
        throw CocoaError(.fileReadTooLarge)
      }
      let loaded = try JSONDecoder().decode(Archive.self, from: Data(contentsOf: url))
      guard loaded.version == 1 else { throw CocoaError(.fileReadCorruptFile) }
      archive = loaded
    } catch let error as CocoaError where error.code == .fileReadNoSuchFile {
      return
    } catch {
      readFailed = true
      message = "上次诊断记录无法读取，原文件已保留"
    }
  }

  func saveEnvironment(
    _ report: EnvironmentDiagnosticReport, local: NetworkDiagnosticEnvironment?,
    previous: EnvironmentDiagnosticReport? = nil
  ) {
    archive.environment = EnvironmentSnapshot(report: report, previous: previous, local: local)
    save()
  }

  func saveRoute(
    report: NetworkDiagnosticReport?, services: NetworkDiagnosticReport?,
    environment: NetworkDiagnosticEnvironment?, previous: NetworkDiagnosticReport? = nil
  ) {
    archive.route = RouteSnapshot(
      report: report, services: services, previous: previous, environment: environment)
    save()
  }

  private func save() {
    guard !readFailed else { return }
    do {
      let data = try JSONEncoder().encode(archive)
      guard data.count <= Self.maximumBytes else { throw CocoaError(.fileWriteOutOfSpace) }
      let directory = url.deletingLastPathComponent()
      try FileManager.default.createDirectory(
        at: directory, withIntermediateDirectories: true,
        attributes: [.posixPermissions: 0o700])
      try FileManager.default.setAttributes(
        [.posixPermissions: 0o700], ofItemAtPath: directory.path)
      try data.write(to: url, options: .atomic)
      try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
      message = nil
    } catch {
      message = "检测完成，但上次诊断记录保存失败"
    }
  }
}
