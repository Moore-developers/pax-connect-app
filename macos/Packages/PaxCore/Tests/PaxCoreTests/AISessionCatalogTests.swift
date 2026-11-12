import Foundation
import Testing

@testable import PaxCore

private actor CatalogFixture {
  let first = AISessionCatalogEntry(
    key: .init(cli: "codex", session: "empty"), title: "No requests")
  let second = AISessionCatalogEntry(
    key: .init(cli: "claude", session: "same"), title: "Claude native")
  private var version: UInt64 = 1
  private var calls = 0
  func rename() { version = 2 }
  func count() -> Int { calls }
  func read(
    _ from: Date, _ to: Date, _ cli: String, _ text: String, _ token: String, _ key: AISessionKey?
  )
    -> AISessionCatalogPage
  {
    calls += 1
    let current = AISessionCatalogEntry(
      key: first.key, title: version == 1 ? first.title : "Renamed native")
    if token == "page-1", version == 2 { return .init(revision: 2, resetRequired: true) }
    let filtered = cli.isEmpty && text.isEmpty ? [current, second] : [second]
    let selected = key == first.key ? current : nil
    return .init(
      items: token.isEmpty ? [filtered[0]] : Array(filtered.suffix(1)), selected: selected,
      counts: ["codex": 1, "claude": 1], revision: version, total: filtered.count,
      nextPageToken: token.isEmpty && filtered.count > 1 ? "page-1" : "")
  }
}

@MainActor @Suite("原生会话目录") struct AISessionCatalogTests {
  @Test func changingDateRangeDropsOldPagesAndRejectsTheirLateResponses() async throws {
    let fixture = RangeCatalogFixture()
    let catalog = AISessionCatalogStore(query: { await fixture.read($0, $1, $2, $3, $4, $5) })
    let first = Date(timeIntervalSince1970: 1000)..<Date(timeIntervalSince1970: 2000)
    let second = Date(timeIntervalSince1970: 2000)..<Date(timeIntervalSince1970: 3000)
    catalog.select(range: first)
    catalog.setActive(true)
    catalog.refresh()
    for _ in 0..<100 where catalog.loading { try await Task.sleep(for: .milliseconds(5)) }
    #expect(catalog.items.first?.key.session == "first" && catalog.hasMore)
    catalog.loadMore()
    for _ in 0..<100 where await !fixture.waiting() {
      try await Task.sleep(for: .milliseconds(5))
    }
    #expect(await fixture.waiting())
    catalog.select(range: second)
    #expect(catalog.items.isEmpty && catalog.counts.isEmpty && !catalog.hasMore)
    catalog.refresh()
    for _ in 0..<100 where catalog.loading { try await Task.sleep(for: .milliseconds(5)) }
    await fixture.release()
    for _ in 0..<10 { await Task.yield() }
    #expect(catalog.items.map(\.key.session) == ["second"])
    #expect(catalog.total == 1 && catalog.counts == ["claude": 1] && !catalog.hasMore)
    #expect(await fixture.lastRange() == second)
    catalog.setActive(false)
  }

  @Test func zeroUsagePaginationFilterAndRenameKeepIdentity() async throws {
    let fixture = CatalogFixture()
    let catalog = AISessionCatalogStore(query: { await fixture.read($0, $1, $2, $3, $4, $5) })
    catalog.refresh()
    #expect(await fixture.count() == 0)
    catalog.select(range: Date(timeIntervalSince1970: 1)..<Date(timeIntervalSince1970: 2))
    catalog.setActive(true)
    catalog.select(.init(cli: "codex", session: "empty"))
    catalog.refresh()
    for _ in 0..<100 where catalog.loading { try await Task.sleep(for: .milliseconds(5)) }
    #expect(catalog.items.map(\.key.session) == ["empty"])
    #expect(catalog.selected?.title == "No requests")
    catalog.loadMore()
    for _ in 0..<100 where catalog.loading { try await Task.sleep(for: .milliseconds(5)) }
    #expect(catalog.items.count == 2)
    catalog.filter(cli: "claude", text: "native")
    for _ in 0..<100 where catalog.loading { try await Task.sleep(for: .milliseconds(5)) }
    #expect(catalog.items.first?.key.cli == "claude")
    #expect(catalog.selected?.key.session == "empty")
    await fixture.rename()
    catalog.observe(revision: 2)
    catalog.refresh()
    for _ in 0..<100 where catalog.loading { try await Task.sleep(for: .milliseconds(5)) }
    #expect(catalog.selected?.title == "Renamed native")
    #expect(catalog.cli == "claude" && catalog.text == "native")
    catalog.observe(revision: 2, error: "ai_session_catalog_unavailable")
    #expect(catalog.collectionError != nil && catalog.selected?.title == "Renamed native")
    catalog.observe(revision: 2)
    #expect(catalog.collectionError == nil)
    catalog.setActive(false)
  }
}

private actor RangeCatalogFixture {
  private var pending: CheckedContinuation<AISessionCatalogPage, Never>?
  private var last: Range<Date>?
  func waiting() -> Bool { pending != nil }
  func lastRange() -> Range<Date>? { last }
  func release() {
    pending?.resume(
      returning: .init(
        items: [.init(key: .init(cli: "codex", session: "late-first"))],
        counts: ["codex": 2], revision: 1, total: 2))
    pending = nil
  }
  func read(
    _ from: Date, _ to: Date, _ cli: String, _ text: String, _ token: String, _ key: AISessionKey?
  ) async -> AISessionCatalogPage {
    last = from..<to
    if !token.isEmpty { return await withCheckedContinuation { pending = $0 } }
    let first = from.timeIntervalSince1970 == 1000
    return .init(
      items: [
        .init(key: .init(cli: first ? "codex" : "claude", session: first ? "first" : "second"))
      ],
      counts: [first ? "codex" : "claude": first ? 2 : 1], revision: 1,
      total: first ? 2 : 1, nextPageToken: first ? "first-page-2" : "")
  }
}

private actor UsageCatalogFixture {
  private var usage: UInt64 = 1
  func advance() { usage = 2 }
  func read(_ token: String) -> AISessionCatalogPage {
    if !token.isEmpty, usage == 2 {
      return .init(revision: 1, usageRevision: 2, resetRequired: true)
    }
    return .init(
      items: [.init(key: .init(cli: "codex", session: usage == 1 ? "old" : "new"))],
      counts: ["codex": 2], revision: 1, usageRevision: usage, total: 2,
      nextPageToken: "page-2")
  }
}

extension AISessionCatalogTests {
  @Test func usageOnlyPaginationResetReplacesOldCandidates() async throws {
    let fixture = UsageCatalogFixture()
    let catalog = AISessionCatalogStore(query: { _, _, _, _, token, _ in await fixture.read(token) }
    )
    catalog.select(range: Date(timeIntervalSince1970: 1)..<Date(timeIntervalSince1970: 2))
    catalog.setActive(true)
    catalog.refresh()
    for _ in 0..<100 where catalog.loading { try await Task.sleep(for: .milliseconds(5)) }
    #expect(catalog.items.map(\.key.session) == ["old"])
    await fixture.advance()
    catalog.observe(revision: 1, usageRevision: 2)
    catalog.loadMore()
    for _ in 0..<100 where catalog.loading { try await Task.sleep(for: .milliseconds(5)) }
    #expect(catalog.items.map(\.key.session) == ["new"])
    #expect(catalog.error == nil && catalog.total == 2)
    catalog.setActive(false)
  }
}
