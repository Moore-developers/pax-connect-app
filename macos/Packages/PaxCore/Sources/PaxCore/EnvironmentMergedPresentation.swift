import Foundation
import PaxClient

extension EnvironmentDiagnosticPresentation {
  /// The headline belongs to one actual egress. The other sample can enrich it
  /// only when both observed the same address.
  public struct Overview: Sendable {
    public let sample: EnvironmentDiagnosticReport.Sample?
    public let intelligence: NetworkDiagnosticReport?
    public let address: String
    public let source: String
    public let comparison: String
  }

  public static func overview(_ report: EnvironmentDiagnosticReport?) -> Overview {
    let mac = report?.mac
    let browser = report?.browser
    let browserAddress = primaryAddress(browser)
    let macAddress = primaryAddress(mac)
    let useBrowser = !browserAddress.isEmpty
    let primary = useBrowser ? browser : mac
    let secondary = useBrowser ? mac : browser
    let address = useBrowser ? browserAddress : macAddress
    let equal = !address.isEmpty && address == primaryAddress(secondary)
    let primaryIntel = primary?.intelligence
    let secondaryIntel = equal ? secondary?.intelligence : nil
    let fallback =
      primaryIntel == nil
      || (primaryIntel?.providerError.isEmpty == false
        && secondaryIntel?.providerError.isEmpty == true)
    let intel = fallback ? secondaryIntel ?? primaryIntel : primaryIntel
    let other = address.contains(":") ? secondary?.ipv6 ?? "" : secondary?.ipv4 ?? ""
    var comparison: String
    if address.isEmpty {
      comparison = "尚未取得公网出口"
    } else if equal {
      comparison = "Mac 与浏览器 " + (address.contains(":") ? "IPv6" : "IPv4") + " 出口相同"
    } else if !other.isEmpty {
      comparison = (useBrowser ? "Mac：" : "浏览器：") + other
    } else {
      comparison = (useBrowser ? "Mac" : "浏览器") + "同地址族出口未取得"
    }
    if let error = primaryIntel?.providerError, !error.isEmpty {
      comparison +=
        "；" + (useBrowser ? "浏览器" : "Mac") + "资料请求："
        + NetworkDiagnosticPresentation.problem(error)
      if fallback, secondaryIntel?.providerError.isEmpty == true {
        comparison += "；采用 " + (useBrowser ? "Mac" : "浏览器") + " 同 IP 资料"
      }
    }
    return Overview(
      sample: primary, intelligence: intel, address: address,
      source: useBrowser ? "浏览器出口" : address.isEmpty ? "当前出口" : "Mac 出口",
      comparison: comparison)
  }

  private static func primaryAddress(_ sample: EnvironmentDiagnosticReport.Sample?) -> String {
    [sample?.ipv4 ?? "", sample?.ipv6 ?? ""].first { !$0.isEmpty } ?? ""
  }

  private static func brief(
    _ row: Row, sample: EnvironmentDiagnosticReport.Sample?,
    local: NetworkDiagnosticEnvironment? = nil, at: Date
  ) -> String {
    switch row.title {
    case "公网 IP":
      guard !primaryAddress(sample).isEmpty else { return row.value }
      return "IPv4 " + (familyValue(sample, v6: false))
        + " · IPv6 " + (familyValue(sample, v6: true))
    case "语言":
      let languages = local?.languages ?? sample?.languages ?? []
      guard !languages.isEmpty else { return row.value }
      let region = local?.region ?? sample?.region ?? ""
      return languages.joined(separator: " → ") + (region.isEmpty ? "" : " · 地区 " + region)
    case "时区":
      let zone = local?.timezone ?? sample?.timezone ?? ""
      guard !zone.isEmpty else { return row.value }
      return zone + " · " + NetworkDiagnosticPresentation.utcOffset(zone, at: at)
    case "DNS":
      guard let sample, let first = sample.resolvers.first else { return row.value }
      let countries = Set(sample.resolvers.map(\.country))
      let country =
        NetworkDiagnosticPresentation.countryName(first.country)
        + (countries.count > 1 ? "等\(countries.count)国" : "")
      return "\(country) \(sample.resolvers.count)个 · \(first.ip)"
    default: return row.value
    }
  }

  /// Combine presentation only: no sample or resolver is mixed into the other.
  public static func mergedRows(
    _ report: EnvironmentDiagnosticReport?, local: NetworkDiagnosticEnvironment?, waiting: Bool
  ) -> [Row] {
    let mac = report?.mac
    let browser = report?.browser
    let macRows = rows(mac, local: local, browser: false, waiting: waiting, at: report?.at)
    let webRows = rows(browser, browser: true, waiting: waiting, at: report?.at)
    let browserName = browser?.browser.isEmpty == false ? browser?.browser ?? "浏览器" : "浏览器"
    var result: [Row] = []
    for (macRow, webRow) in zip(macRows, webRows) {
      let macPresent: Bool
      let webPresent: Bool
      let same: Bool
      switch macRow.title {
      case "公网 IP":
        macPresent = !primaryAddress(mac).isEmpty
        webPresent = !primaryAddress(browser).isEmpty
        let pairs = [
          ("IPv4", mac?.ipv4 ?? "", browser?.ipv4 ?? ""),
          ("IPv6", mac?.ipv6 ?? "", browser?.ipv6 ?? ""),
        ].filter { !$0.1.isEmpty && !$0.2.isEmpty }
        same = !pairs.isEmpty && pairs.allSatisfy { $0.1 == $0.2 }
      case "语言":
        macPresent = !(local?.languages.isEmpty ?? true)
        webPresent = !(browser?.languages.isEmpty ?? true)
        same =
          macPresent && webPresent
          && Locale(identifier: local?.languages.first ?? "").identifier
            == Locale(identifier: browser?.languages.first ?? "").identifier
          && Locale(identifier: local?.region ?? "").region?.identifier
            == Locale(identifier: browser?.region ?? "").region?.identifier
      case "时区":
        macPresent = !(local?.timezone.isEmpty ?? true)
        webPresent = !(browser?.timezone.isEmpty ?? true)
        same = macPresent && webPresent && local?.timezone == browser?.timezone
      case "DNS":
        macPresent = !(mac?.resolvers.isEmpty ?? true)
        webPresent = !(browser?.resolvers.isEmpty ?? true)
        same =
          macPresent && webPresent
          && Set(mac?.resolvers.map(\.ip) ?? []) == Set(browser?.resolvers.map(\.ip) ?? [])
      default: continue
      }
      let macValue = brief(macRow, sample: mac, local: local, at: local?.at ?? report?.at ?? Date())
      let webValue = brief(webRow, sample: browser, at: report?.at ?? Date())
      let text: String
      let tone: Tone
      if !macPresent && !webPresent && report == nil && local == nil {
        text = waiting ? "正在检测…" : "尚未检测"
        tone = .pending
      } else {
        text = "Mac：\(macValue)；\(browserName)：\(webValue)"
        tone =
          same ? .matching : macPresent && webPresent ? .difference : waiting ? .pending : .failure
      }
      let detail =
        "Mac：\(macRow.value)\n\(macRow.detail)\n\(browserName)：\(webRow.value)\n\(webRow.detail)"
      result.append(
        Row(title: macRow.title, symbol: macRow.symbol, value: text, detail: detail, tone: tone))
    }
    if let rtc = webRows.last {
      let rtcValue: String
      if report == nil && !waiting {
        rtcValue = "尚未检测"
      } else {
        rtcValue = rtc.value
      }
      result.append(
        Row(
          title: rtc.title, symbol: rtc.symbol, value: "\(browserName)：" + rtcValue,
          detail: rtc.detail, tone: rtc.tone))
    }
    return result
  }
}
