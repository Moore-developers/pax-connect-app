import PaxCore

@MainActor enum RoutingGeoPresentation {
  static func sourceExplanation(context: PresentationContext = .chinese) -> String {
    context.text("routing_copy.5d44036ea1")
  }

  static func subtitle(
    _ pack: RoutingWorkbenchStore.RulePackage, live: Bool, context: PresentationContext = .chinese
  ) -> String {
    guard live, pack.kind == .geo else { return pack.kindPresentation(context: context).subtitle }
    return pack.kindPresentation(context: context).subtitle
      + context.text("routing_copy.1d233f050d")
      + (pack.updatedAt.map {
        context.text("routing_copy.5ceb7518cf") + TrafficFormatting.dateMinute($0)
      } ?? context.text("routing_copy.63ce7b75f1"))
  }

  static func source(
    _ pack: RoutingWorkbenchStore.RulePackage, context: PresentationContext = .chinese
  ) -> String {
    context.text("routing_copy.6181ed6f97")
      + (pack.source.isEmpty ? context.text("routing_copy.820bad63dd") : pack.source)
  }
}
