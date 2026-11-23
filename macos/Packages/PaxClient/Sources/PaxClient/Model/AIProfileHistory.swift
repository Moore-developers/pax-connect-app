import Foundation
import PaxProto

public struct AIProfileHistoryPage: Sendable {
  public struct Cursor: Sendable {
    let value: Pax_V1_AIProfileHistoryCursor
  }

  public struct Event: Sendable {
    public let date: Date
    public let wasApplied: Bool
    public let profileID: String
    public let profileName: String
    public let source: AIServiceSource

    init(_ value: Pax_V1_AIProfileHistoryEvent) {
      date = Date(timeIntervalSince1970: Double(value.atMs) / 1000)
      wasApplied = value.kind == "apply"
      profileID = value.profileID
      profileName = value.profileName
      source = AIServiceSource(rawValue: value.serviceSource) ?? .unknown
    }
  }

  public let events: [Event]
  public let next: Cursor?

  init(_ value: Pax_V1_AIProfileHistoryPage) {
    events = value.events.map(Event.init)
    next = value.hasNext ? Cursor(value: value.next) : nil
  }
}
