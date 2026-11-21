import Foundation

public enum InterfaceLanguage: String, Codable, Sendable, CaseIterable {
  case chinese = "zh-Hans"
  case english = "en"

  public var nativeName: String { self == .chinese ? "简体中文" : "English" }
  public static func resolve(_ preferences: [String]) -> Self {
    for tag in preferences {
      let base = tag.lowercased().replacingOccurrences(of: "_", with: "-").split(separator: "-")
        .first
      if base == "zh" { return .chinese }
      if base == "en" { return .english }
    }
    return .english
  }
}

public enum InterfaceLanguageChoice: String, Codable, Sendable, CaseIterable {
  case system
  case chinese = "zh-Hans"
  case english = "en"
  public var fixedTitle: String {
    switch self {
    case .system: "跟随系统 / System Default"
    case .chinese: "简体中文"
    case .english: "English"
    }
  }
}

public struct LanguagePackFailure: Error, LocalizedError, Sendable, Equatable {
  public let reason: String
  public let key: String
  public init(_ reason: String, key: String = "") {
    self.reason = reason
    self.key = key
  }
  public var errorDescription: String? { key.isEmpty ? reason : reason + ": " + key }
}

public enum LanguageMessage: Codable, Sendable, Equatable {
  case text(String)
  case plural(one: String?, other: String)

  public init(from decoder: any Decoder) throws {
    let c = try decoder.singleValueContainer()
    if let text = try? c.decode(String.self) {
      self = .text(text)
      return
    }
    let forms = try c.decode([String: String].self)
    guard let other = forms["other"], Set(forms.keys).isSubset(of: ["one", "other"]) else {
      throw LanguagePackFailure("invalid_plural")
    }
    self = .plural(one: forms["one"], other: other)
  }
  public func encode(to encoder: any Encoder) throws {
    var c = encoder.singleValueContainer()
    switch self {
    case .text(let text): try c.encode(text)
    case .plural(let one, let other):
      var forms = ["other": other]
      if let one { forms["one"] = one }
      try c.encode(forms)
    }
  }
  var forms: [String] {
    switch self {
    case .text(let value): [value]
    case .plural(let one, let other): one.map { [$0, other] } ?? [other]
    }
  }
}

public struct LanguagePack: Codable, Sendable, Equatable {
  public var formatVersion = 1
  public var locale: InterfaceLanguage
  public var packVersion: String?
  public var catalogVersion: String?
  public var messages: [String: LanguageMessage]
  public init(
    locale: InterfaceLanguage, packVersion: String? = nil, catalogVersion: String? = "1",
    messages: [String: LanguageMessage]
  ) {
    self.locale = locale
    self.packVersion = packVersion
    self.catalogVersion = catalogVersion
    self.messages = messages
  }
  public func encoded() throws -> Data {
    let encoder = JSONEncoder()
    encoder.outputFormatting = [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]
    return try encoder.encode(self)
  }
  public static func decode(_ data: Data) throws -> Self {
    guard data.count <= 4 * 1024 * 1024 else { throw LanguagePackFailure("pack_too_large") }
    try StrictLanguageJSON.validate(data)
    let value: Self
    do { value = try JSONDecoder().decode(Self.self, from: data) } catch let error
      as LanguagePackFailure
    { throw error } catch { throw LanguagePackFailure("invalid_pack_format") }
    guard value.formatVersion == 1 else { throw LanguagePackFailure("unsupported_format_version") }
    guard (value.packVersion?.utf8.count ?? 0) <= 64,
      (value.catalogVersion?.utf8.count ?? 0) <= 64, value.messages.count <= 20_000
    else { throw LanguagePackFailure("pack_limit_exceeded") }
    for key in value.messages.keys where key.isEmpty || key.utf8.count > 160 {
      throw LanguagePackFailure("invalid_key")
    }
    for label in [value.packVersion, value.catalogVersion].compactMap({ $0 }) {
      if label.unicodeScalars.contains(where: { CharacterSet.controlCharacters.contains($0) }) {
        throw LanguagePackFailure("invalid_pack_metadata")
      }
    }
    return value
  }
}

/// The small template language is deliberately separate from printf and ICU.
enum LanguageTemplate {
  enum Piece {
    case text(String)
    case argument(String)
  }
  static func pieces(_ text: String) throws -> [Piece] {
    let chars = Array(text)
    var index = 0
    var literal = ""
    var result: [Piece] = []
    while index < chars.count {
      let char = chars[index]
      if char == "{" || char == "}" {
        if index + 1 < chars.count, chars[index + 1] == char {
          literal.append(char)
          index += 2
          continue
        }
        guard char == "{" else { throw LanguagePackFailure("invalid_template") }
        if !literal.isEmpty {
          result.append(.text(literal))
          literal = ""
        }
        index += 1
        var name = ""
        while index < chars.count, chars[index] != "}" {
          name.append(chars[index])
          index += 1
        }
        guard index < chars.count, !name.isEmpty, name.utf8.count <= 64,
          name.unicodeScalars.allSatisfy({
            CharacterSet.alphanumerics.contains($0) && $0.isASCII || $0 == "_"
          })
        else { throw LanguagePackFailure("invalid_template") }
        result.append(.argument(name))
        index += 1
      } else {
        literal.append(char)
        index += 1
      }
    }
    if !literal.isEmpty { result.append(.text(literal)) }
    return result
  }
  static func arguments(_ text: String) throws -> Set<String> {
    Set(try pieces(text).compactMap { if case .argument(let key) = $0 { key } else { nil } })
  }
  static func render(_ text: String, parameters: [String: String]) -> String? {
    guard let parts = try? pieces(text) else { return nil }
    var output = ""
    for part in parts {
      switch part {
      case .text(let value): output += value
      case .argument(let name):
        guard let value = parameters[name] else { return nil }
        output += value
      }
    }
    return output
  }
}
