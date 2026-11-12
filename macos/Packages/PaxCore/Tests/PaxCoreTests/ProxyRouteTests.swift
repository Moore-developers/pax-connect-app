import Foundation
import Testing

@testable import PaxCore

struct ProxyRouteTests {
  @Test func purchasedFormatsAndCredentials() throws {
    let parsed = try ProxyRouteDraft.pasted(
      "https://a%40b:p%3A%40%25@proxy.example:8443#Work", keeping: .init())
    #expect(parsed.username == "a@b")
    #expect(parsed.password == "p:@%")
    #expect(parsed.kind == .https)
    #expect(parsed.name == "Work")
    let object = try #require(
      JSONSerialization.jsonObject(with: Data(parsed.document().utf8)) as? [String: Any])
    #expect((object["tls"] as? [String: Any])?["server_name"] as? String == "proxy.example")
    let bare = try ProxyRouteDraft.pasted(
      "[2001:db8::1]:1080:user:pass:with:colon", keeping: .init())
    #expect(bare.server == "2001:db8::1")
    #expect(bare.password == "pass:with:colon")
    #expect(bare.needsProtocolConfirmation)
    #expect(throws: (any Error).self) { try bare.document() }
  }
  @Test func noAuthenticationAndPreservedSettings() throws {
    var value = try ProxyRouteDraft(
      document: """
        {"type":"http","server":"proxy.example","server_port":8443,"username":"old","password":"secret","connect_timeout":"7s","tls":{"enabled":true,"server_name":"cert.example","alpn":["http/1.1"]}}
        """, name: "Proxy")
    value.authenticated = false
    value.server = "192.0.2.1"
    let object = try #require(
      JSONSerialization.jsonObject(with: Data(value.document().utf8)) as? [String: Any])
    #expect(object["username"] == nil && object["password"] == nil)
    #expect(object["connect_timeout"] as? String == "7s")
    #expect((object["tls"] as? [String: Any])?["server_name"] as? String == "cert.example")
    #expect((object["tls"] as? [String: Any])?["alpn"] as? [String] == ["http/1.1"])
  }
  @Test(arguments: [
    "https://host:443/subscription?token=secret", "host:0", "host:1080:user", "host:1080\nother:80",
    "socks4://host:1080",
  ])
  func rejectsAmbiguousInput(_ input: String) {
    #expect(throws: (any Error).self) { try ProxyRouteDraft.pasted(input, keeping: .init()) }
  }
  @Test func socksByteLimitAndAuthentication() throws {
    var value = ProxyRouteDraft()
    value.server = "proxy.example"
    value.port = "1080"
    value.username = "user"
    value.password = String(repeating: "密", count: 86)
    #expect(throws: (any Error).self) { try value.document() }
    value.authenticated = false
    let object = try #require(
      JSONSerialization.jsonObject(with: Data(value.document().utf8)) as? [String: Any])
    #expect(object["username"] == nil && object["password"] == nil)
    #expect(object["version"] as? String == "5")
  }
}
