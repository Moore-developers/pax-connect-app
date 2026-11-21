import Foundation
import Observation

@MainActor @Observable public final class InterfaceLanguageStore {
  public private(set) var choice: InterfaceLanguageChoice
  public private(set) var context: PresentationContext
  public private(set) var installed: [InterfaceLanguage: InstalledLanguagePack] = [:]
  public private(set) var changingPack = false
  private let defaults: UserDefaults
  private let preferences: () -> [String]
  private let repository: LanguagePackRepository
  private let catalog: LanguageCatalog
  private let key: String
  public init(
    catalog: LanguageCatalog, repository: LanguagePackRepository,
    defaults: UserDefaults = .standard, key: String = "pax.interface-language.v1",
    preferences: @escaping () -> [String] = { InterfaceLanguagePreferences.current() }
  ) {
    self.catalog = catalog
    self.repository = repository
    self.defaults = defaults
    self.key = key
    self.preferences = preferences
    let choice =
      defaults.string(forKey: key).flatMap(InterfaceLanguageChoice.init(rawValue:)) ?? .system
    self.choice = choice
    let language = InterfaceLanguage(rawValue: choice.rawValue) ?? .resolve(preferences())
    context = PresentationContext(
      language: language, messages: catalog.packs[language]?.messages ?? [:])
  }
  public func loadInstalled() async {
    for language in InterfaceLanguage.allCases {
      installed[language] = await repository.load(language)
    }
    publish()
  }
  public func choose(_ value: InterfaceLanguageChoice) {
    guard value != choice else { return }
    defaults.set(value.rawValue, forKey: key)
    choice = value
    publish()
  }
  public func updateSystemLanguage() {
    guard choice == .system else { return }
    let language = InterfaceLanguage.resolve(preferences())
    if language != context.language { publish() }
  }
  public func preview(_ file: URL) async throws -> LanguagePackCandidate {
    try await repository.preview(file)
  }
  public func install(_ candidate: LanguagePackCandidate) async throws {
    guard !changingPack else { throw LanguagePackFailure("pack_operation_busy") }
    changingPack = true
    defer { changingPack = false }
    let value = try await repository.install(candidate)
    installed[candidate.pack.locale] = value
    if candidate.pack.locale == context.language { publish() }
  }
  public func restore(_ language: InterfaceLanguage) async throws {
    guard !changingPack else { throw LanguagePackFailure("pack_operation_busy") }
    changingPack = true
    defer { changingPack = false }
    try await repository.restore(language)
    installed[language] = InstalledLanguagePack(exists: false, candidate: nil, problem: nil)
    if language == context.language { publish() }
  }
  private func publish() {
    let language = InterfaceLanguage(rawValue: choice.rawValue) ?? .resolve(preferences())
    let custom = installed[language]?.candidate?.pack
    var messages = catalog.packs[language]?.messages ?? [:]
    if let custom { messages.merge(custom.messages) { _, value in value } }
    context = PresentationContext(
      language: language, revision: context.revision &+ 1, messages: messages,
      customVersion: custom.map { $0.packVersion ?? "" })
  }
}

@usableFromInline enum InterfaceLanguagePreferences {
  @usableFromInline static func current() -> [String] {
    // Read the effective app preference (including the macOS app-specific override) afresh.
    _ = CFPreferencesAppSynchronize(kCFPreferencesCurrentApplication)
    return CFPreferencesCopyAppValue("AppleLanguages" as CFString, kCFPreferencesCurrentApplication)
      as? [String] ?? Locale.preferredLanguages
  }
}
