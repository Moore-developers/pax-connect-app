import Foundation

/// 已审核、随 App 打包的公开品牌映射；来源 URL 仅供归档，不由运行时下载。
struct ObjectIconCatalog: Decodable {
  struct Asset: Decodable { let file: String }
  var assets: [String: Asset] = [:]
  var applications: [String: String] = [:]
  var names: [String: String] = [:]
  var commands: [String: String] = [:]
  var domains: [String: String] = [:]
  var processSymbols: [String: String] = [:]
  static func resourceURL(_ name: String) -> URL? {
    Bundle.module.url(forResource: name, withExtension: "png", subdirectory: "ObjectIcons/brands")
  }
  static func bundled() -> Self {
    guard
      let url = Bundle.module.url(
        forResource: "catalog", withExtension: "json", subdirectory: "ObjectIcons"),
      let data = try? Data(contentsOf: url), data.count <= 256 * 1024,
      let catalog = try? JSONDecoder().decode(Self.self, from: data), catalog.assets.count <= 64
    else { return Self() }
    return catalog
  }
  func asset(kind: WorkbenchObjectIcons.Kind, id: String, name: String) -> String? {
    switch kind {
    case .application:
      return applications[id]
        ?? Self.commandKeys(id: id, name: name).lazy.compactMap { commands[$0] }.first
        ?? names[name.lowercased()]
    case .domain:
      let host = name.lowercased().trimmingCharacters(in: CharacterSet(charactersIn: "."))
      return domains.keys.sorted { $0.count > $1.count }.first {
        host == $0 || host.hasSuffix("." + $0)
      }.flatMap { domains[$0] }
    case .ip: return nil
    }
  }
  func symbol(id: String, name: String) -> String? {
    return Self.commandKeys(id: id, name: name).lazy.compactMap { processSymbols[$0] }.first
      ?? ((id.hasPrefix("process:") || id.hasPrefix("/")) ? "terminal" : nil)
  }
  /// 路径末段（如版本号文件名）之外，也尝试守护进程给出的展示名，使别名可匹配。
  private static func commandKeys(id: String, name: String) -> [String] {
    let path = id.hasPrefix("process:") ? String(id.dropFirst(8)) : id
    guard path.hasPrefix("/") else { return [name] }
    return [URL(fileURLWithPath: path).lastPathComponent, name, name.lowercased()]
  }
}
