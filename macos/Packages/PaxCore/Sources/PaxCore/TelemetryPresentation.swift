import Foundation

extension TelemetryStore {
  public var qualityMessage: String { qualityMessage(context: .chinese) }
  public func qualityMessage(
    context: PresentationContext, includePastEviction: Bool = true
  ) -> String {
    if !connected { return context.text("history_quality.disconnected") }
    if !history.errorCode.isEmpty {
      return ProductErrorPresentation.message(.init(code: history.errorCode), context: context)
    }
    if includePastEviction && history.capacityEvicted {
      return context.text("history_quality.previous_eviction")
    }
    if history.collectionState == "idle" { return context.text("history_quality.idle") }
    return ""
  }
}
