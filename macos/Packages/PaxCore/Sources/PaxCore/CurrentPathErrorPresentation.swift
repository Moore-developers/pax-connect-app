import PaxClient

extension ProductErrorPresentation {
  static func currentPathError(_ error: ProductError, context: PresentationContext) -> String? {
    switch error.code {
    case "current_channel_failed", "check_failed":
      return context.text("error.current_channel_failed")
    case "mixed_listener_unreachable": return context.text("error.mixed_listener_unreachable")
    case "runtime_selection_mismatch": return context.text("error.runtime_selection_mismatch")
    case "check_inconclusive", "target_unconfirmed", "egress_unconfirmed":
      return context.text("error.check_inconclusive")
    case "current_path_unavailable", "no_active_core":
      return context.text("error.current_path_unavailable")
    case "physical_network_wait": return context.text("error.physical_network_wait")
    case "device_sleeping": return context.text("error.device_sleeping")
    case "runtime_binding_unconfirmed", "core_not_started":
      return context.text("error.runtime_binding_unconfirmed")
    case "network_unconfirmed", "check_timeout": return context.text("error.network_unconfirmed")
    case "current_path_stale", "current_path_request_changed":
      return context.text("error.current_path_stale")
    default: return nil
    }
  }
}
