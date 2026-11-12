import Foundation

/// 包类型的展示文案只有一处映射：同时给出详情副标题与包行摘要，
/// 新增包类型只改一个 case，不在渲染处各写一遍 switch。
extension RoutingWorkbenchStore.RulePackage {
  var kindPresentation: (subtitle: String, summary: String) {
    switch kind {
    case .priority: ("每条规则单独设置去向 · 优先于所有其他包", "固定 · 最高优先")
    case .local: ("自己维护 · 包内规则可增删改", "自己维护 · \(rules.count) 条规则")
    case .subscription: ("从链接订阅 · 匹配内容由来源维护", "订阅 · " + version)
    case .adblock: ("内置广告域名名单 · 仅阻断命中的新连接 · 用户规则优先", "内置 · " + version)
    case .geo: ("内置规则 · 作为默认分流依据", "内置 · " + version)
    }
  }
}
