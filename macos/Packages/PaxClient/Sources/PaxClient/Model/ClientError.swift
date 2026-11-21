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
