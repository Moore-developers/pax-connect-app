import Foundation
import PaxProto

public struct SubscriptionAttempt: Sendable, Equatable {
  public let at: Date
  public let channel: String
  public let phase: String
  public let status: UInt32
  public let queueMS, dnsMS, connectMS, tlsMS, headersMS, bodyMS, totalMS: UInt64
  public let errorCode: String
  init(proto: Pax_V1_SubscriptionAttempt) {
    at = proto.at.date
    channel = proto.channel
    phase = proto.phase
    status = proto.httpStatus
    queueMS = proto.queueMs
    dnsMS = proto.dnsMs
    connectMS = proto.connectMs
    tlsMS = proto.tlsMs
    headersMS = proto.headersMs
    bodyMS = proto.bodyMs
    totalMS = proto.totalMs
    errorCode = proto.errorCode
  }
}
