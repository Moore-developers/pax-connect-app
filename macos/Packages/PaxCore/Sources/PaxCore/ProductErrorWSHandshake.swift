import Foundation

extension ProductErrorPresentation {
  /// Cloudflare 520–530 是 CDN 报告的源站故障，属线路服务商服务端问题，不提示用户核对本地配置。
  static func wsHandshakeMessage(status: String?, context: PresentationContext = .chinese) -> String
  {
    guard let status, let code = Int(status), (520...530).contains(code) else {
      return context.text(
        "error.ws_handshake",
        [
          "status": status.map {
            context.language == .chinese ? "（HTTP " + $0 + "）" : " (HTTP " + $0 + ")"
          } ?? ""
        ])
    }
    let reason =
      switch code {
      case 521: context.text("error.ws_521")
      case 522: context.text("error.ws_522")
      case 523: context.text("error.ws_523")
      case 524: context.text("error.ws_524")
      case 525: context.text("error.ws_525")
      case 526: context.text("error.ws_526")
      case 530: context.text("error.ws_530")
      default: context.text("error.ws_unknown")
      }
    return context.text("error.ws_origin", ["code": String(code), "reason": reason])
  }
}
