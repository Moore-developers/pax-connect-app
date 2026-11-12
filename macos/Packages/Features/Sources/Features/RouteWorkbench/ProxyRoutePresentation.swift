import PaxCore

enum ProxyRoutePresentation {
  static func protocolLabel(_ node: NodeProfile) -> String {
    if node.protocolName == "socks" { return "SOCKS5" }
    if node.protocolName == "http" { return node.security == "tls" ? "HTTPS" : "HTTP" }
    return node.protocolName.uppercased()
  }
  static func transportNotice(_ node: NodeProfile) -> String? {
    guard ["socks", "http"].contains(node.protocolName) else { return nil }
    if node.security == "tls" { return "客户端到代理服务器使用 TLS；证书校验结果以证书设置及本次连接为准。" }
    return "代理连接本身未加密；HTTPS 网站内容仍由网站的 TLS 保护。"
  }
  static func connection(_ node: NodeProfile) -> String {
    if node.protocolName == "socks" { return "SOCKS5" }
    if node.protocolName == "http" { return node.security == "tls" ? "HTTPS · TLS" : "HTTP" }
    return [
      node.protocolName.uppercased(), node.transport.uppercased(), node.security.uppercased(),
    ]
    .filter { !$0.isEmpty }.joined(separator: " · ")
  }
}
