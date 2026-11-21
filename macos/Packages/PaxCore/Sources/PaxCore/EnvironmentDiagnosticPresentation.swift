import Foundation
import PaxClient

/// Plain-language evidence, also used by the frozen environment export.
public enum EnvironmentDiagnosticPresentation {
  public enum Tone: Sendable { case information, matching, difference, failure, pending }
  public struct Row: Sendable {
    public let title, symbol, value, detail: String
    /// Stable built-in identity, independent of the displayed title or language pack.
    public let kind: String
    public init(
      title: String, symbol: String, value: String, detail: String, tone: Tone,
      context: PresentationContext = .chinese
    ) {
      let keys = [
        "公网 IP": "public_ip", "语言": "language", "时区": "timezone", "DNS": "dns",
        "WebRTC": "webrtc", "IPv6": "ipv6", "定位": "location",
      ]
      self.kind = keys[title] ?? title
      self.title =
        (keys[title] ?? (keys.values.contains(title) ? title : nil)).map {
          context.text("environment.row." + $0)
        } ?? title
      self.symbol = symbol
      self.value = value
      self.detail = detail
      self.tone = tone
    }
    public let tone: Tone
  }
  public static func rows(
    _ sample: EnvironmentDiagnosticReport.Sample?, local: NetworkDiagnosticEnvironment? = nil,
    browser: Bool, waiting: Bool, at: Date? = nil, context: PresentationContext = .chinese
  ) -> [Row] {
    if sample == nil && local == nil && !waiting {
      let items =
        [("公网 IP", "globe"), ("语言", "character.bubble"), ("时区", "clock"), ("DNS", "server.rack")]
        + (browser ? [("WebRTC", "point.3.connected.trianglepath.dotted")] : [])
      return items.map {
        Row(
          title: $0.0, symbol: $0.1, value: context.text("environment_evidence.4d2966c438"),
          detail: "", tone: .pending, context: context)
      }
    }
    let pending = sample?.complete != true && waiting
    let languages = browser ? sample?.languages ?? [] : local?.languages ?? []
    let region = browser ? sample?.region ?? "" : local?.region ?? ""
    let zone = browser ? sample?.timezone ?? "" : local?.timezone ?? ""
    var rows: [Row] = []
    let ips = [sample?.ipv4 ?? "", sample?.ipv6 ?? ""].filter { !$0.isEmpty }
    rows.append(
      Row(
        title: "公网 IP", symbol: "globe",
        value: ips.isEmpty
          ? (pending
            ? context.text("environment_evidence.24de95eab9")
            : sampleProblem(
              sample?.ipError ?? "", fallback: context.text("environment_evidence.ae00dafe64"),
              context: context))
          : location(sample, context: context),
        detail: ips.isEmpty
          ? context.text("environment_evidence.feeda78116")
          : ips.joined(separator: "\n"),
        tone: ips.isEmpty ? (pending ? .pending : .failure) : .information, context: context))
    let locale = Locale(identifier: context.language == .chinese ? "zh_CN" : "en")
    let language =
      languages.first.map {
        locale.localizedString(
          forLanguageCode: Locale(identifier: $0).language.languageCode?.identifier ?? $0) ?? $0
      } ?? ""
    let code = Locale(identifier: region).region?.identifier ?? ""
    let country =
      code.isEmpty ? "" : NetworkDiagnosticPresentation.countryName(code, context: context)
    rows.append(
      Row(
        title: "语言", symbol: "character.bubble",
        value: language.isEmpty
          ? (pending
            ? context.text("environment_evidence.8c45a5bbbf")
            : context.text("environment_evidence.263ebd99b1")) : language,
        detail: languages.joined(separator: " → ")
          + (country.isEmpty ? "" : context.text("environment_evidence.549da762ae") + country),
        tone: language.isEmpty ? (pending ? .pending : .failure) : .information, context: context))
    rows.append(
      Row(
        title: "时区", symbol: "clock",
        value: zone.isEmpty
          ? (pending
            ? context.text("environment_evidence.5181fb2a7b")
            : context.text("environment_evidence.d4b04cb131"))
          : timezone(zone, at: local?.at ?? at ?? Date(), context: context),
        detail: zone.isEmpty
          ? "" : zone + " · " + localTime(zone, at: local?.at ?? at ?? Date(), context: context),
        tone: zone.isEmpty ? (pending ? .pending : .failure) : .information, context: context))
    let resolvers = sample?.resolvers ?? []
    rows.append(
      Row(
        title: "DNS", symbol: "server.rack",
        value: resolvers.isEmpty
          ? (pending
            ? context.text("environment_evidence.a2a8af90ba")
            : sampleProblem(
              sample?.dnsError ?? "", fallback: context.text("environment_evidence.1567b0a5b3"),
              context: context))
          : dnsSummary(resolvers, context: context),
        detail: resolvers.isEmpty
          ? (pending ? "" : context.text("environment_evidence.15d8603daf"))
          : resolvers.prefix(2).map(\.ip).joined(separator: " · ")
            + (resolvers.count > 2
              ? context.text(
                "environment_evidence.3d9397bc02", ["arg0": String(describing: resolvers.count - 2)]
              ) : ""),
        tone: resolvers.isEmpty ? (pending ? .pending : .failure) : .information, context: context))
    if browser {
      let addresses = (sample?.rtcAddresses ?? []).filter { isPublicAddress($0) }
      let state = sample?.rtcState ?? ""
      let verdict = webRTCVerdict(sample, pending: pending, context: context)
      let evidence = (sample?.checks ?? []).filter { $0.kind.hasPrefix("rtc_") }
        .map { $0.target + "：" + checkOutcome($0, context: context) }
      var lines =
        addresses.isEmpty
        ? (pending || state == "unsupported"
          ? [] : [context.text("environment_evidence.650a19b9eb")]) : addresses
      lines += verdict.routeDetails + evidence
      rows.append(
        Row(
          title: "WebRTC", symbol: "point.3.connected.trianglepath.dotted",
          value: verdict.text, detail: lines.joined(separator: "\n"), tone: verdict.tone,
          context: context))
    }
    return rows
  }
  private static func sampleProblem(
    _ code: String, fallback: String, context: PresentationContext = .chinese
  ) -> String {
    switch code {
    case "sampling_timeout": context.text("environment_evidence.875d1d42cd")
    case "dns_cancelled": context.text("environment_evidence.bb69d85029")
    case "dns_unavailable": context.text("environment_evidence.c1160cf273")
    case "ip_unavailable": context.text("environment_evidence.4ec8f916ca")
    case "": fallback
    default: checkProblem(code, context: context)
    }
  }
  public static func location(
    _ sample: EnvironmentDiagnosticReport.Sample?, context: PresentationContext = .chinese
  ) -> String {
    guard let sample, !sample.ipv4.isEmpty || !sample.ipv6.isEmpty else {
      return context.text("environment_evidence.400be355bf")
    }
    if let error = sample.intelligence?.providerError, !error.isEmpty {
      return context.text("environment_evidence.5aaa9e6b89")
        + NetworkDiagnosticPresentation.problem(error, context: context)
    }
    guard let intel = sample.intelligence, !intel.country.isEmpty else {
      return context.text("environment_evidence.6dc8c692de")
    }
    let city = intel.sources.first { $0.country == intel.country && !$0.city.isEmpty }?.city ?? ""
    return NetworkDiagnosticPresentation.countryName(intel.country, context: context)
      + (city.isEmpty ? "" : " · " + city)
  }
  public static func dnsSummary(
    _ values: [EnvironmentDiagnosticReport.Resolver], context: PresentationContext = .chinese
  ) -> String {
    let grouped = Dictionary(grouping: values, by: \.country)
    let countries = grouped.keys.sorted().map {
      NetworkDiagnosticPresentation.countryName($0, context: context)
        + (grouped.count > 1
          ? context.text(
            "environment_evidence.f3ccaefd18", ["arg0": String(describing: grouped[$0]?.count ?? 0)]
          ) : "")
    }.joined(separator: "、")
    return context.text(
      "environment_evidence.144e5251ed", ["arg0": String(describing: values.count)])
      + (grouped.count == 1
        ? context.text("environment_evidence.9654415847")
        : context.text("environment_evidence.ae0cb210d8")) + countries
  }
  public static func timezone(
    _ identifier: String, at: Date, context: PresentationContext = .chinese
  ) -> String {
    guard let zone = TimeZone(identifier: identifier) else {
      return context.text(
        "environment_evidence.e8fc21c6b1", ["arg0": String(describing: identifier)])
    }
    let countries = (zoneCountries[identifier] ?? []).map {
      NetworkDiagnosticPresentation.countryName($0, context: context)
    }.joined(separator: " / ")
    let name =
      zone.localizedName(
        for: .generic, locale: Locale(identifier: context.language == .chinese ? "zh_CN" : "en"))
      ?? identifier
    return (countries.isEmpty ? "" : countries + " · ") + name + "，"
      + NetworkDiagnosticPresentation.utcOffset(identifier, at: at, context: context)
  }
  private static let zoneCountries: [String: [String]] = {
    guard let text = try? String(contentsOfFile: "/usr/share/zoneinfo/zone.tab", encoding: .utf8)
    else { return [:] }
    var result: [String: [String]] = [:]
    for line in text.split(separator: "\n") where !line.hasPrefix("#") {
      let fields = line.split(separator: "\t")
      if fields.count >= 3 {
        result[String(fields[2])] = fields[0].split(separator: ",").map(String.init)
      }
    }
    return result
  }()
  private static func localTime(
    _ identifier: String, at: Date, context: PresentationContext = .chinese
  ) -> String {
    let format = DateFormatter()
    format.locale = .current
    format.timeZone = TimeZone(identifier: identifier)
    format.setLocalizedDateFormatFromTemplate("MMMdd HHmm")
    return context.text("environment_evidence.639b333de7") + format.string(from: at)
  }
  public static func isPublicAddress(_ address: String) -> Bool {
    let lower = address.lowercased()
    if lower.hasSuffix(".local") || lower == "::1" || lower == "::" || lower.hasPrefix("fe80:")
      || lower.hasPrefix("fc") || lower.hasPrefix("fd")
    {
      return false
    }
    if lower.contains(":") { return true }
    let parts = lower.split(separator: ".").compactMap { Int($0) }
    guard parts.count == 4 else { return false }
    return
      !(parts[0] == 0 || parts[0] == 10 || parts[0] == 127 || parts[0] >= 224
      || (parts[0] == 169 && parts[1] == 254) || (parts[0] == 172 && (16...31).contains(parts[1]))
      || (parts[0] == 192 && parts[1] == 168) || (parts[0] == 100 && (64...127).contains(parts[1])))
  }
  public static func differences(
    _ report: EnvironmentDiagnosticReport?, local: NetworkDiagnosticEnvironment?
  ) -> Set<String> {
    guard let report, report.mac.complete, report.browser.complete, !report.browser.browser.isEmpty
    else { return [] }
    var result: Set<String> = []
    for pair in [(report.mac.ipv4, report.browser.ipv4), (report.mac.ipv6, report.browser.ipv6)]
    where !pair.0.isEmpty && !pair.1.isEmpty {
      if pair.0 != pair.1 { result.insert("公网 IP") }
    }
    if let macLanguage = local?.languages.first,
      let browserLanguage = report.browser.languages.first,
      Locale(identifier: macLanguage).identifier != Locale(identifier: browserLanguage).identifier
    {
      result.insert("语言")
    }
    if let macZone = local?.timezone, !report.browser.timezone.isEmpty,
      macZone != report.browser.timezone
    {
      result.insert("时区")
    }
    let macDNS = Set(report.mac.resolvers.map(\.ip))
    let browserDNS = Set(report.browser.resolvers.map(\.ip))
    if !macDNS.isEmpty && !browserDNS.isEmpty && macDNS != browserDNS { result.insert("DNS") }
    return result
  }
  public static func difference(
    _ report: EnvironmentDiagnosticReport?, local: NetworkDiagnosticEnvironment? = nil,
    context: PresentationContext = .chinese
  ) -> String {
    guard let report, report.mac.complete, report.browser.complete, !report.browser.browser.isEmpty
    else { return context.text("environment_evidence.6c353e9dc8") }
    let values = differences(report, local: local)
    if !values.isEmpty {
      return context.text(
        "environment_evidence.f35db5d500", ["arg0": String(describing: values.count)])
    }
    guard !report.mac.ipv4.isEmpty || !report.mac.ipv6.isEmpty,
      !report.browser.ipv4.isEmpty || !report.browser.ipv6.isEmpty
    else { return context.text("environment_evidence.6954ee060e") }
    return context.text("environment_evidence.b84f03e250")
  }
}
