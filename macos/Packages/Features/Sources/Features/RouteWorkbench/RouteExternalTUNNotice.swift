import PaxClient
import PaxCore

/// 线路工作台批量测试旁的第三方 TUN 提示；无第三方 TUN 时返回 nil，视图隐藏且不占位。
enum RouteExternalTUNNotice {
  static func text(_ tun: ExternalTUN?, context: PresentationContext = .chinese) -> String? {
    guard let tun, !tun.interface.isEmpty else { return nil }
    return context.text("routes.external_tun", ["interface": tun.interface])
  }
}
