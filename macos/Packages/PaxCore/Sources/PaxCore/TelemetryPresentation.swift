import Foundation

extension TelemetryStore {
  public var qualityMessage: String { qualityMessage(context: .chinese) }
  public func qualityMessage(context: PresentationContext) -> String {
    if !connected { return context.text("history_quality.disconnected") }
    if !history.errorCode.isEmpty {
      return ProductErrorPresentation.message(.init(code: history.errorCode), context: context)
    }
    if history.collectionState == "idle" { return context.text("history_quality.idle") }
    return ""
  }
}
