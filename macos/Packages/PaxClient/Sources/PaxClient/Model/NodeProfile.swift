import Foundation

/// 一条线路（对应 proto 的 `NodeProfile`；名称与 Go 端一致，见 code-standards.md §二）。
///
/// 不含凭据：UUID / 密码等只存在于 daemon 侧，永不进入本类型。
public struct NodeProfile: Sendable, Equatable, Identifiable {
  public let id: String
  public let name: String
  /// 协议标签：vless / vmess / trojan / shadowsocks。
  public let protocolName: String
  public let server: String
  public let port: UInt32
  public let group: String
  public let serverName: String
  public let configRevision: UInt64
  public let revision: UInt64
  public let transport: String
  public let security: String
  public let sourceID: String
  /// local / subscription；本地线路为空或 local。
  public let origin: String
  /// 用户删除的订阅线路：仍在目录中，可在已隐藏列表恢复。
  public let hidden: Bool
  /// 提供方已移除但记录仍在（或本轮无法确认）；界面须显式提示。
  public let sourceRemoved: Bool
  /// 最近一次手动连通性测试；无测试时为空。过期由 testedRevision 与当前 revision 对比判定。
  public let testSummary: RouteTestSummary?
  public let exitSummary: RouteExitSummary?
  public let downloadSummary: DownloadTestSummary?
  public let cfEntry: Bool
  public let chain: ChainRoute?
  public let tlsPolicy: RouteTLSPolicy?

  public init(
    id: String, name: String, protocolName: String, server: String, port: UInt32, group: String,
    serverName: String = "", tlsPolicy: RouteTLSPolicy? = nil, revision: UInt64 = 0,
    transport: String = "", security: String = "tls", sourceID: String = "",
    origin: String = "", hidden: Bool = false, sourceRemoved: Bool = false,
    testSummary: RouteTestSummary? = nil, configRevision: UInt64 = 0,
    downloadSummary: DownloadTestSummary? = nil, exitSummary: RouteExitSummary? = nil,
    cfEntry: Bool = false, chain: ChainRoute? = nil
  ) {
    self.id = id
    self.name = name
    self.protocolName = protocolName
    self.server = server
    self.port = port
    self.group = group
    self.serverName = serverName
    self.tlsPolicy = tlsPolicy
    self.configRevision = configRevision
    self.revision = revision
    self.transport = transport
    self.security = security
    self.sourceID = sourceID
    self.origin = origin
    self.hidden = hidden
    self.sourceRemoved = sourceRemoved
    self.testSummary = testSummary
    self.downloadSummary = downloadSummary
    self.exitSummary = exitSummary
    self.cfEntry = cfEntry
    self.chain = chain
  }
}

/// 一次手动连通性测试的结果：受控目标的实测连接，不是 ICMP 或带宽测速。
public struct RouteTestSummary: Sendable, Equatable {
  public let exitLocation: ExitLocation?
  public let lastExitLocation: ExitLocation?
  public let stableMS: UInt32
  public let stableSamples: UInt32
  public let stableError: String
  public let testTarget: String
  public let coldMS: UInt32
  public let secondMS: UInt32
  public let reused: Bool
  public let method: String
  public let firstError: String
  public let secondError: String
  public let at: Date
  public let success: Bool
  /// 兼容旧单值；v3界面分别使用coldMS/stableMS和采样数。
  public let durationMS: UInt32
  /// success=false 时的受控失败码。
  public let error: ProductError?
  /// 测量时的配置 revision；与当前 revision 不同即显示过期。
  public let testedRevision: UInt64
  public let testedConfigRevision: UInt64
  public let exitIP: String
  public let exitError: ProductError?

  public init(
    at: Date, success: Bool, durationMS: UInt32, error: ProductError? = nil,
    testedRevision: UInt64 = 0, testedConfigRevision: UInt64 = 0, exitIP: String = "",
    exitError: ProductError? = nil, coldMS: UInt32 = 0, secondMS: UInt32 = 0,
    reused: Bool = false, method: String = "", firstError: String = "", secondError: String = "",
    stableMS: UInt32 = 0, stableSamples: UInt32 = 0, stableError: String = "",
    testTarget: String = "", exitLocation: ExitLocation? = nil,
    lastExitLocation: ExitLocation? = nil
  ) {
    self.exitLocation = exitLocation
    self.lastExitLocation = lastExitLocation
    self.stableMS = stableMS
    self.stableSamples = stableSamples
    self.stableError = stableError
    self.testTarget = testTarget
    self.at = at
    self.coldMS = coldMS
    self.secondMS = secondMS
    self.reused = reused
    self.method = method
    self.firstError = firstError
    self.secondError = secondError
    self.success = success
    self.durationMS = durationMS
    self.error = error
    self.testedRevision = testedRevision
    self.testedConfigRevision = testedConfigRevision
    self.exitIP = exitIP
    self.exitError = exitError
  }
}

public struct DownloadTestSummary: Sendable, Equatable {
  public let durationMS: UInt32
  public let method: String
  public let parallel: UInt32
  public let setupMS: UInt32
  public let endReason: String
  public let at: Date
  public let success: Bool
  public let bytesPerSecond: Double
  public let error: ProductError?
  public let testedConfigRevision: UInt64
  public let bytes: UInt64
  public init(
    at: Date, success: Bool, bytesPerSecond: Double, error: ProductError? = nil,
    testedConfigRevision: UInt64, bytes: UInt64, method: String = "", parallel: UInt32 = 1,
    setupMS: UInt32 = 0, endReason: String = "", durationMS: UInt32 = 0
  ) {
    self.at = at
    self.durationMS = durationMS
    self.method = method
    self.parallel = parallel
    self.setupMS = setupMS
    self.endReason = endReason
    self.success = success
    self.bytesPerSecond = bytesPerSecond
    self.error = error
    self.testedConfigRevision = testedConfigRevision
    self.bytes = bytes
  }
}
