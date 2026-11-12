import Foundation

/// 流量快照（对应 proto 的 `TrafficSnapshot`）：今日累计值。
///
/// 口径（decisions/traffic-accounting.md、spec.md「流量与 Dashboard」）：
/// proxied + direct + unclassified == 总流量；未归类计入总量但不算进代理/直连。
public struct TrafficSnapshot: Sendable, Equatable {
  public var asOf: Date? = nil
  public var activeConnectionsKnown = false
  public var partial = false
  public var collectionState = "idle"
  public var uploadBytes: UInt64
  public var downloadBytes: UInt64
  public var proxiedBytes: UInt64
  public var directBytes: UInt64
  public var unclassifiedBytes: UInt64
  /// 连接数；核心事件不可用时为 0，UI 据此隐藏该项。
  public var activeConnections: UInt64

  public static let zero = TrafficSnapshot(
    uploadBytes: 0, downloadBytes: 0, proxiedBytes: 0,
    directBytes: 0, unclassifiedBytes: 0, activeConnections: 0)

  public var totalBytes: UInt64 { uploadBytes + downloadBytes }

  public init(
    uploadBytes: UInt64,
    downloadBytes: UInt64,
    proxiedBytes: UInt64,
    directBytes: UInt64,
    unclassifiedBytes: UInt64,
    activeConnections: UInt64
  ) {
    self.uploadBytes = uploadBytes
    self.downloadBytes = downloadBytes
    self.proxiedBytes = proxiedBytes
    self.directBytes = directBytes
    self.unclassifiedBytes = unclassifiedBytes
    self.activeConnections = activeConnections
  }
}
