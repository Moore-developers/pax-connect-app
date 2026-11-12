import PaxCore

@MainActor enum RoutingGeoPresentation {
  static let sourceExplanation = "固定社区来源：SagerNet sing-geosite（Google/CN）与 sing-geoip；更新失败保留原版。"

  static func subtitle(_ pack: RoutingWorkbenchStore.RulePackage, live: Bool) -> String {
    guard live, pack.kind == .geo else { return pack.kindPresentation.subtitle }
    return pack.kindPresentation.subtitle + " · Google 代理优先，国内及私网直连"
      + (pack.updatedAt.map { " · 更新于 " + TrafficFormatting.dateMinute($0) } ?? " · 随 App 提供的基线")
  }

  static func source(_ pack: RoutingWorkbenchStore.RulePackage) -> String {
    "来源：" + (pack.source.isEmpty ? "Geo 社区规则集（内置基线及已下载缓存）" : pack.source)
  }
}
