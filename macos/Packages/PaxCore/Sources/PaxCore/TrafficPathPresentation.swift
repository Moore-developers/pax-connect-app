import Foundation

@MainActor public enum TrafficPathPresentation {
  public static func text(_ report: TrafficPathDiagnostics) -> String {
    var lines = [
      "Pax 线路诊断 · " + MeasurementTimestamp.text(report.at), summary(report),
      "诊断目标：" + report.targetRouteName,
      "实际运行：" + (report.runningRouteName.isEmpty ? "直连 / 未运行" : report.runningRouteName),
      "最近启用 / 切线：" + MeasurementTimestamp.text(report.attemptAt) + " · " + report.attemptRouteName,
    ]
    lines.append(contentsOf: report.steps.map { stepTitle($0.kind) + "：" + stepDetail($0) })
    lines.append(
      contentsOf: report.attemptSteps.map { "原操作 · " + stepTitle($0.kind) + "：" + stepDetail($0) })
    if report.partial { lines.append("部分观测不可用") }
    if report.runtimeObservationAvailable, !report.changedDuringCapture {
      lines.append(
        "本次运行进入 TUN：\(report.tunConnections)；mixed：\(report.mixedConnections)；选择代理线路：\(report.proxyRoutes)；选择直连：\(report.directRoutes)。不代表传输成功或特定浏览器归属。"
      )
    }
    lines.append(contentsOf: report.findings.map { title($0) + "：" + detail($0) })
    return lines.joined(separator: "\n")
  }
  public static func summary(_ report: TrafficPathDiagnostics) -> String {
    if report.changedDuringCapture { return "采集期间网络或线路发生变化，请重新检测" }
    if report.findings.contains(where: { $0.kind == "proxy_port" && $0.state == "unreachable" }) {
      return "检测到无法连接的本地系统代理，浏览器可能在进入 Pax 前失败"
    }
    if report.findings.contains(where: { $0.kind == "browser" && $0.state == "proxy_override" }) {
      return "检测到浏览器独立代理配置，请结合实际连接核对"
    }
    if report.findings.contains(where: { $0.kind == "system_owner" && $0.state == "pax" }),
      report.findings.contains(where: { $0.kind == "mixed" && $0.state == "owned" })
    {
      return "Pax mixed 与系统代理已接管；具体连接结果见日志"
    }
    if report.findings.contains(where: { $0.kind == "system" && $0.state == "enabled" }) {
      return "系统代理已启用，浏览器可能先经过该代理"
    }
    if report.findings.contains(where: { $0.kind == "route" && $0.state == "pax" }) {
      return "探测目标的路由指向 Pax；不代表所有应用均已接管"
    }
    return "尚未确认应用流量经过 Pax"
  }

  public static func stepTitle(_ kind: String) -> String {
    [
      "configuration": "线路配置与证书", "core": "授权与代理内核", "third_party": "其他代理软件",
      "node": "连接线路服务器", "takeover": "系统网络接管", "https": "访问测试网页（Google）",
    ][kind] ?? kind
  }
  public static func stepDetail(_ step: ConnectionDiagnosticStep) -> String {
    let state = ["pass": "通过", "fail": "失败", "not_checked": "未检查"][step.state] ?? "未检查"
    let duration =
      step.at == nil ? "" : " · \(step.durationMS) ms · " + MeasurementTimestamp.text(step.at)
    return state + duration + " · " + stepReason(step)
  }
  public static func stepReason(_ step: ConnectionDiagnosticStep) -> String {
    let descriptions = [
      "no_evidence": "尚未检查，点击“重新检测”后查看", "measurement_busy": "测量名额已满，请稍后重测",
      "node_dial_only": "节点拨号已返回，完整协议通路仍未确认", "capture_changed": "检测期间线路或网络变化，请重测",
      "observation_unavailable": "系统观测不完整", "core_idle": "内核尚未运行",
      "no_third_party_tun_observed": "固定目标未发现第三方 TUN 接管",
      "takeover_observed": "已核对 TUN、固定目标路由、三类系统代理及 DNS 配置",
      "takeover_unconfirmed": "接管证据不完整，不能确认全部生效",
      "tun_unconfirmed": "无法确认本次内核的 TUN 接口",
      "runtime_selection_mismatch": "内核实际线路与目标配置不一致",
      "route_takeover_unconfirmed": "本次启用的 IP 地址族目标未全部指向 Pax TUN",
      "system_proxy_not_owned": "HTTP / HTTPS / SOCKS 系统代理未全部指向 Pax",
      "dns_takeover_unconfirmed": "系统 DNS 未观测到 Pax 的 TUN DNS 地址", "proxy_off": "当前处于直连",
      "target_not_running": "诊断目标尚未运行，当前仍是原线路",
      "core_profile_start_failed:address_in_use": "本地监听端口被占用",
      "core_profile_start_failed:permission_denied": "创建网络资源的权限不足",
      "core_profile_start_failed:route_conflict": "系统路由冲突",
      "tls_observation_unavailable": "无法只读取得证书策略",
      "core_profile_start_failed:resource_unavailable": "系统网络资源暂不可用",
      "core_profile_start_failed:configuration_rejected": "内核拒绝当前配置",
      "core_profile_start_failed:kernel_start_failed": "内核启动失败",
    ]
    let code = step.code.split(separator: ":").first.map(String.init) ?? step.code
    return descriptions[step.code]
      ?? (step.code.isEmpty
        ? "已取得成功证据" : ProductErrorPresentation.message(ProductError(code: code)))
  }

  public static func conciseStep(_ step: ConnectionDiagnosticStep) -> String {
    if step.state == "pass" { return "通过" }
    if step.code == "no_evidence" { return "未检查，点击“重新检测”。" }
    if step.code == "proxy_off" { return "当前为直连，开启代理后可检查接管状态。" }
    let state = step.state == "fail" ? "失败" : "未确认"
    let suggestion = stepAdvice(step)
    let reason = step.code == "core_required" ? "尚未完成系统授权" : stepReason(step)
    return state + " · " + reason + (suggestion.isEmpty ? "" : "\n" + suggestion)
  }
  public static func stepAdvice(_ step: ConnectionDiagnosticStep) -> String {
    guard step.state != "pass" else { return "" }
    switch step.code {
    case "core_required": return "点击代理开关，完成一次系统授权后重试。"
    case "core_idle", "proxy_off": return "需要使用代理时，开启代理后再检测。"
    case "tun_conflict_other": return "关闭其他软件的 TUN，再开启 Pax 代理。"
    case "tls_confirmation_required", "tls_policy_stale", "tls_certificate_invalid":
      return "核对线路的 SNI 和证书设置后重试。"
    case "core_profile_start_failed:address_in_use": return "退出占用代理端口的软件后重试。"
    case "core_profile_start_failed:permission_denied": return "重新完成系统授权后重试。"
    case "core_profile_start_failed:configuration_rejected", "route_invalid", "config_invalid":
      return "核对线路协议、地址和认证信息后重试。"
    case "target_not_running", "runtime_selection_mismatch", "route_selection_failed":
      return "重新选择目标线路并开启代理，再检测。"
    case "system_proxy_not_owned", "dns_takeover_unconfirmed", "route_takeover_unconfirmed",
      "takeover_unconfirmed", "tun_unconfirmed":
      return "确认其他代理已关闭，再重新连接 Pax。"
    case "measurement_busy": return "等待其他测试结束后再检测。"
    case "capture_changed": return "保持当前网络和线路不变，重新检测。"
    case "no_evidence", "observation_unavailable", "tls_observation_unavailable":
      return "点击“重新检测”补齐检查结果。"
    default:
      if step.kind == "node" { return "检查当前网络和线路配置，换一条线路对比。" }
      if step.kind == "https" { return "稍后重测，或换一条线路对比目标网站能否访问。" }
      return "重新连接后再检测；仍失败时查看详细结果。"
    }
  }
  public static func recommendation(_ report: TrafficPathDiagnostics) -> String {
    if report.changedDuringCapture { return "保持当前网络和线路不变，重新检测。" }
    if let failure = report.steps.first(where: { $0.state == "fail" }) {
      return stepAdvice(failure)
    }
    if let unknown = report.steps.first(where: { $0.state != "pass" }) {
      return stepAdvice(unknown)
    }
    return report.steps.isEmpty ? "点击“重新检测”检查这条线路。" : "本次检查通过，可以继续使用当前线路。"
  }

  public static func title(_ finding: TrafficPathFinding) -> String {
    let subject: String
    switch finding.subject {
    case "ProxyAutoConfig": subject = "自动代理脚本（PAC）"
    case "ProxyAutoDiscovery": subject = "自动发现代理"
    case "1.1.1.1": subject = "IPv4 探测目标"
    case "2606:4700:4700::1111": subject = "IPv6 探测目标"
    default: subject = finding.subject
    }
    switch finding.kind {
    case "ip_family": return finding.subject == "ipv6" ? "公网 IPv6" : "连接方式"
    case "mixed": return "Pax mixed 入口"
    case "system_owner": return "系统代理归属"
    case "system": return "系统代理 · " + subject
    case "proxy_port": return "代理端口 · " + subject
    case "listener": return "端口进程 · " + subject
    case "browser": return "浏览器参数 · " + subject
    case "browser_socket": return "实际本地连接 · " + subject
    case "route": return subject + "路由"
    case "dns": return "系统 DNS"
    default: return "检测范围"
    }
  }

  public static func detail(_ finding: TrafficPathFinding) -> String {
    if finding.kind == "ip_family" {
      if finding.subject == "connection" {
        return ["ipv4": "IPv4", "dual": "IPv4 / IPv6", "unknown": "无法确认"][finding.state] ?? "无法确认"
      }
      return [
        "disabled": "关闭（系统已关闭）", "link_local": "关闭（仅本地链路）", "unavailable": "不可用，已使用 IPv4",
        "available": "可用", "unknown": "无法确认",
      ][finding.state] ?? "无法确认"
    }

    let state: String
    switch finding.state {
    case "owned": state = "本实例已启动监听；转发结果见连接日志"
    case "effective_pending": state = "等待 macOS 应用 Pax 代理设置"
    case "effective_override", "taken_over": state = "配置已被覆盖"
    case "network_changed": state = "网络已变化，等待接管新网络"
    case "unavailable": state = "入口不可用"
    case "enabled": state = "已启用"
    case "disabled": state = "未启用"
    case "dynamic", "dynamic_override": state = "动态代理；未执行脚本，具体网址的代理决策未知"
    case "reachable": state = "本地 TCP 可连接；未验证代理协议和远端转发"
    case "unreachable": state = "本地 TCP 无法连接"
    case "proxy_override": state = "启动参数指定了代理；扩展或策略仍可能覆盖"
    case "direct_override": state = "启动参数要求绕过代理；以实际连接为准"
    case "not_observed": state = "未发现代理启动参数；不等同于浏览器没有代理"
    case "none_observed": state = "未发现支持识别的浏览器主进程"
    case "observed":
      state = finding.kind == "browser_socket" ? "已观测连接到本地端口；未确认代理协议或后续出网路径" : "已观测"
    case "pax":
      state =
        finding.kind == "dns"
        ? "系统配置包含 Pax TUN DNS 地址；不代表每次解析均成功"
        : finding.kind == "system_owner" ? "系统代理指向 Pax" : "当前内核路由指向 Pax TUN"
    case "other": state = "当前内核路由不指向 Pax TUN"
    case "partial":
      state =
        finding.subject == "scoped_proxy"
        ? "发现按接口或目标设置的代理；上方只展示全局有效设置，具体请求决策仍未知"
        : "浏览器扩展、企业策略、独立配置、各进程代理环境变量、应用内代理和 VPN 插件未覆盖；不读取浏览历史或执行 PAC"
    default: state = "未知或暂时无法读取"
    }
    let value = finding.value == "other_process" ? "其他进程" : finding.value
    return value.isEmpty || finding.kind == "coverage" ? state : state + " · " + value
  }
}
