import PaxClient

extension AIUsageRequest {
  public func sourceLabel(context: PresentationContext) -> String {
    if client.isUsageOnly { return context.text("profile_state.read_only") }
    if !profileName.isEmpty { return profileName }
    return serviceSource.label(context: context)
  }
}
