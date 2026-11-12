import Darwin
import Foundation

/// A single purchased proxy. Parsing never connects, saves, or infers a protocol from a port.
public struct ProxyRouteDraft: Equatable, Sendable {
  public enum Kind: String, CaseIterable, Sendable {
    case socks5 = "SOCKS5"
    case http = "HTTP"
    case https = "HTTPS"
  }
  public var kind: Kind = .socks5
  public var server = ""
  public var port = ""
  public var authenticated = true
  public var username = ""
  public var password = ""
  public var name = ""
  public var serverName = ""
  public var needsProtocolConfirmation = false
  private var original: [String: JSONValue] = [:]

  public init() {}

  public init(document: String, name: String) throws {
    let data = Data(document.utf8)
    original = try JSONDecoder().decode([String: JSONValue].self, from: data)
    guard let object = try JSONSerialization.jsonObject(with: data) as? [String: Any],
      let type = object["type"] as? String, ["socks", "http"].contains(type)
    else { throw Validation("这不是可编辑的 SOCKS5、HTTP 或 HTTPS 线路") }
    let tls = object["tls"] as? [String: Any]
    kind = type == "socks" ? .socks5 : tls?["enabled"] as? Bool == true ? .https : .http
    server = object["server"] as? String ?? ""
    port = (object["server_port"] as? NSNumber)?.stringValue ?? ""
    username = object["username"] as? String ?? ""
    password = object["password"] as? String ?? ""
    authenticated = !username.isEmpty || !password.isEmpty
    self.name = name
    serverName = tls?["server_name"] as? String ?? ""
    if serverName == server { serverName = "" }
  }

  public var displayName: String {
    let entered = name.trimmingCharacters(in: .whitespacesAndNewlines)
    return entered.isEmpty ? "\(kind.rawValue) · \(host):\(port)" : entered
  }
  private var host: String {
    let value = server.trimmingCharacters(in: .whitespacesAndNewlines)
    return value.hasPrefix("[") && value.hasSuffix("]")
      ? String(value.dropFirst().dropLast()) : value
  }
  public func document() throws -> String {
    guard !needsProtocolConfirmation else { throw Validation("请选择代理类型，不能根据端口判断协议") }
    guard Self.validHost(host) else { throw Validation("请填写有效的 IP 或域名，不包含协议、端口或路径") }
    guard let number = UInt16(port), number > 0 else { throw Validation("端口须为 1–65535 的整数") }
    guard displayName.utf8.count <= 128 else { throw Validation("线路名称过长，请填写一个较短的名称") }
    if authenticated {
      guard !username.isEmpty, !password.isEmpty else { throw Validation("请填写用户名和密码，或选择无需账号密码") }
      let limit = kind == .socks5 ? 255 : 4096
      guard username.utf8.count <= limit, password.utf8.count <= limit,
        kind == .socks5 || !username.contains(":"), !username.contains(where: \.isNewline),
        !password.contains(where: \.isNewline)
      else { throw Validation("认证信息格式无效或超出协议长度限制") }
    }
    let sni = serverName.trimmingCharacters(in: .whitespacesAndNewlines)
    guard kind != .https || sni.isEmpty || Self.validHost(sni) else {
      throw Validation("服务器名称须为域名或 IP")
    }
    var object = original
    object["type"] = .string(kind == .socks5 ? "socks" : "http")
    object["tag"] = .string(displayName)
    object["server"] = .string(host)
    object["server_port"] = .number(Int(number))
    object["version"] = kind == .socks5 ? .string("5") : nil
    object["username"] = authenticated ? .string(username) : nil
    object["password"] = authenticated ? .string(password) : nil
    if kind == .https {
      var tls: [String: JSONValue] = [:]
      if case .object(let saved) = original["tls"] { tls = saved }
      tls["enabled"] = .bool(true)
      tls["server_name"] = .string(sni.isEmpty ? host : sni)
      object["tls"] = .object(tls)
    } else {
      object["tls"] = nil
    }
    let encoder = JSONEncoder()
    encoder.outputFormatting = [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]
    return String(decoding: try encoder.encode(object), as: UTF8.self)
  }

  public static func pasted(_ text: String, keeping draft: Self) throws -> Self {
    let input = text.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !input.isEmpty, !input.contains(where: \.isNewline) else {
      throw Validation("请一次粘贴一条代理信息")
    }
    var value = draft
    if input.contains("://") {
      guard let url = URLComponents(string: input), let scheme = url.scheme?.lowercased(),
        let kind = Kind.allCases.first(where: { $0.rawValue.lowercased() == scheme }),
        let address = url.host, let port = url.port,
        url.path.isEmpty || url.path == "/", url.query == nil
      else { throw Validation("仅支持带端口的 socks5://、http://、https:// 单条代理链接") }
      value.kind = kind
      value.server = address
      value.port = String(port)
      value.username = url.user ?? ""
      value.password = url.password ?? ""
      value.authenticated = url.user != nil || url.password != nil
      value.serverName = ""
      if let name = url.fragment { value.name = name }
      value.needsProtocolConfirmation = false
    } else {
      let address: String
      let remainder: Substring
      if input.hasPrefix("["), let end = input.firstIndex(of: "]") {
        address = String(input[input.index(after: input.startIndex)..<end])
        let after = input.index(after: end)
        guard after < input.endIndex, input[after] == ":" else { throw Validation("IPv6 地址后须填写端口") }
        remainder = input[input.index(after: after)...]
      } else {
        guard let separator = input.firstIndex(of: ":") else {
          throw Validation("请粘贴地址:端口，或地址:端口:用户名:密码")
        }
        address = String(input[..<separator])
        remainder = input[input.index(after: separator)...]
      }
      let parts = remainder.split(separator: ":", maxSplits: 2, omittingEmptySubsequences: false)
      guard parts.count == 1 || parts.count == 3 else {
        throw Validation("格式不明确，请分项填写；IPv6 请使用方括号")
      }
      value.server = address
      value.port = String(parts[0])
      value.username = parts.count == 3 ? String(parts[1]) : ""
      value.password = parts.count == 3 ? String(parts[2]) : ""
      value.authenticated = parts.count == 3
      value.serverName = ""
      value.needsProtocolConfirmation = false
      _ = try value.document()
      value.needsProtocolConfirmation = true
    }
    if !value.needsProtocolConfirmation { _ = try value.document() }
    return value
  }

  private static func validHost(_ value: String) -> Bool {
    guard !value.isEmpty, value.utf8.count <= 253 else { return false }
    var v4 = in_addr()
    var v6 = in6_addr()
    if value.withCString({ inet_pton(AF_INET, $0, &v4) == 1 || inet_pton(AF_INET6, $0, &v6) == 1 })
    {
      return true
    }
    let labels =
      value.hasSuffix(".")
      ? value.dropLast().split(separator: ".", omittingEmptySubsequences: false)
      : value.split(separator: ".", omittingEmptySubsequences: false)
    return labels.allSatisfy { label in
      !label.isEmpty && label.utf8.count <= 63 && label.first != "-" && label.last != "-"
        && label.utf8.allSatisfy {
          (48...57).contains($0) || (65...90).contains($0) || (97...122).contains($0) || $0 == 45
        }
    }
  }
  public struct Validation: LocalizedError, Sendable {
    public let message: String
    public init(_ message: String) { self.message = message }
    public var errorDescription: String? { message }
  }
}

private enum JSONValue: Codable, Equatable, Sendable {
  case string(String)
  case number(Int)
  case decimal(Double)
  case bool(Bool)
  case object([String: JSONValue])
  case array([JSONValue])
  case null
  init(from decoder: Decoder) throws {
    let c = try decoder.singleValueContainer()
    if c.decodeNil() {
      self = .null
    } else if let value = try? c.decode(Bool.self) {
      self = .bool(value)
    } else if let value = try? c.decode(Int.self) {
      self = .number(value)
    } else if let value = try? c.decode(Double.self) {
      self = .decimal(value)
    } else if let value = try? c.decode(String.self) {
      self = .string(value)
    } else if let value = try? c.decode([String: JSONValue].self) {
      self = .object(value)
    } else {
      self = .array(try c.decode([JSONValue].self))
    }
  }
  func encode(to encoder: Encoder) throws {
    var c = encoder.singleValueContainer()
    switch self {
    case .string(let v): try c.encode(v)
    case .number(let v): try c.encode(v)
    case .decimal(let v): try c.encode(v)
    case .bool(let v): try c.encode(v)
    case .object(let v): try c.encode(v)
    case .array(let v): try c.encode(v)
    case .null: try c.encodeNil()
    }
  }
}
