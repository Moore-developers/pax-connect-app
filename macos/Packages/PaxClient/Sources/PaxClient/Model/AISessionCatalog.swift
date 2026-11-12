import Foundation
import PaxProto

/// Native directory metadata exists independently of retained usage requests.
public struct AISessionCatalogEntry: Equatable, Sendable, Identifiable {
  public let key: AISessionKey
  public let title: String
  public let titleSource: String
  public let titleStatus: String
  public let lastActivity: Date
  public let unlinkedChild: Bool
  public var id: String { key.id }
  public init(
    key: AISessionKey, title: String = "", titleSource: String = "",
    titleStatus: String = "untitled", lastActivity: Date = .distantPast, unlinkedChild: Bool = false
  ) {
    self.key = key
    self.title = title
    self.titleSource = titleSource
    self.titleStatus = titleStatus
    self.lastActivity = lastActivity
    self.unlinkedChild = unlinkedChild
  }
  init(_ p: Pax_V1_AISessionCatalogEntry) {
    self.init(
      key: .init(cli: p.key.cli, session: p.key.sessionID), title: p.title,
      titleSource: p.titleSource, titleStatus: p.titleStatus,
      lastActivity: Date(timeIntervalSince1970: Double(p.lastActivityMs) / 1000),
      unlinkedChild: p.unlinkedChild)
  }
}

public struct AISessionCatalogPage: Sendable {
  public let items: [AISessionCatalogEntry]
  public let selected: AISessionCatalogEntry?
  public let counts: [String: Int]
  public let sourceErrors: [String: String]
  public let revision: UInt64
  public let usageRevision: UInt64
  public let ready: Bool
  public let total: Int
  public let nextPageToken: String
  public let resetRequired: Bool
  public init(
    items: [AISessionCatalogEntry] = [], selected: AISessionCatalogEntry? = nil,
    counts: [String: Int] = [:], sourceErrors: [String: String] = [:], revision: UInt64 = 0,
    usageRevision: UInt64 = 0,
    ready: Bool = true, total: Int = 0, nextPageToken: String = "", resetRequired: Bool = false
  ) {
    self.items = items
    self.selected = selected
    self.counts = counts
    self.sourceErrors = sourceErrors
    self.revision = revision
    self.usageRevision = usageRevision
    self.ready = ready
    self.total = total
    self.nextPageToken = nextPageToken
    self.resetRequired = resetRequired
  }
  init(_ p: Pax_V1_AISessionCatalogPage) {
    self.init(
      items: p.items.map(AISessionCatalogEntry.init),
      selected: p.hasSelected ? AISessionCatalogEntry(p.selected) : nil,
      counts: Dictionary(p.counts.map { ($0.cli, Int($0.count)) }, uniquingKeysWith: max),
      sourceErrors: Dictionary(
        p.sources.map { ($0.cli, $0.errorCode) }, uniquingKeysWith: { _, latest in latest }),
      revision: p.revision, usageRevision: p.usageRevision, ready: p.ready, total: Int(p.total),
      nextPageToken: p.nextPageToken,
      resetRequired: p.resetRequired)
  }
}
