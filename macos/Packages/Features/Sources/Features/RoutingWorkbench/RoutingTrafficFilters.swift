import Foundation
import PaxCore

extension RoutingWorkbenchStore {
  enum Dimension: Int, CaseIterable {
    case application, domain, system
    var title: String { title(context: .chinese) }
    func title(context: PresentationContext) -> String {
      [
        context.text("traffic_details.63c73c4730"), context.text("traffic_details.2229524311"),
        context.text("traffic_details.360c4bd731"),
      ][rawValue]
    }
  }
  enum Period: Int, CaseIterable {
    case day = 0
    case week = 1
    case month = 2
    case minute = 3
    case yesterday = 4
    static var allCases: [Period] { [.day, .yesterday, .week, .month] }
    var title: String { title(context: .chinese) }
    func title(context: PresentationContext) -> String {
      [
        context.text("traffic_details.5c9cf5e0dd"), context.text("traffic_details.6082bd2a63"),
        context.text("traffic_details.afcd5fc44b"), context.text("traffic_details.2bf8a3a26f"),
        context.text("traffic_details.655d11bd2a"),
      ][rawValue]
    }
    var seconds: TimeInterval { [86_400.0, 604_800, 2_592_000, 60, 86_400][rawValue] }
    var window: String { ["today", "week", "month", "minute", "yesterday"][rawValue] }
  }
  enum Direction: Int, CaseIterable {
    case all, proxy, direct, unknown
    var title: String { title(context: .chinese) }
    func title(context: PresentationContext) -> String {
      [
        context.text("traffic_details.5c55a67935"), context.text("traffic_details.5e84ea61e8"),
        context.text("traffic_details.b06325c566"), context.text("traffic_details.d0ba4641be"),
      ][rawValue]
    }
  }
}
