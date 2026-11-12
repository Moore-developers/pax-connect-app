import Foundation

extension ProductErrorPresentation {
  /// Cloudflare 520–530 是 CDN 报告的源站故障，属线路服务商服务端问题，不提示用户核对本地配置。
  static func wsHandshakeMessage(status: String?) -> String {
    guard let status, let code = Int(status), (520...530).contains(code) else {
      return "WS 握手失败\(status.map { "（HTTP \($0)）" } ?? "")，请核对 Host、路径与服务端"
    }
    let reason =
      switch code {
      case 521: "源站拒绝连接"
      case 522: "源站连接超时"
      case 523: "源站不可达"
      case 524: "源站响应超时"
      case 525: "源站 TLS 握手失败"
      case 526: "源站证书无效"
      case 530: "源站不可用"
      default: "源站故障"
      }
    return "服务端源站故障（HTTP \(code) · \(reason)），Cloudflare 报告线路服务商的服务器异常，非本地配置问题；请换线路或联系服务商"
  }
}
