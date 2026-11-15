import Foundation
import PaxClient

extension EnvironmentDiagnosticPresentation {
  public static let ipv6Title = "IPv6"

  public enum IPv6Exposure: Sendable { case on, off, unreadable, old }

  /// Judged only from the Mac's existing `ipv6_route` evidence; the browser has no switch of its own.
  public static func ipv6Exposure(_ report: EnvironmentDiagnosticReport?) -> IPv6Exposure? {
    guard let checks = report?.mac.checks else { return report == nil ? nil : .old }
    switch checks.first(where: { $0.kind == "ipv6_route" })?.state {
    case "observed": return .on
    case "absent": return .off
    default: return .unreadable
    }
  }

  private static let ipv6Path = "系统设置 › 网络 › 当前使用的服务 › 详细信息 › TCP/IP › 配置 IPv6"
  private static let ipv6Boundary = "开启不等于已泄露；IPv6 流量是否经代理以出口对照为准"

  /// One row after WebRTC. Report text carries states only, never addresses.
  static func ipv6Row(_ report: EnvironmentDiagnosticReport?, waiting: Bool) -> Row {
    func row(_ value: String, _ detail: String, _ tone: Tone) -> Row {
      Row(title: ipv6Title, symbol: "network", value: value, detail: detail, tone: tone)
    }
    guard let report else { return row(waiting ? "正在检测…" : "尚未检测", "", .pending) }
    switch ipv6Exposure(report) {
    case .on:
      let interface = report.mac.checks?.first { $0.kind == "ipv6_route" }?.value ?? ""
      var lines = ["Mac 存在 IPv6 默认路由" + (interface.isEmpty ? "" : "（" + interface + "）")]
      for (name, sample) in [("Mac", report.mac), ("浏览器", report.browser)] {
        let check = sample.checks?.first { $0.kind == "ipv6" && $0.state == "observed" }
        lines.append(
          name + " IPv6 出口："
            + (check.map { $0.value + ($0.at.map { " · 测量于 " + timeText($0) } ?? "") } ?? "未取得"))
      }
      lines += ["修改路径：" + ipv6Path, ipv6Boundary]
      return row(
        "存在潜在风险 · 已开启，尚未确认 IPv6 出口是否经代理", lines.joined(separator: "\n"), .difference)
    case .off:
      return row(
        "本项不适用 · 未开启，无 IPv6 默认路由",
        "Mac 没有 IPv6 默认路由；Mac 与浏览器的 IPv6 请求不适用：本机未开启 IPv6", .information)
    case .old:
      return row("旧记录未采集", "", .pending)
    default:
      if !report.mac.complete && waiting { return row("正在检测…", "", .pending) }
      return row("暴露风险待确认 · 无法读取 IPv6 默认路由", "未取得 Mac 的 IPv6 默认路由证据，不视为未开启。", .pending)
    }
  }

  /// Report variant of the row's 依据.
  static func ipv6ReportDetail(_ report: EnvironmentDiagnosticReport?) -> String {
    switch ipv6Exposure(report) {
    case .on: "存在 IPv6 默认路由；" + ipv6Boundary
    case .off: "无 IPv6 默认路由；IPv6 请求不适用"
    case .old: "旧记录未采集"
    case .unreadable: "未取得 IPv6 默认路由证据"
    case nil: "尚未检测"
    }
  }
}
