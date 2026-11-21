import Foundation

extension AIProfilesStore {
  public static func stateLabel(_ binding: AIBinding?) -> String {
    stateLabel(binding, context: .chinese)
  }
  public static func stateLabel(_ b: AIBinding?, context: PresentationContext) -> String {
    guard let b else { return context.text("profile_state.not_imported") }
    switch b.state {
    case "applied": return context.text("profile_state.applied")
    case "pending": return context.text("profile_state.pending")
    case "changed": return context.text("profile_state.changed")
    case "ready": return context.text("profile_state.ready")
    case "missing": return context.text("profile_state.missing")
    case "account": return context.text("profile_state.account")
    case "invalid": return context.text("profile_state.invalid")
    case "read_only": return context.text("profile_state.read_only")
    case "restored": return context.text("profile_state.restored")
    case "recovery_required": return context.text("profile_state.recovery_required")
    default:
      return b.reason == "ai_template_unsupported"
        ? context.text("profile_state.unsupported") : context.text("profile_state.unconfigured")
    }
  }
  static func failureMeaning(_ error: any Error) -> ProductText {
    if let failure = error as? ProductError {
      let code = failure.code == "ai_commit_uncertain" ? "ai_recovery_required" : failure.code
      let known = [
        "ai_profile_invalid", "ai_profile_conflict", "ai_profile_in_use", "ai_target_changed",
        "ai_template_unsupported", "ai_target_unsafe", "ai_plan_expired", "ai_recovery_required",
      ]
      return ProductText(code: "profile_error." + (known.contains(code) ? code : "storage"))
    }
    if let client = error as? ClientError { return ProductErrorPresentation.clientMeaning(client) }
    return ProductText(code: "profile_error.service")
  }
  static func message(_ error: any Error) -> String {
    let meaning = failureMeaning(error)
    return PresentationContext.chinese.text(meaning.code, meaning.params)
  }
}
