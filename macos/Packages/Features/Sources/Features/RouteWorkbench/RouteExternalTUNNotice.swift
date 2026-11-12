import PaxClient

/// 线路工作台批量测试旁的第三方 TUN 提示；无第三方 TUN 时返回 nil，视图隐藏且不占位。
enum RouteExternalTUNNotice {
  static func text(_ tun: ExternalTUN?) -> String? {
    guard let tun, !tun.interface.isEmpty else { return nil }
    return "检测到第三方 TUN（\(tun.interface)）正在承接流量，测试结果可能经其转发，不代表直连"
  }
}
