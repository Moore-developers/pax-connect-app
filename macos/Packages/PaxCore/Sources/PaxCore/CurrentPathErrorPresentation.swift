import PaxClient

extension ProductErrorPresentation {
  static func currentPathError(_ error: ProductError) -> String? {
    switch error.code {
    case "current_channel_failed", "check_failed": return "当前代理通道未能完成连接，请重试验证或选择其它线路"
    case "mixed_listener_unreachable": return "当前代理入口无法连接，请检查内核运行状态"
    case "runtime_selection_mismatch": return "验证期间实际线路不匹配，请重新验证"
    case "check_inconclusive", "target_unconfirmed", "egress_unconfirmed": return "暂未确认当前通道可用，可重新验证"
    case "current_path_unavailable", "no_active_core": return "当前没有可验证的运行通道"
    case "physical_network_wait": return "本机网络尚未就绪，可在网络恢复后重新验证"
    case "device_sleeping": return "系统处于休眠状态，唤醒后再验证通道"
    case "runtime_binding_unconfirmed", "core_not_started": return "当前内核运行状态尚未确认，请检查代理状态"
    case "network_unconfirmed", "check_timeout": return "暂未确认当前通道连通，可重新验证"
    case "current_path_stale", "current_path_request_changed": return "运行通道已变化，请重新验证"
    default: return nil
    }
  }
}
