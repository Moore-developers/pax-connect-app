import PaxProto

/// Saved meaning and safe event-time parameters. Missing or unknown text keeps its original fallback.
public struct ProductText: Sendable, Equatable, Codable {
  public let code: String
  public let params: [String: String]
  public init(code: String, params: [String: String] = [:]) {
    self.code = code
    self.params = params
  }
  init(_ p: Pax_V1_ProductText) {
    code = p.code
    params = p.params
  }
}
