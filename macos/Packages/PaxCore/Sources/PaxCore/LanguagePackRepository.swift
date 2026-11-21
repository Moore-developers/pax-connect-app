import Darwin
import Foundation

public struct InstalledLanguagePack: Sendable {
  public let exists: Bool
  public let candidate: LanguagePackCandidate?
  public let problem: String?
}

/// Only this owner accesses installed packs. It never writes business settings or the app bundle.
public actor LanguagePackRepository {
  private let directory: URL
  public let catalog: LanguageCatalog
  public init(directory: URL, catalog: LanguageCatalog) {
    self.directory = directory
    self.catalog = catalog
  }
  private func file(_ language: InterfaceLanguage) -> URL {
    directory.appendingPathComponent(language.rawValue + ".paxlang")
  }
  public func load(_ language: InterfaceLanguage) -> InstalledLanguagePack {
    let url = file(language)
    do {
      try rejectSymlink(directory)
      var info = stat()
      if lstat(url.path, &info) != 0 {
        if errno == ENOENT {
          return InstalledLanguagePack(exists: false, candidate: nil, problem: nil)
        }
        throw POSIXError(.init(rawValue: errno) ?? .EIO)
      }
      let data = try Self.readFile(url)
      let pack = try LanguagePack.decode(data)
      guard pack.locale == language else { throw LanguagePackFailure("installed_locale_mismatch") }
      let candidate = try catalog.candidate(pack, tolerateChangedEntries: true)
      return InstalledLanguagePack(
        exists: true, candidate: candidate,
        problem: candidate.ignoredKeys.isEmpty ? nil : "pack_entries_outdated")
    } catch {
      return InstalledLanguagePack(
        exists: true, candidate: nil, problem: "installed_pack_unreadable")
    }
  }
  public func preview(_ source: URL) throws -> LanguagePackCandidate {
    try catalog.candidate(Self.readFile(source))
  }
  public func install(_ candidate: LanguagePackCandidate) throws -> InstalledLanguagePack {
    try prepareDirectory()
    let target = file(candidate.pack.locale)
    try rejectSymlink(target)
    let data = try candidate.pack.encoded()
    let temporary = directory.appendingPathComponent(".import-" + UUID().uuidString)
    let descriptor = open(temporary.path, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW, 0o600)
    guard descriptor >= 0 else { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
    let handle = FileHandle(fileDescriptor: descriptor, closeOnDealloc: true)
    defer {
      try? handle.close()
      try? FileManager.default.removeItem(at: temporary)
    }
    try handle.write(contentsOf: data)
    try handle.synchronize()
    guard rename(temporary.path, target.path) == 0 else {
      throw POSIXError(.init(rawValue: errno) ?? .EIO)
    }
    return InstalledLanguagePack(exists: true, candidate: candidate, problem: nil)
  }
  public func restore(_ language: InterfaceLanguage) throws {
    try rejectSymlink(directory)
    let target = file(language)
    try rejectSymlink(target)
    do { try FileManager.default.removeItem(at: target) } catch let error as CocoaError
      where error.code == .fileNoSuchFile
    { return }
  }
  private func prepareDirectory() throws {
    try rejectSymlink(directory)
    try FileManager.default.createDirectory(
      at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
    try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: directory.path)
  }
  private func rejectSymlink(_ url: URL) throws {
    var info = stat()
    if lstat(url.path, &info) == 0, info.st_mode & S_IFMT == S_IFLNK {
      throw LanguagePackFailure("pack_path_is_symlink")
    }
  }
  private static func readFile(_ url: URL) throws -> Data {
    let descriptor = open(url.path, O_RDONLY | O_NOFOLLOW)
    guard descriptor >= 0 else { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
    let handle = FileHandle(fileDescriptor: descriptor, closeOnDealloc: true)
    defer { try? handle.close() }
    var info = stat()
    guard fstat(descriptor, &info) == 0, info.st_mode & S_IFMT == S_IFREG,
      info.st_size <= 4 * 1024 * 1024
    else { throw LanguagePackFailure("invalid_pack_file") }
    let data = try handle.read(upToCount: 4 * 1024 * 1024 + 1) ?? Data()
    guard data.count <= 4 * 1024 * 1024 else { throw LanguagePackFailure("pack_too_large") }
    return data
  }
}
