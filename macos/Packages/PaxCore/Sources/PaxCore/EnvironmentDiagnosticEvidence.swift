import Foundation
import PaxClient

extension EnvironmentDiagnosticPresentation {
  public static func checkProblem(_ code: String) -> String {
    if code.hasPrefix("probe_http_") { return "HTTP " + code.dropFirst("probe_http_".count) }
    if code.hasPrefix("rtc_server_") { return "STUN 错误 " + code.dropFirst("rtc_server_".count) }
    switch code {
    case "probe_timeout": return "请求超时"
    case "probe_cancelled": return "用户取消"
    case "ipv6_no_default_route": return "本机无 IPv6 默认路由"
    case "ipv6_route_unavailable": return "无法读取 IPv6 路由"
    case "browser_request_failed": return "浏览器请求失败，未能区分网络与跨域限制"
    case "probe_response_invalid", "probe_address_invalid", "probe_response_too_large":
      return "响应未包含有效读值或超出限额"
    case "dns_session_invalid": return "DNS 服务会话格式无效"
    case "dns_session_unavailable": return "未建立 DNS 采样会话"
    case "dns_response_invalid": return "DNS 服务返回格式无效"
    case "dns_no_results": return "权威端未返回解析器记录"
    case "rtc_api_unavailable": return "浏览器不支持 WebRTC API"
    case "rtc_operation_failed": return "浏览器创建或设置 WebRTC 会话失败"
    case "rtc_gathering_timeout": return "6.5 秒采集窗口到期"
    case "": return "具体原因未记录"
    default: return NetworkDiagnosticPresentation.problem(code)
    }
  }
  public static func checkOutcome(_ check: EnvironmentDiagnosticReport.Check) -> String {
    let result: String
    switch check.state {
    case "observed": result = check.value.isEmpty ? "取得响应" : check.value
    case "attempted": result = "已触发查询 · " + check.value
    case "absent": result = checkProblem(check.errorCode)
    case "empty": result = "采集自然结束，0 个候选"
    case "timeout": result = "采集等待到期" + (check.value.isEmpty ? "" : " · " + check.value)
    default: result = checkProblem(check.errorCode)
    }
    let error =
      !check.errorCode.isEmpty && ["observed", "empty", "timeout"].contains(check.state)
      ? " · " + checkProblem(check.errorCode) : ""
    return result + error + " · \(check.durationMS) ms"
  }
  @MainActor public static func checkText(_ check: EnvironmentDiagnosticReport.Check) -> String {
    checkOutcome(check) + " · " + MeasurementTimestamp.text(check.at)
  }
  public static func familyValue(_ sample: EnvironmentDiagnosticReport.Sample?, v6: Bool) -> String
  {
    let address = v6 ? sample?.ipv6 ?? "" : sample?.ipv4 ?? ""
    if !address.isEmpty { return address }
    let kind = v6 ? "ipv6" : "ipv4"
    var text =
      sample?.checks?.first { $0.kind == kind }.map { checkProblem($0.errorCode) }
      ?? "本次未取得（旧记录未采集原因）"
    if v6,
      let route = sample?.checks?.first(where: { $0.kind == "ipv6_route" && $0.state == "absent" })
    {
      text = checkProblem(route.errorCode) + "；" + text
    }
    return text
  }
}
