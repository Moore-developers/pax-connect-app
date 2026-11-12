import Foundation

/// Public exit evidence from a frozen route. Location is an IP-database estimate.
public struct ExitLocation: Sendable, Equatable, Codable {
  public let ip: String
  public let countryCode: String
  public let country: String
  public let region: String
  public let city: String
  public let isp: String
  public let asn: String
  public let source: String
  public let geoSource: String
  public let geoError: String
  public let at: Date
  public let geoAt: Date?
  public let network: String
  public let configRevision: UInt64
  public init(
    ip: String, countryCode: String = "", country: String = "", region: String = "",
    city: String = "", isp: String = "", asn: String = "", source: String = "",
    geoSource: String = "", geoError: String = "", at: Date, geoAt: Date? = nil,
    network: String = "", configRevision: UInt64 = 0
  ) {
    self.ip = ip
    self.countryCode = countryCode
    self.country = country
    self.region = region
    self.city = city
    self.isp = isp
    self.asn = asn
    self.source = source
    self.geoSource = geoSource
    self.geoError = geoError
    self.at = at
    self.geoAt = geoAt
    self.network = network
    self.configRevision = configRevision
  }
}
