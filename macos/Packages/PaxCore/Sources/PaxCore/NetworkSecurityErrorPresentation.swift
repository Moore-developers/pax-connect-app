import PaxClient

extension ProductErrorPresentation {
  static func networkSecurityMessage(_ error: ProductError, context: PresentationContext) -> String?
  {
    switch error.code {
    case "environment_browser_open_failed": context.text("error.environment_browser_open_failed")
    case "measurement_busy": context.text("error.measurement_busy")
    case "path_diagnostics_busy": context.text("error.path_diagnostics_busy")
    case "tls_unverified": context.text("error.tls_unverified")
    case "tls_certificate_invalid":
      switch error.messageParams.first {
      case "expired": context.text("error.expired")
      case "hostname_mismatch": context.text("error.hostname_mismatch")
      case "untrusted": context.text("error.untrusted")
      case "noncompliant": context.text("error.noncompliant")
      default: context.text("error.tls_verification_failed")
      }
    case "tls_confirmation_required": context.text("error.tls_confirmation_required")
    case "tls_policy_stale": context.text("error.tls_policy_stale")
    case "tls_policy_store_invalid": context.text("error.tls_policy_store_invalid")
    case "tls_policy_save_failed": context.text("error.tls_policy_save_failed")
    case "network_privacy_invalid": context.text("error.network_privacy_invalid")
    case "network_privacy_unavailable": context.text("error.network_privacy_unavailable")
    case "network_privacy_conflict": context.text("error.network_privacy_conflict")
    case "network_privacy_save_failed": context.text("error.network_privacy_save_failed")
    default: nil
    }
  }
}
