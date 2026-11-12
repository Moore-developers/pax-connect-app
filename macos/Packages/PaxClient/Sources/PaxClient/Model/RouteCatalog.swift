public struct RoutesPreview: Sendable {
  public let token: String
  public let revision: UInt64
  public let items: [RoutePreviewItem]
  /// 订阅预览的来源标识：脱敏 hostname 与用户来源名，绝不含 URL。
  public let sourceHost: String
  public let sourceName: String
  /// 预览对应的已保存来源；首次导入为空。
  public let subscriptionID: String
  public init(
    token: String, revision: UInt64, items: [RoutePreviewItem],
    sourceHost: String = "", sourceName: String = "", subscriptionID: String = ""
  ) {
    self.token = token
    self.revision = revision
    self.items = items
    self.sourceHost = sourceHost
    self.sourceName = sourceName
    self.subscriptionID = subscriptionID
  }
}
public struct RoutePreviewItem: Sendable, Identifiable {
  public let id: String
  public let route: NodeProfile?
  public let errorCode: String
  public let duplicate: Bool
  public let line: UInt32
  /// 该节点在当前来源中已被用户隐藏：默认不勾选，可在已隐藏列表恢复。
  public let hidden: Bool
  public let displayName: String
  public let protocolName: String
  public init(
    id: String, route: NodeProfile?, errorCode: String, duplicate: Bool, line: UInt32,
    hidden: Bool = false, displayName: String = "", protocolName: String = ""
  ) {
    self.id = id
    self.route = route
    self.errorCode = errorCode
    self.duplicate = duplicate
    self.line = line
    self.hidden = hidden
    self.displayName = displayName
    self.protocolName = protocolName
  }
}
public struct RouteEdit: Sendable {
  public var documentJSON = ""
  public var requestID = ""
  public var configRevision: UInt64 = 0
  public var routeID = ""
  public var revision: UInt64 = 0
  public var name = ""
  public var protocolName = "hysteria2"
  public var server = ""
  public var port: UInt32 = 443
  public var transport = "quic"
  public var security = "tls"
  public var serverName = ""
  public var uuid = ""
  public var password = ""
  public var replaceCredentials = false
  public var wsHost = ""
  public var wsPath = "/"
  public var alpn: [String] = []
  public var fingerprint = ""
  public var realityPublicKey = ""
  public var realityShortID = ""
  public var flow = ""
  public var alterID: UInt32 = 0
  public var vmessSecurity = "auto"
  public var obfsPassword = ""
  public var skipSuggested = false
  public var credentialsSet = false
  public init() {}
}
