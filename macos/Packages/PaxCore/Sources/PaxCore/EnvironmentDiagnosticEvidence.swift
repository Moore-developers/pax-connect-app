import Foundation
import PaxClient

extension EnvironmentDiagnosticPresentation {
  public static func checkProblem(_ code: String, context: PresentationContext = .chinese) -> String
  {
    if code.hasPrefix("probe_http_") { return "HTTP " + code.dropFirst("probe_http_".count) }
    if code.hasPrefix("rtc_server_") {
      return context.text("environment_evidence.216ee74c77") + code.dropFirst("rtc_server_".count)
    }
    switch code {
    case "probe_timeout": return context.text("environment_evidence.8b7f52f0db")
    case "probe_cancelled": return context.text("environment_evidence.d8ccc062b1")
    case "ipv6_no_default_route": return context.text("environment_evidence.82b3cdaa0a")
    case "ipv6_route_unavailable": return context.text("environment_evidence.5548c97160")
    case "browser_request_failed": return context.text("environment_evidence.561a629f26")
    case "probe_response_invalid", "probe_address_invalid", "probe_response_too_large":
      return context.text("environment_evidence.9832c92ec6")
    case "dns_session_invalid": return context.text("environment_evidence.ef626879ca")
    case "dns_session_unavailable": return context.text("environment_evidence.3a43070445")
    case "dns_response_invalid": return context.text("environment_evidence.c1ce336817")
    case "dns_no_results": return context.text("environment_evidence.b82deb1ebe")
    case "rtc_api_unavailable": return context.text("environment_evidence.e00844ad1f")
    case "rtc_operation_failed": return context.text("environment_evidence.1f3b0f8c0c")
    case "rtc_gathering_timeout": return context.text("environment_evidence.1d3e3981e7")
    case "": return context.text("environment_evidence.3fcad6415d")
    default: return NetworkDiagnosticPresentation.problem(code, context: context)
    }
  }
  public static func checkOutcome(
    _ check: EnvironmentDiagnosticReport.Check, ipv6Off: Bool = false,
    context: PresentationContext = .chinese
  ) -> String {
    if ipv6Off && check.kind == "ipv6" && check.state != "observed" {
      return context.text(
        "environment_evidence.61f587d595", ["arg0": String(describing: check.durationMS)])
    }
    let result: String
    switch check.state {
    case "observed":
      result = check.value.isEmpty ? context.text("environment_evidence.039c373dfa") : check.value
    case "attempted": result = context.text("environment_evidence.e50d5bacbd") + check.value
    case "absent": result = checkProblem(check.errorCode, context: context)
    case "empty": result = context.text("environment_evidence.ed1cf12bca")
    case "timeout":
      result =
        context.text("environment_evidence.75d274d40d")
        + (check.value.isEmpty ? "" : " · " + check.value)
    default: result = checkProblem(check.errorCode, context: context)
    }
    let error =
      !check.errorCode.isEmpty && ["observed", "empty", "timeout"].contains(check.state)
      ? " · " + checkProblem(check.errorCode, context: context) : ""
    return result + error + " · \(check.durationMS) ms"
  }
  @MainActor public static func checkText(
    _ check: EnvironmentDiagnosticReport.Check, ipv6Off: Bool = false,
    context: PresentationContext = .chinese
  ) -> String {
    checkOutcome(check, ipv6Off: ipv6Off, context: context) + " · "
      + MeasurementTimestamp.text(check.at)
  }
  public static func familyValue(
    _ sample: EnvironmentDiagnosticReport.Sample?, v6: Bool, context: PresentationContext = .chinese
  ) -> String {
    let address = v6 ? sample?.ipv6 ?? "" : sample?.ipv4 ?? ""
    if !address.isEmpty { return address }
    let kind = v6 ? "ipv6" : "ipv4"
    var text =
      sample?.checks?.first { $0.kind == kind }.map { checkProblem($0.errorCode, context: context) }
      ?? context.text("environment_evidence.1b4f54945f")
    if v6,
      let route = sample?.checks?.first(where: { $0.kind == "ipv6_route" && $0.state == "absent" })
    {
      text = checkProblem(route.errorCode, context: context) + "；" + text
    }
    return text
  }
}
