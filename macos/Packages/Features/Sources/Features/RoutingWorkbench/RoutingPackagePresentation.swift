import Foundation
import PaxCore

/// 包类型的展示文案只有一处映射：同时给出详情副标题与包行摘要，
/// 新增包类型只改一个 case，不在渲染处各写一遍 switch。
extension RoutingWorkbenchStore.RulePackage {
  func displayName(context: PresentationContext) -> String {
    let keys: [RoutingWorkbenchStore.PackageKind: String] = [
      .priority: "priority", .geo: "geo", .adblock: "adblock", .claude: "claude",
    ]
    return keys[kind].map { context.text("rules.package." + $0) } ?? name
  }
  var kindPresentation: (subtitle: String, summary: String) { kindPresentation(context: .chinese) }
  func kindPresentation(context: PresentationContext) -> (subtitle: String, summary: String) {
    switch kind {
    case .priority:
      (context.text("routing_copy.b31b7c4bc4"), context.text("routing_copy.e30c829d3e"))
    case .local:
      (
        context.text("routing_copy.8eb8378a49"),
        context.text("routing_copy.d461bf4f62", ["arg0": String(describing: rules.count)])
      )
    case .subscription:
      (context.text("routing_copy.a44df5b18c"), context.text("routing_copy.aef0eb612a") + version)
    case .adblock:
      (context.text("routing_copy.4eb8d4b047"), context.text("routing_copy.c2703ab2aa") + version)
    case .claude:
      (
        context.text("routing_copy.e40a8313b0"),
        context.text("routing_copy.350c0e1fd0", ["arg0": String(describing: rules.count)])
      )
    case .geo:
      (context.text("routing_copy.47a81d6b47"), context.text("routing_copy.c2703ab2aa") + version)
    }
  }
}
