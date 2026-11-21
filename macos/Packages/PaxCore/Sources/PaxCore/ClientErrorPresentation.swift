import PaxClient

extension ProductErrorPresentation {
  static func message(_ error: ClientError, context: PresentationContext) -> String {
    ProductTextPresentation.text(
      clientMeaning(error), original: context.text("client.unknown"), context: context)
  }
  static func clientMeaning(_ error: ClientError) -> ProductText {
    switch error {
    case .proxyMustBeOff: ProductText(code: "client.proxyMustBeOff")
    case .installationRequired: ProductText(code: "client.installationRequired")
    case .installationInvalid: ProductText(code: "client.installationInvalid")
    case .registrationFailed: ProductText(code: "client.registrationFailed")
    case .serviceIdentityUnknown: ProductText(code: "client.serviceIdentityUnknown")
    case .updateFailed: ProductText(code: "client.updateFailed")
    case .unavailable: ProductText(code: "client.unavailable")
    case .timeout: ProductText(code: "client.timeout")
    case .permissionDenied: ProductText(code: "client.permissionDenied")
    case .protocolMismatch(let daemon, let client):
      ProductText(
        code: "client.protocolMismatch",
        params: ["client": String(client), "daemon": daemon.map(String.init) ?? ""])
    case .invalidEvent: ProductText(code: "client.invalidEvent")
    case .eventGap: ProductText(code: "client.eventGap")
    case .unknown: ProductText(code: "client.unknown")
    }
  }

}

extension ClientError {
  public var message: String { ProductErrorPresentation.message(self, context: .chinese) }
}
