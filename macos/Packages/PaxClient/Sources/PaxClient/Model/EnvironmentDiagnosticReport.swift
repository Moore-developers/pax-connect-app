import Foundation
import PaxProto

/// The Mac probe and real-browser probe are independent of selected-route reports.
public struct EnvironmentDiagnosticReport: Sendable, Equatable, Codable {
  public struct Resolver: Sendable, Equatable, Codable {
    public let ip, country, organization: String
  }
  public struct Check: Sendable, Equatable, Codable {
    public let kind, target, state, value, errorCode: String
    public let durationMS: UInt64
    public let httpStatus: UInt32
    public let at: Date?
  }
  public struct Sample: Sendable, Equatable, Codable {
    public let ipv4, ipv6: String
    public let complete: Bool
    public let resolvers: [Resolver]
    public let dnsError: String
    public let languages: [String]
    public let region, timezone, browser: String
    public let rtcAddresses: [String]
    public let rtcState, ipError: String
    public let intelligence: NetworkDiagnosticReport?
    public let checks: [Check]?
    init(_ p: Pax_V1_EnvironmentSample) {
      checks = p.checks.map {
        Check(
          kind: $0.kind, target: $0.target, state: $0.state, value: $0.value,
          errorCode: $0.errorCode, durationMS: $0.durationMs, httpStatus: $0.httpStatus,
          at: $0.hasAt ? $0.at.date : nil)
      }
      ipv4 = p.ipv4
      ipv6 = p.ipv6
      complete = p.complete
      resolvers = p.resolvers.map {
        Resolver(ip: $0.ip, country: $0.country, organization: $0.organization)
      }
      dnsError = p.dnsError
      languages = p.languages
      region = p.region
      timezone = p.timezone
      browser = p.browser
      rtcAddresses = p.rtcAddresses
      rtcState = p.rtcState
      ipError = p.ipError
      intelligence = p.hasIntelligence ? NetworkDiagnosticReport(p.intelligence) : nil
    }
  }
  public let sessionID, browserURL: String
  public let at: Date
  public let mac, browser: Sample
  public let complete: Bool

  // The session and loopback URL are short-lived credentials, never persisted.
  private enum CodingKeys: String, CodingKey { case at, mac, browser, complete }

  public init(from decoder: Decoder) throws {
    let container = try decoder.container(keyedBy: CodingKeys.self)
    sessionID = ""
    browserURL = ""
    at = try container.decode(Date.self, forKey: .at)
    mac = try container.decode(Sample.self, forKey: .mac)
    browser = try container.decode(Sample.self, forKey: .browser)
    complete = try container.decode(Bool.self, forKey: .complete)
  }
  init(_ p: Pax_V1_EnvironmentDiagnosticReport) {
    sessionID = p.sessionID
    browserURL = p.browserURL
    at = p.at.date
    mac = Sample(p.mac)
    browser = Sample(p.browser)
    complete = p.complete
  }
}
