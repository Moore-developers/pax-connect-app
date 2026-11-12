import PaxProto

public struct NetworkPrivacy: Sendable, Equatable {
  public var revision: UInt64 = 1
  public var dnsMode = "balanced"
  public var webRTC = false
  public init() {}
  init(proto: Pax_V1_NetworkPrivacy) {
    revision = proto.revision
    dnsMode = proto.dnsMode
    webRTC = proto.webrtc
  }
  var proto: Pax_V1_NetworkPrivacy {
    .with {
      $0.revision = revision
      $0.dnsMode = dnsMode
      $0.webrtc = webRTC
    }
  }
}
