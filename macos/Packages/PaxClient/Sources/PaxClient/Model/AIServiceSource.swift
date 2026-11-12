import Foundation

/// Observed local configuration, never a verified login or a provider invoice.
public enum AIServiceSource: String, Equatable, Sendable {
  case unrecorded = ""
  case officialAccount = "official_account"
  case officialAPI = "official_api"
  case relayAPI = "relay_api"
  case unknown = "unknown"

  public var label: String {
    switch self {
    case .officialAccount: "官方账号"
    case .officialAPI: "官方 API"
    case .relayAPI: "第三方中转"
    case .unrecorded, .unknown: "来源待确认"
    }
  }

  public var hasChannelEstimate: Bool {
    self != .officialAccount && self != .unknown
  }

  public static func api(endpoint: String) -> Self {
    guard let url = URLComponents(string: endpoint), let host = url.host else { return .unknown }
    if url.scheme == "https", url.port == nil || url.port == 443,
      ["api.openai.com", "api.anthropic.com"].contains(host.lowercased()),
      ["", "/", "/v1", "/v1/"].contains(url.path),
      url.user == nil, url.password == nil, url.query == nil, url.fragment == nil
    {
      return .officialAPI
    }
    return .relayAPI
  }
}
