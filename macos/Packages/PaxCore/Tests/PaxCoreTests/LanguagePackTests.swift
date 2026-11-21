import Foundation
import Testing

@testable import PaxCore

struct LanguagePackTests {
  @MainActor private final class Preferences { var languages = ["fr", "en-US"] }
  private func catalog() throws -> LanguageCatalog {
    try LanguageCatalog(packs: [
      .chinese: LanguagePack(
        locale: .chinese,
        messages: [
          "route.selected": .text("线路：{name}"), "plain": .text("内置"),
          "count": .plural(one: nil, other: "{count}条"),
        ]),
      .english: LanguagePack(
        locale: .english,
        messages: [
          "route.selected": .text("Route: {name}"), "plain": .text("Built-in"),
          "count": .plural(one: "{count} item", other: "{count} items"),
        ]),
    ])
  }
  @Test func rejectsDuplicateEscapedKeysAndParameterLoss() throws {
    let duplicate = #"{"formatVersion":1,"locale":"en","messages":{"plain":"A","pl\u0061in":"B"}}"#
    #expect(throws: LanguagePackFailure("duplicate_key", key: "plain")) {
      try LanguagePack.decode(Data(duplicate.utf8))
    }
    let catalog = try catalog()
    let invalid = LanguagePack(locale: .english, messages: ["route.selected": .text("No facts")])
    #expect(throws: LanguagePackFailure("parameters_mismatch", key: "route.selected")) {
      try catalog.candidate(invalid.encoded())
    }
    let extra = LanguagePack(
      locale: .english, messages: ["route.selected": .text("{name} {secret}")])
    #expect(throws: LanguagePackFailure("parameters_mismatch", key: "route.selected")) {
      try catalog.candidate(extra.encoded())
    }
  }
  @Test @MainActor func followingSystemReevaluatesWithoutOverridingExplicitChoice() throws {
    let catalog = try catalog()
    let suite = "LanguagePreferenceTests." + UUID().uuidString
    let defaults = try #require(UserDefaults(suiteName: suite))
    defer { defaults.removePersistentDomain(forName: suite) }
    let preferences = Preferences()
    let repository = LanguagePackRepository(
      directory: FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString),
      catalog: catalog)
    let store = InterfaceLanguageStore(
      catalog: catalog, repository: repository, defaults: defaults,
      preferences: { preferences.languages })
    #expect(store.context.language == .english)
    preferences.languages = ["zh-Hant", "en"]
    store.updateSystemLanguage()
    #expect(store.context.language == .chinese)
    store.choose(.english)
    preferences.languages = ["zh-Hans"]
    store.updateSystemLanguage()
    #expect(store.context.language == .english)
    store.choose(.system)
    #expect(store.context.language == .chinese)
    #expect(InterfaceLanguage.resolve(["de-DE"]) == .english)
  }
  @Test func importRejectsInvalidStructureAndPluralButIgnoresUnknownKeys() throws {
    let catalog = try catalog()
    let wrong = LanguagePack(
      locale: .english, messages: ["count": .plural(one: nil, other: "{count} items")])
    #expect(throws: LanguagePackFailure("invalid_plural", key: "count")) {
      try catalog.candidate(wrong.encoded())
    }
    let data = try LanguagePack(
      locale: .english,
      messages: [
        "plain": .text("Custom"), "future.key": .text("Ignored"),
      ]
    ).encoded()
    let candidate = try catalog.candidate(data)
    #expect(candidate.ignoredKeys == ["future.key"])
    #expect(candidate.pack.messages == ["plain": .text("Custom")])
    #expect(throws: LanguagePackFailure("invalid_utf8")) { try LanguagePack.decode(Data([0xff])) }
    #expect(throws: LanguagePackFailure("unsupported_format_version")) {
      try LanguagePack.decode(Data(#"{"formatVersion":2,"locale":"en","messages":{}}"#.utf8))
    }
  }
  @Test func rendersReorderedArgumentsWithoutRecursionAndPluralizes() throws {
    let context = PresentationContext(
      language: .english,
      messages: [
        "value": .text("{{literal}} {second} {first} 100% <b>"),
        "items": .plural(one: "{count} item", other: "{count} items"),
      ])
    #expect(
      context.text("value", ["first": "{second}", "second": "A"]) == "{literal} A {second} 100% <b>"
    )
    #expect(context.text("items", count: 1) == "1 item")
    #expect(context.text("items", count: 0) == "0 items")
    #expect(context.text("items", count: 2) == "2 items")
  }
  @Test func shippedCatalogIsCompleteAndExportRoundTrips() throws {
    let catalog = try LanguageCatalog.bundled()
    for language in InterfaceLanguage.allCases {
      let pack = try #require(catalog.packs[language])
      let candidate = try catalog.candidate(pack.encoded())
      #expect(candidate.ignoredKeys.isEmpty)
      #expect(candidate.pack.messages == pack.messages)
    }
  }
  @Test func errorPresentationUsesSelectedLanguageAndKeepsParameters() throws {
    let catalog = try LanguageCatalog.bundled()
    let context = PresentationContext(
      language: .english, messages: try #require(catalog.packs[.english]).messages)
    #expect(
      ProductErrorPresentation.display(ClientError.timeout, context: context)
        == "Connecting to the backend timed out.")
    #expect(
      ProductErrorPresentation.message(
        ProductError(code: "target_http_failed", messageParams: ["403"]), context: context)
        == "The test target returned HTTP 403.")
    #expect(
      ProductErrorPresentation.message(ProductError(code: "new_unknown_code"), context: context)
        == "The proxy operation did not finish. Retry it.")
  }
  @Test func symlinkCannotReplaceAnotherFile() async throws {
    let catalog = try catalog()
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: directory) }
    let original = directory.appendingPathComponent("unrelated")
    try Data("keep".utf8).write(to: original)
    try FileManager.default.createSymbolicLink(
      at: directory.appendingPathComponent("en.paxlang"), withDestinationURL: original)
    let repository = LanguagePackRepository(directory: directory, catalog: catalog)
    let candidate = try catalog.candidate(
      LanguagePack(
        locale: .english, messages: ["plain": .text("New")]
      ).encoded())
    await #expect(throws: LanguagePackFailure("pack_path_is_symlink")) {
      try await repository.install(candidate)
    }
    #expect(try String(contentsOf: original, encoding: .utf8) == "keep")
  }
  @Test @MainActor func replacementRestoreAndOldSnapshotsPreserveFacts() async throws {
    let catalog = try catalog()
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    defer { try? FileManager.default.removeItem(at: directory) }
    let repository = LanguagePackRepository(directory: directory, catalog: catalog)
    let suite = "LanguagePackTests." + UUID().uuidString
    let defaults = try #require(UserDefaults(suiteName: suite))
    defer { defaults.removePersistentDomain(forName: suite) }
    let store = InterfaceLanguageStore(
      catalog: catalog, repository: repository, defaults: defaults,
      preferences: { ["fr", "en-US"] })
    #expect(store.context.language == .english)
    store.choose(.chinese)
    let english = try catalog.candidate(
      LanguagePack(
        locale: .english, messages: ["plain": .text("Custom")]
      ).encoded())
    try await store.install(english)
    #expect(store.context.language == .chinese)
    store.choose(.english)
    let snapshot = store.context
    #expect(snapshot.text("plain") == "Custom")
    let replacement = try catalog.candidate(
      LanguagePack(
        locale: .english, messages: ["route.selected": .text("Selected: {name}")]
      ).encoded())
    try await store.install(replacement)
    #expect(store.context.text("plain") == "Built-in")
    #expect(store.context.revision > snapshot.revision)
    #expect(snapshot.text("plain") == "Custom")
    let resumed = InterfaceLanguageStore(
      catalog: catalog, repository: repository, defaults: defaults, preferences: { ["zh"] })
    await resumed.loadInstalled()
    #expect(resumed.choice == .english)
    #expect(resumed.context.text("route.selected", ["name": "用户线路"]) == "Selected: 用户线路")
    try await resumed.restore(.english)
    #expect(resumed.context.text("route.selected", ["name": "A"]) == "Route: A")
    #expect(
      !FileManager.default.fileExists(atPath: directory.appendingPathComponent("en.paxlang").path))
  }
  @Test func installedCorruptionAndUpgradeKeepOriginalFile() async throws {
    let catalog = try catalog()
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: directory) }
    let file = directory.appendingPathComponent("en.paxlang")
    let repository = LanguagePackRepository(directory: directory, catalog: catalog)
    try Data("broken".utf8).write(to: file)
    let corrupted = await repository.load(.english)
    #expect(corrupted.exists && corrupted.candidate == nil && corrupted.problem != nil)
    #expect(try String(contentsOf: file, encoding: .utf8) == "broken")
    let outdated = LanguagePack(
      locale: .english,
      messages: [
        "plain": .text("Compatible"), "route.selected": .text("Old {route}"),
        "removed": .text("Old"),
      ])
    let bytes = try outdated.encoded()
    try bytes.write(to: file)
    let upgraded = await repository.load(.english)
    #expect(upgraded.candidate?.pack.messages == ["plain": .text("Compatible")])
    #expect(upgraded.candidate?.ignoredKeys == ["removed", "route.selected"])
    #expect(try Data(contentsOf: file) == bytes)
    try await repository.restore(.english)
  }
}
