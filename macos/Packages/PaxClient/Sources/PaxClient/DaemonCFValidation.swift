import Foundation
import PaxProto

/// A complete route validation; its token only authorizes saving the frozen draft.
public struct DaemonCFValidation: Sendable, Equatable {
  public let candidate: CFCandidate?
  public let validationID: String
  public let draftDigest: String
  public let network: String
  public let routeRevision: UInt64
  public let configRevision: UInt64
  public let expiresAt: Date?
  public let success: Bool

  init(_ value: Pax_V1_CFValidationResult) {
    candidate = value.hasCandidate ? CFCandidate(proto: value.candidate) : nil
    validationID = value.validationID
    draftDigest = value.draftDigest
    network = value.network
    routeRevision = value.routeRevision
    configRevision = value.configRevision
    expiresAt = value.hasExpiresAt ? value.expiresAt.date : nil
    success = value.success
  }
}
