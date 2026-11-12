import Foundation
import PaxProto

/// A separate proxy exit query; a latency failure cannot replace this result.
public struct RouteExitSummary: Sendable, Equatable {
  public let at: Date
  public let success: Bool
  public let testedConfigRevision: UInt64
  public let error: ProductError?
  public let ip: String
  public let location: ExitLocation?
  public let lastLocation: ExitLocation?
  /// 与 ProductError 的 messageParams 同义，如 ws 握手失败时的 HTTP 状态码。
  public let errorParams: [String]

  public init(
    at: Date, success: Bool, testedConfigRevision: UInt64, error: ProductError? = nil,
    ip: String = "", location: ExitLocation? = nil, lastLocation: ExitLocation? = nil,
    errorParams: [String] = []
  ) {
    self.at = at
    self.success = success
    self.testedConfigRevision = testedConfigRevision
    self.error = error
    self.ip = ip
    self.location = location
    self.lastLocation = lastLocation
    self.errorParams = errorParams
  }
}

extension RouteExitSummary {
  init(proto: Pax_V1_RouteExitSummary) {
    self.init(
      at: proto.hasAt ? proto.at.date : Date(timeIntervalSince1970: 0),
      success: proto.success, testedConfigRevision: proto.testedConfigRevision,
      error: proto.errorCode.isEmpty ? nil : ProductError(code: proto.errorCode),
      ip: proto.ip, location: proto.hasLocation ? ExitLocation(proto: proto.location) : nil,
      lastLocation: proto.hasLastLocation ? ExitLocation(proto: proto.lastLocation) : nil,
      errorParams: proto.errorParams)
  }
}
