import Foundation

extension EnvironmentDiagnosticPresentation {
  public static let locationTitle = "定位"

  struct LocationReading {
    let value: String
    let detail: String
    let tone: Tone
  }

  private static func systemPath(context: PresentationContext) -> String {
    context.text("environment_evidence.595da825fc")
  }
  private static func notScanned(context: PresentationContext) -> String {
    context.text("environment_evidence.4a0a75ddd9")
  }
  private static func offNote(context: PresentationContext) -> String {
    context.text("environment_evidence.792bc88ae7")
  }

  /// One combined row after IPv6. `local` nil with a report means an old record.
  static func locationRows(
    _ report: EnvironmentDiagnosticReport?, local: NetworkDiagnosticEnvironment?, waiting: Bool,
    context: PresentationContext = .chinese
  ) -> [Row] {
    let reading: LocationReading
    if local == nil && report == nil {
      let text =
        waiting
        ? context.text("environment_evidence.e8b970bc3e")
        : context.text("environment_evidence.4d2966c438")
      reading = .init(value: text, detail: "", tone: .pending)
    } else if let location = local?.location {
      reading = locationReading(location, context: context)
    } else if waiting {
      reading = .init(
        value: context.text("environment_evidence.e8b970bc3e"), detail: "",
        tone: .pending)
    } else {
      reading = .init(
        value: context.text("environment_evidence.883b0ebcba"), detail: "",
        tone: .pending)
    }
    return [
      Row(
        title: locationTitle, symbol: "location", value: reading.value, detail: reading.detail,
        tone: reading.tone, context: context)
    ]
  }

  private static func locationReading(
    _ location: LocationExposure, context: PresentationContext = .chinese
  ) -> LocationReading {
    let system = systemReading(location, context: context)
    let browser = browserReading(location, context: context)
    let value: String
    switch location.systemEnabled {
    case nil:
      value = context.text("environment_evidence.b11a791164")
    case false?:
      value = context.text("environment_evidence.f106e5fc38")
    case true?:
      if location.authorizationsReadable {
        let apps = location.apps.filter(\.allowed).count
        let browsers = location.browsers.filter { $0.authorization == .allowed }.count
        value =
          apps + browsers > 0
          ? context.text(
            "environment_evidence.c9061d0a95",
            ["arg0": String(describing: apps), "arg1": String(describing: browsers)])
          : context.text("environment_evidence.fa8af21d10")
      } else {
        value = context.text("environment_evidence.e2e3bcc529")
      }
    }
    let boundary = context.text("environment_evidence.6bcac31928")
    return .init(
      value: value,
      detail: context.text("environment_evidence.603ff31cb4") + system.detail
        + context.text("environment_evidence.d9ac3d215a") + browser + "\n" + boundary,
      tone: system.tone)
  }

  private static func systemReading(
    _ location: LocationExposure, context: PresentationContext = .chinese
  ) -> (detail: String, tone: Tone) {
    let readable = location.authorizationsReadable
    let allowed = location.apps.filter(\.allowed)
    let denied = location.apps.filter { !$0.allowed }
    func line(_ app: LocationExposure.App) -> String {
      app.name
        + (app.path.isEmpty
          ? "（" + app.bundleID + context.text("environment_evidence.5b5129748b") : " · " + app.path)
    }
    var lines: [String] = []
    if readable {
      if !allowed.isEmpty {
        lines.append(
          context.text("environment_evidence.93fe8ce78a")
            + allowed.map(line).joined(separator: "\n"))
      }
      if !denied.isEmpty {
        lines.append(
          context.text("environment_evidence.e89b2a52ba") + denied.map(line).joined(separator: "\n")
        )
      }
      if lines.isEmpty { lines.append(context.text("environment_evidence.8d8b166fe3")) }
    } else {
      lines.append(context.text("environment_evidence.1f210c29e8"))
    }
    let tail =
      "\n" + lines.joined(separator: "\n") + context.text("environment_evidence.a5ee3e61d7")
    switch location.systemEnabled {
    case nil:
      return (
        context.text("environment_evidence.aff8b8e614") + tail, .pending
      )
    case false?:
      return (
        context.text("environment_evidence.5e85cc61e1") + tail, .information
      )
    case true?:
      return (
        context.text("environment_evidence.8c002edfc6") + tail
          + context.text("environment_evidence.e109b1547b") + systemPath(context: context),
        .difference
      )
    }
  }

  private static func browserBoundary(context: PresentationContext) -> String {
    context.text("environment_evidence.c23c79dc0f")
  }

  private static func browserReading(
    _ location: LocationExposure, context: PresentationContext = .chinese
  ) -> String {
    guard location.authorizationsReadable else {
      return context.text("environment_evidence.13c210cfce") + notScanned(context: context)
    }
    let states = location.browsers.map { ($0, LocationExposure.state($0)) }
    var lines = states.map { browser, state in browserLine(browser, state, context: context) }
    if lines.isEmpty { lines = [context.text("environment_evidence.179d3b4394")] }
    if location.systemEnabled == false {
      return offNote(context: context) + "\n" + lines.joined(separator: "\n") + "\n"
        + notScanned(context: context)
    }
    let detail =
      lines.joined(separator: "\n") + context.text("environment_evidence.e109b1547b")
      + systemPath(context: context) + "\n" + browserBoundary(context: context) + "\n"
      + notScanned(context: context)
    return detail
  }

  private static func stateText(
    _ state: LocationExposure.State, context: PresentationContext = .chinese
  ) -> String {
    switch state {
    case .open: context.text("environment_evidence.4dbb226601")
    case .closed: context.text("environment_evidence.6744b4c6a9")
    }
  }

  private static func browserLine(
    _ browser: LocationExposure.Browser, _ state: LocationExposure.State,
    context: PresentationContext = .chinese
  ) -> String {
    let reason: String
    switch browser.authorization {
    case .allowed: reason = context.text("environment_evidence.c3d72b1ab5")
    case .denied: reason = context.text("environment_evidence.cdcaf965c8")
    case .none: reason = context.text("environment_evidence.f886839eed")
    }
    return browser.name + "：" + stateText(state, context: context) + "（" + reason + "）"
  }
}
