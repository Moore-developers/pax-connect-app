import Foundation
import Observation
import PaxClient

/// Owns native directory filters, pagination and stale-response rejection.
@MainActor @Observable public final class AISessionCatalogStore {
  public typealias Query =
    @Sendable (Date, Date, String, String, String, AISessionKey?) async throws ->
    AISessionCatalogPage
  public private(set) var items: [AISessionCatalogEntry] = []
  public private(set) var selected: AISessionCatalogEntry?
  public private(set) var counts: [String: Int] = [:]
  public private(set) var sourceErrors: [String: String] = [:]
  public private(set) var total = 0
  public private(set) var ready = false
  public private(set) var loading = false
  public private(set) var error: String?
  public private(set) var collectionError: String?
  public private(set) var cli = ""
  public private(set) var text = ""
  public private(set) var hasMore = false
  public private(set) var revision: UInt64 = 0
  private let query: Query
  private var nextPage = ""
  private var key: AISessionKey?
  private var task: Task<Void, Never>?
  private var token = 0
  private var active = false
  private var observed: UInt64 = 0
  private var observedUsage: UInt64 = 0
  private var usageRevision: UInt64 = 0
  private var range: Range<Date>?
  public init(query: @escaping Query) { self.query = query }
  public func setActive(_ value: Bool) {
    active = value
    if !value { cancel() }
  }
  public func observe(
    revision: UInt64, usageRevision: UInt64 = 0, error: String = "", reset: Bool = false
  ) {
    collectionError = error.isEmpty ? nil : "本机会话目录索引不可用，保留已知记录"
    if reset {
      cancel()
      observed = revision
      observedUsage = usageRevision
      self.usageRevision = 0
      self.revision = 0
      return
    }
    observed = max(observed, revision)
    observedUsage = max(observedUsage, usageRevision)
  }
  public func filter(cli: String, text: String) {
    guard self.cli != cli || self.text != text else { return }
    self.cli = cli
    self.text = text
    items = []
    nextPage = ""
    hasMore = false
    read(append: false, target: 100)
  }
  /// The owning session store refreshes once after updating the shared page range.
  public func select(range: Range<Date>) {
    guard self.range != range else { return }
    cancel()
    self.range = range
    items = []
    counts = [:]
    total = 0
    nextPage = ""
    hasMore = false
  }
  public func select(_ key: AISessionKey?) {
    self.key = key
    selected = items.first { $0.key == key } ?? (selected?.key == key ? selected : nil)
  }
  public func refresh() { read(append: false, target: items.count) }
  public func loadMore() {
    guard hasMore, !loading else { return }
    read(append: true, target: 100)
  }
  private func cancel() {
    token += 1
    task?.cancel()
    loading = false
  }
  private func read(append: Bool, target: Int) {
    guard active, let range else { return }
    cancel()
    let ticket = token
    let cli = self.cli
    let text = self.text
    let key = self.key
    let pageToken = append ? nextPage : ""
    loading = true
    error = nil
    task = Task { [weak self] in
      guard let self else { return }
      defer { if token == ticket { loading = false } }
      do {
        var page = try await query(range.lowerBound, range.upperBound, cli, text, pageToken, key)
        if page.resetRequired {
          guard !Task.isCancelled, token == ticket else { return }
          page = try await query(range.lowerBound, range.upperBound, cli, text, "", key)
        }
        let startFresh = !append || page.revision != revision || page.usageRevision != usageRevision
        var loaded = startFresh ? [] : items
        loaded += page.items
        let firstRevision = page.revision
        let firstUsageRevision = page.usageRevision
        while !append, loaded.count < target, !page.nextPageToken.isEmpty {
          let next = try await query(
            range.lowerBound, range.upperBound, cli, text, page.nextPageToken, key)
          guard !next.resetRequired, next.revision == firstRevision,
            next.usageRevision == firstUsageRevision
          else {
            throw ClientError.unavailable
          }
          loaded += next.items
          page = next
        }
        guard !Task.isCancelled, active, token == ticket else { return }
        guard page.revision >= observed, page.usageRevision >= observedUsage else {
          throw ClientError.unavailable
        }
        items = loaded
        selected = page.selected ?? loaded.first { $0.key == key }
        counts = page.counts
        sourceErrors = page.sourceErrors
        total = page.total
        ready = page.ready
        revision = page.revision
        usageRevision = page.usageRevision
        nextPage = page.nextPageToken
        hasMore = !nextPage.isEmpty
      } catch {
        guard !Task.isCancelled, token == ticket else { return }
        self.error = "会话目录读取失败，请重试"
      }
    }
  }
}
