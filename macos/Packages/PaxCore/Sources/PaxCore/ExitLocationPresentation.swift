import Foundation
import PaxClient

@MainActor public enum ExitLocationPresentation {
  public static func region(_ value: ExitLocation) -> String {
    let country =
      value.countryCode.isEmpty
      ? value.country
      : Locale(identifier: "zh_Hans").localizedString(forRegionCode: value.countryCode)
        ?? value.country
    var seen = Set<String>()
    let parts = [country, value.region, value.city].filter {
      !$0.isEmpty && seen.insert($0.lowercased()).inserted
    }
    return parts.isEmpty ? "地区未获取" : parts.joined(separator: " · ")
  }
  public static func details(_ value: ExitLocation) -> String {
    var parts = [region(value)]
    let owner = [value.isp, value.asn].filter { !$0.isEmpty }.joined(separator: " · ")
    if !owner.isEmpty { parts.append(owner) }
    parts.append("查询：" + value.source + " · " + MeasurementTimestamp.text(value.at))
    if !value.geoSource.isEmpty {
      parts.append("地区来源：" + value.geoSource + " · " + MeasurementTimestamp.text(value.geoAt))
    }
    if !value.geoError.isEmpty { parts.append("地区信息不完整") }
    parts.append("地区为IP数据库估计，可能与实际机房位置不同")
    return parts.joined(separator: "\n")
  }
}
