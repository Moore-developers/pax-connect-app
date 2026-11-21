import Foundation
import PaxClient

@MainActor public enum ExitLocationPresentation {
  public static func region(_ value: ExitLocation, context: PresentationContext = .chinese)
    -> String
  {
    let country =
      value.countryCode.isEmpty
      ? value.country
      : Locale(identifier: context.language == .chinese ? "zh_Hans" : "en").localizedString(
        forRegionCode: value.countryCode)
        ?? value.country
    var seen = Set<String>()
    let parts = [country, value.region, value.city].filter {
      !$0.isEmpty && seen.insert($0.lowercased()).inserted
    }
    return parts.isEmpty ? context.text("exit.region_missing") : parts.joined(separator: " · ")
  }
  public static func details(_ value: ExitLocation, context: PresentationContext = .chinese)
    -> String
  {
    var parts = [region(value, context: context)]
    let owner = [value.isp, value.asn].filter { !$0.isEmpty }.joined(separator: " · ")
    if !owner.isEmpty { parts.append(owner) }
    parts.append(
      context.text("exit.query") + value.source + " · " + MeasurementTimestamp.text(value.at))
    if !value.geoSource.isEmpty {
      parts.append(
        context.text("exit.source") + value.geoSource + " · "
          + MeasurementTimestamp.text(value.geoAt))
    }
    if !value.geoError.isEmpty { parts.append(context.text("exit.incomplete")) }
    parts.append(context.text("exit.estimate"))
    return parts.joined(separator: "\n")
  }
}
