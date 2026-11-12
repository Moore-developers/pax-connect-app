import Foundation
import PaxProto

/// 非 Pax 的 utun 接管了公网默认路由时的观测结果。
public struct ExternalTUN: Sendable, Equatable {
  /// 接口名，如 utun6。
  public let interface: String
  /// 被承接的路由目标，如 128.0/1。
  public let target: String
  /// 观测时间。
  public let at: Date

  public init(interface: String, target: String, at: Date) {
    self.interface = interface
    self.target = target
    self.at = at
  }
}

extension ExternalTUN {
  init(proto: Pax_V1_ExternalTUN) {
    self.init(
      interface: proto.interface, target: proto.target,
      at: proto.hasAt ? proto.at.date : Date(timeIntervalSince1970: 0))
  }
}
