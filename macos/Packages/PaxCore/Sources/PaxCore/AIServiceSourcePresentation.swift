import PaxClient

extension AIServiceSource {
  public func label(context: PresentationContext) -> String {
    context.text("usage.source." + (self == .unrecorded ? "unknown" : rawValue))
  }
}
