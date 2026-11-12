import PaxClient

extension RoutesStore {
  public enum Intent: Sendable {
    case select(NodeProfile)
    case saveChain(ChainEdit)
    case delete(NodeProfile)
    case save(RouteEdit)
    case saveGroups(RouteGroupsEdit)
    case confirm(RoutesPreview, [String])
    case automaticImport(RoutesPreview, [String])
    case confirmTLSBatch([NodeProfile])
    case restore(NodeProfile)
    case refresh(SubscriptionSummary)
    case refreshVia(SubscriptionSummary, NodeProfile)
    case refreshSaved(SubscriptionSummary)
    case replaceSource(SubscriptionSummary, String, String)
    case deleteSubscription(SubscriptionSummary, UInt64)
    case test(NodeProfile)
    case diagnose(NodeProfile)
    case queryExit(NodeProfile)
    case queryExitBatch([NodeProfile])
    case download(NodeProfile)
    case testBatch([NodeProfile], Bool)
  }
  /// 来源过滤：全部 / 本地 / 每个订阅 / 已隐藏（恢复入口）。
  public enum SourceFilter: Equatable, Sendable {
    case all
    case local
    case source(String)
    case hidden
  }
}

extension RoutesStore.Intent {
  var feedbackOwner: String {
    switch self {
    case .select: "selection"
    case .refresh(let source), .refreshVia(let source, _), .refreshSaved(let source),
      .replaceSource(let source, _, _):
      "subscription:" + source.id
    case .test, .download, .diagnose, .testBatch, .queryExit, .queryExitBatch: "measurement"
    default: "route-action"
    }
  }
}
