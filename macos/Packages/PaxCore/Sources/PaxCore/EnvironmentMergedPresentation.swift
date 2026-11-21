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

  public static func overview(
    _ report: EnvironmentDiagnosticReport?, context: PresentationContext = .chinese
  ) -> Overview {
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
      comparison = context.text("environment_evidence.32df071b4d")
    } else if equal {
      comparison =
        context.text("environment_evidence.3cc000bd7d") + (address.contains(":") ? "IPv6" : "IPv4")
        + context.text("environment_evidence.ef06b69dfa")
    } else if !other.isEmpty {
      comparison = (useBrowser ? "Mac：" : context.text("environment_evidence.f18bb04f28")) + other
    } else {
      comparison =
        (useBrowser ? "Mac" : context.text("environment_evidence.e19c3b9e26"))
        + context.text("environment_evidence.d4025b7ef7")
    }
    if let error = primaryIntel?.providerError, !error.isEmpty {
      comparison +=
        "；" + (useBrowser ? context.text("environment_evidence.e19c3b9e26") : "Mac")
        + context.text("environment_evidence.c134ff3fbe")
        + NetworkDiagnosticPresentation.problem(error, context: context)
      if fallback, secondaryIntel?.providerError.isEmpty == true {
        comparison +=
          context.text("environment_evidence.ca841aeee1")
          + (useBrowser ? "Mac" : context.text("environment_evidence.e19c3b9e26"))
          + context.text("environment_evidence.9d7321425a")
      }
    }
    return Overview(
      sample: primary, intelligence: intel, address: address,
      source: useBrowser
        ? context.text("environment_evidence.7b8cea54d1")
        : address.isEmpty
          ? context.text("environment_evidence.8c50920fd6")
          : context.text("environment_evidence.b317191e55"),
      comparison: comparison)
  }

  private static func primaryAddress(_ sample: EnvironmentDiagnosticReport.Sample?) -> String {
    [sample?.ipv4 ?? "", sample?.ipv6 ?? ""].first { !$0.isEmpty } ?? ""
  }

  private static func brief(
    _ row: Row, sample: EnvironmentDiagnosticReport.Sample?,
    local: NetworkDiagnosticEnvironment? = nil, at: Date, context: PresentationContext = .chinese
  ) -> String {
    switch row.kind {
    case "public_ip":
      guard !primaryAddress(sample).isEmpty else { return row.value }
      return "IPv4 " + (familyValue(sample, v6: false, context: context))
        + " · IPv6 " + (familyValue(sample, v6: true, context: context))
    case "language":
      let languages = local?.languages ?? sample?.languages ?? []
      guard !languages.isEmpty else { return row.value }
      let region = local?.region ?? sample?.region ?? ""
      return languages.joined(separator: " → ")
        + (region.isEmpty ? "" : context.text("environment_evidence.1235aaead5") + region)
    case "timezone":
      let zone = local?.timezone ?? sample?.timezone ?? ""
      guard !zone.isEmpty else { return row.value }
      return zone + " · " + NetworkDiagnosticPresentation.utcOffset(zone, at: at, context: context)
    case "dns":
      guard let sample, let first = sample.resolvers.first else { return row.value }
      let countries = Set(sample.resolvers.map(\.country))
      let country =
        NetworkDiagnosticPresentation.countryName(first.country, context: context)
        + (countries.count > 1
          ? context.text(
            "environment_evidence.9c06196905", ["arg0": String(describing: countries.count)]) : "")
      return context.text(
        "environment_evidence.cdca82acc0",
        [
          "arg0": String(describing: country), "arg1": String(describing: sample.resolvers.count),
          "arg2": String(describing: first.ip),
        ])
    default: return row.value
    }
  }

  /// Combine presentation only: no sample or resolver is mixed into the other.
  public static func mergedRows(
    _ report: EnvironmentDiagnosticReport?, local: NetworkDiagnosticEnvironment?, waiting: Bool,
    context: PresentationContext = .chinese
  ) -> [Row] {
    let mac = report?.mac
    let browser = report?.browser
    let macRows = rows(
      mac, local: local, browser: false, waiting: waiting, at: report?.at, context: context)
    let webRows = rows(browser, browser: true, waiting: waiting, at: report?.at, context: context)
    let browserName =
      browser?.browser.isEmpty == false
      ? browser?.browser ?? context.text("environment_evidence.e19c3b9e26")
      : context.text("environment_evidence.e19c3b9e26")
    var result: [Row] = []
    for (macRow, webRow) in zip(macRows, webRows) {
      let macPresent: Bool
      let webPresent: Bool
      let same: Bool
      switch macRow.kind {
      case "public_ip":
        macPresent = !primaryAddress(mac).isEmpty
        webPresent = !primaryAddress(browser).isEmpty
        let pairs = [
          ("IPv4", mac?.ipv4 ?? "", browser?.ipv4 ?? ""),
          ("IPv6", mac?.ipv6 ?? "", browser?.ipv6 ?? ""),
        ].filter { !$0.1.isEmpty && !$0.2.isEmpty }
        same = !pairs.isEmpty && pairs.allSatisfy { $0.1 == $0.2 }
      case "language":
        macPresent = !(local?.languages.isEmpty ?? true)
        webPresent = !(browser?.languages.isEmpty ?? true)
        same =
          macPresent && webPresent
          && Locale(identifier: local?.languages.first ?? "").identifier
            == Locale(identifier: browser?.languages.first ?? "").identifier
          && Locale(identifier: local?.region ?? "").region?.identifier
            == Locale(identifier: browser?.region ?? "").region?.identifier
      case "timezone":
        macPresent = !(local?.timezone.isEmpty ?? true)
        webPresent = !(browser?.timezone.isEmpty ?? true)
        same = macPresent && webPresent && local?.timezone == browser?.timezone
      case "dns":
        macPresent = !(mac?.resolvers.isEmpty ?? true)
        webPresent = !(browser?.resolvers.isEmpty ?? true)
        same =
          macPresent && webPresent
          && Set(mac?.resolvers.map(\.ip) ?? []) == Set(browser?.resolvers.map(\.ip) ?? [])
      default: continue
      }
      let macValue = brief(
        macRow, sample: mac, local: local, at: local?.at ?? report?.at ?? Date(), context: context)
      let webValue = brief(webRow, sample: browser, at: report?.at ?? Date(), context: context)
      let text: String
      let tone: Tone
      if !macPresent && !webPresent && report == nil && local == nil {
        text =
          waiting
          ? context.text("environment_evidence.e8b970bc3e")
          : context.text("environment_evidence.4d2966c438")
        tone = .pending
      } else {
        text = "Mac：\(macValue)；\(browserName)：\(webValue)"
        tone =
          same ? .matching : macPresent && webPresent ? .difference : waiting ? .pending : .failure
      }
      let detail =
        "Mac：\(macRow.value)\n\(macRow.detail)\n\(browserName)：\(webRow.value)\n\(webRow.detail)"
      result.append(
        Row(
          title: macRow.kind, symbol: macRow.symbol, value: text, detail: detail, tone: tone,
          context: context))
    }
    if let rtc = webRows.last {
      let rtcValue: String
      if report == nil && !waiting {
        rtcValue = context.text("environment_evidence.4d2966c438")
      } else {
        rtcValue = rtc.value
      }
      result.append(
        Row(
          title: rtc.kind, symbol: rtc.symbol, value: "\(browserName)：" + rtcValue,
          detail: rtc.detail, tone: rtc.tone, context: context))
    }
    return result + [ipv6Row(report, waiting: waiting, context: context)]
      + locationRows(report, local: local, waiting: waiting, context: context)
  }
}
