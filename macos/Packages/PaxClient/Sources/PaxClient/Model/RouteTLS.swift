/// Saved policy is separate from the mode frozen for the active proxy connection.
public enum TLSMode: Sendable, Equatable {
  case strict, skipVerify, unknown
}
public struct RouteTLSPolicy: Sendable, Equatable {
  public let mode: TLSMode
  public let userOverride: Bool
  public let needsConfirmation: Bool
  public let identityToken: String
  public let revision: UInt64
  public init(
    mode: TLSMode, userOverride: Bool, needsConfirmation: Bool, identityToken: String,
    revision: UInt64
  ) {
    self.mode = mode
    self.userOverride = userOverride
    self.needsConfirmation = needsConfirmation
    self.identityToken = identityToken
    self.revision = revision
  }
}
public struct ActiveTLS: Sendable, Equatable {
  public let routeID: String
  public let identityToken: String
  public let mode: TLSMode
  public let verified: Bool
  public let skipped: Bool
  public init(routeID: String, identityToken: String, mode: TLSMode, verified: Bool, skipped: Bool)
  {
    self.routeID = routeID
    self.identityToken = identityToken
    self.mode = mode
    self.verified = verified
    self.skipped = skipped
  }
}
public struct RouteTLSIntent: Sendable {
  public let routeID: String
  public let identityToken: String
  public let expectedRevision: UInt64
  public let mode: TLSMode
  public let confirmSkip: Bool
  public init(route: NodeProfile, mode: TLSMode, confirmSkip: Bool) {
    routeID = route.id
    identityToken = route.tlsPolicy?.identityToken ?? ""
    expectedRevision = route.tlsPolicy?.revision ?? 0
    self.mode = mode
    self.confirmSkip = confirmSkip
  }
}
