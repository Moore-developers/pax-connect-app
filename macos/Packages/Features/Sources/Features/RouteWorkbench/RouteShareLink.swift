import CoreFoundation
import Darwin
import Foundation
import PaxCore

extension RouteWorkbenchStore {
  /// Read the saved effective node, including subscription overrides. Credentials stay in this call.
  func loadShareLink(_ id: String) async throws -> String {
    guard let frozen = node(id) else { throw EditError("线路不存在") }
    guard frozen.chain == nil else { throw EditError("组合线路不能导出为单节点分享链接") }
    guard let routes else {
      // Fixed fictional data makes the isolated demo explicit; never invent the real node's credentials.
      return
        "trojan://fictional-demo-password@demo.invalid:443?sni=demo.invalid#%E6%BC%94%E7%A4%BA%E7%BA%BF%E8%B7%AF"
    }
    let edit = try await routes.loadEditor(id)
    guard routes.connected, let current = node(id),
      current.revision == frozen.revision, current.configRevision == frozen.configRevision,
      current.tlsPolicy == frozen.tlsPolicy
    else { throw EditError("线路配置已变化，请重新打开分享") }
    return try RouteShareLink.make(edit: edit, node: current)
  }
}

/// A deliberately bounded inverse of daemon/internal/importers/ParseLink.
/// Unknown connection options fail closed instead of silently producing a different connection.
enum RouteShareLink {
  static func make(edit: RouteEdit, node: NodeProfile) throws -> String {
    guard node.chain == nil else { throw invalid("组合线路不能导出为单节点分享链接") }
    // configRevision is an opaque connection version (including resolved TLS), not a schema number.
    guard edit.routeID == node.id, edit.revision == node.revision,
      edit.configRevision != 0, edit.configRevision == node.configRevision,
      edit.name == node.name, edit.server == node.server, edit.port == node.port,
      edit.protocolName == node.protocolName, edit.transport == node.transport,
      edit.security == node.security, edit.serverName == node.serverName
    else { throw invalid("后台返回的线路版本不一致，请重新打开分享") }
    let mode: TLSMode
    if node.security == "tls" {
      guard let policy = node.tlsPolicy, policy.mode != .unknown, !policy.needsConfirmation else {
        throw invalid("线路的 TLS 验证策略尚未确认，暂时不能分享")
      }
      mode = policy.mode
    } else {
      mode = .strict
    }
    return try make(documentJSON: edit.documentJSON, name: node.name, tlsMode: mode, expected: node)
  }

  static func make(
    documentJSON: String, name: String, tlsMode: TLSMode, expected: NodeProfile? = nil
  ) throws -> String {
    var syntax = RouteDocumentSyntax(documentJSON)
    try syntax.validate()
    guard
      let object = try JSONSerialization.jsonObject(with: Data(documentJSON.utf8))
        as? [String: Any]
    else { throw invalid("节点配置格式无效") }
    let document = Object(object)
    let protocolName = try document.string("type", required: true)
    let credentials: Set<String>
    switch protocolName {
    case "vless": credentials = ["uuid", "flow"]
    case "vmess": credentials = ["uuid", "alter_id", "security"]
    case "trojan", "anytls": credentials = ["password"]
    case "hysteria2": credentials = ["password", "obfs"]
    case "shadowsocks": credentials = ["password", "method"]
    default: throw invalid("此协议暂不支持生成标准分享链接")
    }
    try document.allow(
      Set(["type", "tag", "server", "server_port", "tls", "transport"]).union(credentials))
    _ = try document.string("tag")
    let server = try document.string("server", required: true)
    let port = try document.integer("server_port", range: 1...65535, required: true)
    guard !name.isEmpty, name.utf8.count <= 128, !server.isEmpty, server.utf8.count <= 253,
      server.utf8.allSatisfy({ $0 < 128 }),
      !server.contains(where: { $0.isWhitespace || "/?#@[]".contains($0) })
    else { throw invalid("线路名称或服务器地址无法导出") }
    if server.contains(":") {
      var address = in6_addr()
      guard server.withCString({ inet_pton(AF_INET6, $0, &address) }) == 1 else {
        throw unsupported
      }
    }
    var query: [(String, String)] = []
    let tls = try TLS(document.object("tls"), mode: tlsMode)
    let transport = try Transport(document.object("transport"))
    if let expected {
      guard expected.protocolName == protocolName, expected.server == server,
        expected.port == UInt32(port), expected.security == tls.kind,
        expected.serverName == tls.serverName
          || (protocolName == "vmess" && tls.kind == "none" && expected.serverName == server),
        expected.transport == (protocolName == "hysteria2" ? "quic" : transport.kind)
      else { throw invalid("后台返回的节点配置与线路状态不一致，请重新打开分享") }
    }
    var user = ""
    var scheme = protocolName
    switch protocolName {
    case "vless", "vmess":
      user = try document.string("uuid", required: true)
      guard UUID(uuidString: user) != nil, user.count == 36 else { throw invalid("节点 UUID 无效") }
    default:
      user = try document.string("password", required: true)
      guard !user.isEmpty, user.utf8.count <= 4096 else { throw invalid("节点密码无效") }
    }
    switch protocolName {
    case "vless":
      let flow = try document.string("flow")
      guard
        (transport.kind == "tcp" && tls.kind == "reality"
          && ["", "xtls-rprx-vision"].contains(flow))
          || (["ws", "httpupgrade"].contains(transport.kind) && tls.kind == "tls" && flow.isEmpty)
      else { throw unsupported }
      query += [("type", transport.kind), ("security", tls.kind)]
      if !flow.isEmpty { query.append(("flow", flow)) }
      query += transport.query + tls.query
    case "vmess":
      guard ["ws", "httpupgrade"].contains(transport.kind), tls.kind != "reality" else {
        throw unsupported
      }
      let aid = try document.integer("alter_id", range: 0...65535)
      let cipher = try document.string("security", fallback: "auto")
      var fields: [String: String] = [
        "v": "2", "ps": name, "add": server, "port": String(port), "id": user,
        "aid": String(aid), "scy": cipher, "net": transport.kind, "type": "none",
        "host": transport.host, "path": transport.path,
        "tls": tls.kind == "tls" ? "tls" : "", "sni": tls.serverName,
      ]
      for (key, value) in tls.query {
        switch key {
        case "insecure": fields["allowInsecure"] = value
        case "fp", "alpn": fields[key] = value
        default: break
        }
      }
      let data = try JSONSerialization.data(withJSONObject: fields, options: [.sortedKeys])
      return try bounded("vmess://" + data.base64EncodedString())
    case "trojan":
      guard tls.kind == "tls", ["tcp", "ws", "httpupgrade"].contains(transport.kind) else {
        throw unsupported
      }
      query = [("type", transport.kind)] + transport.query + tls.query
    case "anytls":
      guard tls.kind == "tls", transport.kind == "tcp" else { throw unsupported }
      query = tls.query
    case "hysteria2":
      guard tls.kind == "tls", !document.contains("transport"), tls.fingerprint.isEmpty else {
        throw unsupported
      }
      query = tls.query
      if let obfs = try document.object("obfs") {
        try obfs.allow(["type", "password"])
        guard try obfs.string("type", required: true) == "salamander" else { throw unsupported }
        let password = try obfs.string("password", required: true)
        guard !password.isEmpty else { throw unsupported }
        query += [("obfs", "salamander"), ("obfs-password", password)]
      }
    case "shadowsocks":
      guard tls.kind == "none", transport.kind == "tcp" else { throw unsupported }
      let method = try document.string("method", required: true)
      try validateShadowsocks(method: method, password: user)
      user = Data((method + ":" + user).utf8).base64EncodedString()
      scheme = "ss"
    default: throw unsupported
    }
    // Encode each component from UTF-8 using only RFC 3986 unreserved bytes. In particular,
    // a password's ':' must stay inside username, and '+' must not become a query space.
    let host = server.contains(":") ? "[" + server + "]" : encode(server)
    let parameters = query.map { encode($0.0) + "=" + encode($0.1) }.joined(separator: "&")
    let link =
      scheme + "://" + encode(user) + "@" + host + ":" + String(port)
      + (parameters.isEmpty ? "" : "?" + parameters) + "#" + encode(name)
    return try bounded(link)
  }

  private static func validateShadowsocks(method: String, password: String) throws {
    switch method {
    case "aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305": return
    case "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305":
      let size = method == "2022-blake3-aes-128-gcm" ? 16 : 32
      guard
        password.split(separator: ":", omittingEmptySubsequences: false).allSatisfy({
          Data(base64Encoded: String($0))?.count == size
        })
      else { throw invalid("Shadowsocks 2022 密钥格式无效") }
    default: throw unsupported
    }
  }

  private static func encode(_ value: String) -> String {
    value.utf8.map { byte in
      if (65...90).contains(byte) || (97...122).contains(byte) || (48...57).contains(byte)
        || [45, 46, 95, 126].contains(byte)
      {
        return String(UnicodeScalar(byte))
      }
      return String(format: "%%%02X", byte)
    }.joined()
  }
  private static func bounded(_ link: String) throws -> String {
    guard link.utf8.count <= 32 * 1024 else { throw invalid("分享链接超过导入器的 32 KiB 限制") }
    return link
  }
  fileprivate static var unsupported: RouteWorkbenchStore.EditError {
    invalid("此节点含标准链接无法完整表达的连接选项，暂时不能分享")
  }
  fileprivate static func invalid(_ message: String) -> RouteWorkbenchStore.EditError {
    RouteWorkbenchStore.EditError(message)
  }

  private struct Transport {
    var kind = "tcp"
    var host = ""
    var path = "/"
    var query: [(String, String)] {
      kind == "tcp" ? [] : [("host", host), ("path", path)]
    }
    init(_ object: Object?) throws {
      guard let object else { return }
      kind = try object.string("type", required: true)
      switch kind {
      case "ws":
        try object.allow(["type", "path", "headers"])
        if let headers = try object.object("headers") {
          try headers.allow(["Host"])
          host = try headers.string("Host")
        }
      case "httpupgrade":
        try object.allow(["type", "path", "host"])
        host = try object.string("host")
      default: throw unsupported
      }
      path = try object.string("path", fallback: "/")
      if path.isEmpty { path = "/" }
      guard path.utf8.count <= 2048 else { throw unsupported }
    }
  }

  private struct TLS {
    var kind = "none"
    var serverName = ""
    var fingerprint = ""
    var query: [(String, String)] = []
    init(_ object: Object?, mode: TLSMode) throws {
      guard let object else { return }
      guard try object.bool("enabled") else {
        try object.allow(["enabled"])
        return
      }
      try object.allow(["enabled", "server_name", "insecure", "alpn", "utls", "reality"])
      kind = "tls"
      serverName = try object.string("server_name", required: true)
      guard !serverName.isEmpty, serverName.utf8.count <= 253 else { throw unsupported }
      query.append(("sni", serverName))
      // Source suggestion is replaced by the resolved saved policy.
      _ = try object.bool("insecure")
      if let value = object.values["alpn"] {
        guard let alpn = value as? [String], alpn.allSatisfy({ !$0.isEmpty && !$0.contains(",") })
        else { throw unsupported }
        if !alpn.isEmpty { query.append(("alpn", alpn.joined(separator: ","))) }
      }
      if let utls = try object.object("utls") {
        try utls.allow(["enabled", "fingerprint"])
        if try utls.bool("enabled") {
          fingerprint = try utls.string("fingerprint", required: true)
          guard !fingerprint.isEmpty else { throw unsupported }
          query.append(("fp", fingerprint))
        } else if utls.contains("fingerprint") {
          throw unsupported
        }
      }
      if let reality = try object.object("reality") {
        try reality.allow(["enabled", "public_key", "short_id"])
        if try reality.bool("enabled") {
          kind = "reality"
          let publicKey = try reality.string("public_key", required: true)
          let shortID = try reality.string("short_id")
          let base64 = publicKey.replacingOccurrences(of: "-", with: "+")
            .replacingOccurrences(of: "_", with: "/")
          guard publicKey.utf8.count == 43, Data(base64Encoded: base64 + "=")?.count == 32,
            shortID.count <= 16, shortID.count.isMultiple(of: 2),
            shortID.utf8.allSatisfy({
              (48...57).contains($0) || (65...70).contains($0) || (97...102).contains($0)
            }),
            !fingerprint.isEmpty
          else { throw unsupported }
          query += [("pbk", publicKey), ("sid", shortID)]
        } else if reality.contains("public_key") || reality.contains("short_id") {
          throw unsupported
        }
      }
      if kind == "tls" {
        guard mode != .unknown else { throw invalid("无法确定线路的 TLS 验证策略") }
        if mode == .skipVerify { query.append(("insecure", "1")) }
      } else if mode == .skipVerify {
        throw unsupported
      }
    }
  }

  fileprivate struct Object {
    let values: [String: Any]
    init(_ values: [String: Any]) { self.values = values }
    func contains(_ key: String) -> Bool { values[key] != nil }
    func allow(_ keys: Set<String>) throws {
      guard Set(values.keys).isSubset(of: keys) else { throw unsupported }
    }
    func string(_ key: String, fallback: String = "", required: Bool = false) throws -> String {
      guard let value = values[key] else {
        if required { throw unsupported }
        return fallback
      }
      guard let result = value as? String else { throw unsupported }
      return result
    }
    func integer(_ key: String, range: ClosedRange<Int>, required: Bool = false) throws -> Int {
      guard let value = values[key] else {
        if required { throw unsupported }
        return 0
      }
      guard let number = value as? NSNumber, CFGetTypeID(number) != CFBooleanGetTypeID(),
        number.doubleValue == Double(number.intValue), range.contains(number.intValue)
      else { throw unsupported }
      return number.intValue
    }
    func bool(_ key: String) throws -> Bool {
      guard let value = values[key] else { return false }
      guard let number = value as? NSNumber, CFGetTypeID(number) == CFBooleanGetTypeID() else {
        throw unsupported
      }
      return number.boolValue
    }
    func object(_ key: String) throws -> Object? {
      guard let value = values[key] else { return nil }
      guard let result = value as? [String: Any] else { throw unsupported }
      return Object(result)
    }
  }
}
