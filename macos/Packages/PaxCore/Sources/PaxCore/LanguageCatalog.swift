import Foundation

public struct LanguagePackCandidate: Sendable {
  public let pack: LanguagePack
  public let ignoredKeys: [String]
  public let inheritedCount: Int
}

public struct LanguageCatalog: Sendable {
  public let packs: [InterfaceLanguage: LanguagePack]
  public init(packs: [InterfaceLanguage: LanguagePack]) throws {
    guard let chinese = packs[.chinese], let english = packs[.english],
      Set(chinese.messages.keys) == Set(english.messages.keys)
    else { throw LanguagePackFailure("builtin_keys_mismatch") }
    for language in InterfaceLanguage.allCases {
      guard let pack = packs[language] else { throw LanguagePackFailure("builtin_pack_missing") }
      for (key, entry) in pack.messages { try Self.validate(entry, key: key, locale: language) }
    }
    for key in chinese.messages.keys {
      guard let c = chinese.messages[key], let e = english.messages[key],
        try Self.signature(c) == Self.signature(e)
      else { throw LanguagePackFailure("builtin_parameters_mismatch", key: key) }
    }
    self.packs = packs
  }
  public static func bundled() throws -> Self {
    guard let root = Bundle.module.url(forResource: "LanguagePacks", withExtension: nil) else {
      throw LanguagePackFailure("builtin_pack_missing")
    }
    var packs: [InterfaceLanguage: LanguagePack] = [:]
    for locale in InterfaceLanguage.allCases {
      let files = try FileManager.default.contentsOfDirectory(
        at: root.appendingPathComponent(locale.rawValue), includingPropertiesForKeys: nil
      )
      .filter { $0.pathExtension == "paxlang" }.sorted { $0.path < $1.path }
      var messages: [String: LanguageMessage] = [:]
      for file in files {
        let pack = try LanguagePack.decode(Data(contentsOf: file))
        guard pack.locale == locale else { throw LanguagePackFailure("builtin_locale_mismatch") }
        for (key, value) in pack.messages {
          guard messages.updateValue(value, forKey: key) == nil else {
            throw LanguagePackFailure("duplicate_builtin_key", key: key)
          }
        }
      }
      packs[locale] = LanguagePack(locale: locale, messages: messages)
    }
    return try Self(packs: packs)
  }
  public func candidate(_ data: Data) throws -> LanguagePackCandidate {
    let pack = try LanguagePack.decode(data)
    return try candidate(pack, tolerateChangedEntries: false)
  }
  func candidate(_ pack: LanguagePack, tolerateChangedEntries: Bool) throws -> LanguagePackCandidate
  {
    guard let builtin = packs[pack.locale] else {
      throw LanguagePackFailure("unsupported_language")
    }
    var known: [String: LanguageMessage] = [:]
    var ignored: [String] = []
    for (key, entry) in pack.messages {
      guard let original = builtin.messages[key], !key.hasPrefix("fixed.") else {
        ignored.append(key)
        continue
      }
      do {
        try Self.validate(entry, key: key, locale: pack.locale)
        guard try Self.signature(entry) == Self.signature(original) else {
          throw LanguagePackFailure("parameters_mismatch", key: key)
        }
        switch (original, entry) {
        case (.text, .text), (.plural, .plural): break
        default: throw LanguagePackFailure("entry_type_mismatch", key: key)
        }
        known[key] = entry
      } catch {
        guard tolerateChangedEntries else { throw error }
        ignored.append(key)
      }
    }
    guard !known.isEmpty || tolerateChangedEntries else {
      throw LanguagePackFailure("no_usable_entries")
    }
    var clean = pack
    clean.messages = known
    return LanguagePackCandidate(
      pack: clean, ignoredKeys: ignored.sorted(),
      inheritedCount: builtin.messages.keys.filter { !($0.hasPrefix("fixed.")) }.count - known.count
    )
  }
  private static func validate(_ entry: LanguageMessage, key: String, locale: InterfaceLanguage)
    throws
  {
    guard !key.isEmpty, key.utf8.count <= 160 else {
      throw LanguagePackFailure("invalid_key", key: key)
    }
    for text in entry.forms {
      guard text.utf8.count <= (key.hasPrefix("browser.") ? 2048 : 8192),
        !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
        !text.unicodeScalars.contains(where: {
          CharacterSet.controlCharacters.contains($0) && $0 != "\n" && $0 != "\t"
        })
      else { throw LanguagePackFailure("invalid_text", key: key) }
      _ = try LanguageTemplate.pieces(text)
    }
    if case .plural(let one, let other) = entry {
      let arguments = try LanguageTemplate.arguments(other)
      let sameArguments = try one.map { try LanguageTemplate.arguments($0) == arguments } ?? true
      guard (locale == .english) == (one != nil),
        arguments.contains("count"), sameArguments
      else { throw LanguagePackFailure("invalid_plural", key: key) }
    }
  }
  private static func signature(_ entry: LanguageMessage) throws -> Set<String> {
    try LanguageTemplate.arguments(entry.forms[0])
  }
}

/// A value snapshot: existing editors and report exports never observe a later replacement.
public struct PresentationContext: Sendable {
  /// The compatibility default for pure callers; the app validates its catalog before launching.
  public static let chinese = PresentationContext(
    language: .chinese, messages: (try? LanguageCatalog.bundled())?.packs[.chinese]?.messages ?? [:]
  )
  public let language: InterfaceLanguage
  public let revision: UInt64
  public let messages: [String: LanguageMessage]
  public let customVersion: String?
  public init(
    language: InterfaceLanguage, revision: UInt64 = 0, messages: [String: LanguageMessage],
    customVersion: String? = nil
  ) {
    self.language = language
    self.revision = revision
    self.messages = messages
    self.customVersion = customVersion
  }
  public func text(_ key: String, _ parameters: [String: String] = [:], count: Int? = nil) -> String
  {
    guard let message = messages[key] else { return "—" }
    var values = parameters
    if let count { values["count"] = count.formatted() }
    let template: String
    switch message {
    case .text(let value): template = value
    case .plural(let one, let other):
      template = language == .english && count == 1 ? one ?? other : other
    }
    return LanguageTemplate.render(template, parameters: values) ?? "—"
  }
  public func exportPack() -> LanguagePack {
    LanguagePack(
      locale: language, packVersion: customVersion,
      messages: messages.filter { !$0.key.hasPrefix("fixed.") })
  }
  public func browserPresentation() -> BrowserPresentation {
    var subset: [String: String] = [:]
    for (key, value) in messages where key.hasPrefix("browser.") {
      if case .text(let template) = value { subset[key] = template }
    }
    return BrowserPresentation(locale: language.rawValue, messages: subset)
  }
}
