import GRPCCore

/// 连接层可解释错误；UI 不接触 gRPC 或系统 Error。
public enum ClientError: Error, Sendable, Equatable {
  case proxyMustBeOff
  case installationRequired
  case installationInvalid
  case registrationFailed
  case serviceIdentityUnknown
  case updateFailed
  case unavailable
  case timeout
  case permissionDenied
  case protocolMismatch(daemon: UInt32?, client: UInt32)
  case invalidEvent
  case eventGap
  case unknown

  public var isRetryable: Bool {
    switch self {
    case .unavailable, .timeout, .eventGap: true
    case .permissionDenied, .protocolMismatch, .invalidEvent, .unknown, .installationRequired,
      .installationInvalid, .registrationFailed, .updateFailed, .serviceIdentityUnknown,
      .proxyMustBeOff:
      false
    }
  }

  public var message: String {
    switch self {
    case .proxyMustBeOff: "请先关闭代理，再更新后台服务"
    case .installationRequired: "请将 App 放入 /Applications/PaxConnect.app 后重新打开"
    case .installationInvalid: "App 的后台服务资源或签名无效，请重新构建或安装"
    case .registrationFailed: "登记后台服务失败，请检查系统设置后重试"
    case .serviceIdentityUnknown: "后台服务与当前 App 不匹配，请退出其它 Pax 实例或重新安装"
    case .updateFailed: "更新后台服务失败，请重试或重新安装"
    case .unavailable: "后台服务暂不可用"
    case .timeout: "连接后台服务超时"
    case .permissionDenied: "无权访问后台服务，请检查授权"
    case .protocolMismatch(let daemon, let client):
      "后台服务协议不兼容（App v\(client)，daemon \(daemon.map { String($0) } ?? "未知")），请更新或重新安装"
    case .invalidEvent: "后台服务返回了无效同步数据"
    case .eventGap: "状态同步中断，正在重新获取"
    case .unknown: "连接后台服务失败，请重试"
    }
  }

  static func classify(_ error: any Error) -> ClientError {
    if let error = error as? ClientError { return error }
    if let error = error as? RPCError {
      switch error.code {
      case .unavailable, .cancelled: return .unavailable
      case .deadlineExceeded: return .timeout
      case .permissionDenied, .unauthenticated: return .permissionDenied
      case .failedPrecondition:
        return .protocolMismatch(daemon: nil, client: DaemonConnection.protocolVersion)
      case .resourceExhausted: return .eventGap
      default: return .unknown
      }
    }
    return .unknown
  }
}
