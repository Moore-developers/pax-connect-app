import Foundation
import PaxClient

extension EnvironmentDiagnosticPresentation {
  /// Compact conclusions preserve full readings in the same row's details.
  public static func assessedRows(
    _ report: EnvironmentDiagnosticReport?, local: NetworkDiagnosticEnvironment?, waiting: Bool
  ) -> [Row] {
    let raw = mergedRows(report, local: local, waiting: waiting)
    guard let report else { return raw }
    let mac = report.mac
    let web = report.browser
    return raw.map { row in
      var value = row.value
      var tone = row.tone
      switch row.title {
      case "公网 IP":
        let pairs = [("IPv4", mac.ipv4, web.ipv4), ("IPv6", mac.ipv6, web.ipv6)].filter {
          !$0.1.isEmpty && !$0.2.isEmpty
        }
        if let different = pairs.first(where: { $0.1 != $0.2 }) {
          value = "Mac 与浏览器 \(different.0) 出口不同"
          tone = .difference
        } else if let first = pairs.first {
          value = "Mac 与浏览器 \(first.0) 同出口 · \(first.1)"
          tone = .information
        } else {
          value = waiting ? "正在获取两侧出口" : "出口资料不全，无法对照"
          tone = waiting ? .pending : .failure
        }
        if mac.checks?.contains(where: { $0.kind == "ipv6_route" && $0.state == "absent" }) == true
        {
          value += "；本机无 IPv6 默认路由"
        }
      case "语言":
        let a = local?.languages.map { Locale(identifier: $0).identifier } ?? []
        let b = web.languages.map { Locale(identifier: $0).identifier }
        if !a.isEmpty && !b.isEmpty {
          value =
            a == b
            ? "Mac 与浏览器语言顺序相同 · " + web.languages.joined(separator: " → ")
            : "Mac 与浏览器语言偏好不同，按使用习惯核对"
          tone = .information
        } else {
          value = waiting ? "正在读取两侧语言" : "两侧语言资料不全，无法对照"
        }
      case "时区":
        if let zone = local?.timezone, !zone.isEmpty, !web.timezone.isEmpty,
          let a = TimeZone(identifier: zone), let b = TimeZone(identifier: web.timezone)
        {
          if zone == web.timezone {
            value =
              "Mac 与浏览器同为 " + zone + " · "
              + NetworkDiagnosticPresentation.utcOffset(zone, at: report.at)
          } else {
            value =
              a.secondsFromGMT(for: report.at) == b.secondsFromGMT(for: report.at)
              ? "时区名称不同，本次 UTC 偏移相同" : "Mac 与浏览器时差不同，核对预期设置"
          }
          tone = zone == web.timezone ? .information : .difference
        } else {
          value = waiting ? "正在读取两侧时区" : "两侧时区资料不全，无法对照"
        }
      case "DNS":
        let a = Set(mac.resolvers.map(\.ip))
        let b = Set(web.resolvers.map(\.ip))
        if !a.isEmpty && !b.isEmpty {
          value =
            a == b
            ? "两侧观察到相同解析器 · \(a.count) 个" : "Mac \(a.count) 个 / 浏览器 \(b.count) 个解析器；集合不同，不等于泄露"
          tone = .information
        } else {
          value = waiting ? "正在检测解析路径" : "DNS 采样不完整，不能判断解析路径"
          tone = waiting ? .pending : .failure
        }
      case "WebRTC":
        let publicAddresses = web.rtcAddresses.filter(isPublicAddress)
        let checks = (web.checks ?? []).filter { $0.kind.hasPrefix("rtc_") }
        if web.rtcState == "unsupported" {
          value = "浏览器不支持 WebRTC，无法检测"
          tone = .pending
        } else if !publicAddresses.isEmpty {
          let known = publicAddresses.allSatisfy {
            $0.contains(":") ? !web.ipv6.isEmpty : !web.ipv4.isEmpty
          }
          let equal = publicAddresses.allSatisfy { [web.ipv4, web.ipv6].contains($0) }
          value =
            !known
            ? "发现 \(publicAddresses.count) 个公网地址，缺少同地址族网页出口"
            : equal ? "公网候选与网页同出口 · \(publicAddresses.count) 个" : "公网候选与网页出口不同，需核对保护策略"
          tone = known && !equal ? .difference : .information
        } else if checks.contains(where: {
          $0.state == "timeout" || $0.state == "failed" || !$0.errorCode.isEmpty
        }) || web.rtcState == "failed" {
          value = "STUN 采集失败或到期，不能判断公网暴露"
          tone = .failure
        } else if !web.rtcState.isEmpty {
          value = web.rtcAddresses.isEmpty ? "未取得候选地址，不能据此确认防泄露" : "仅取得局域网 / mDNS 候选，未见公网地址"
          tone = .information
        } else {
          value = waiting ? "正在采集浏览器候选地址" : "浏览器未返回候选，无法判断"
          tone = .pending
        }
      default: break
      }
      var detail = row.value + "\n" + row.detail
      if row.title == "DNS" {
        for (name, sample) in [("Mac", mac), ("浏览器", web)] {
          detail +=
            "\n" + name + "全部解析器：\n"
            + sample.resolvers.map {
              $0.ip + " · " + NetworkDiagnosticPresentation.countryName($0.country) + " · "
                + $0.organization
            }.joined(separator: "\n")
        }
      }
      return Row(title: row.title, symbol: row.symbol, value: value, detail: detail, tone: tone)
    }
  }

  public static func exitAssessment(_ checks: [EnvironmentDiagnosticReport.Check])
    -> NetworkDiagnosticPresentation.Assessment
  {
    let a = checks.first { $0.kind == "echo_ipip" && $0.state == "observed" }?.value
    let b = checks.first { $0.kind == "echo_ipinfo" && $0.state == "observed" }?.value
    guard let a, let b, !a.isEmpty, !b.isEmpty else {
      return .init(text: "缺少两个目标的出口，无法判断分流", tone: .pending)
    }
    guard a.contains(":") == b.contains(":") else {
      return .init(text: "两个目标地址族不同，不能直接比较出口", tone: .information)
    }
    return .init(
      text: a == b ? "IPIP 与 IPinfo 本次同出口" : "IPIP 与 IPinfo 本次使用不同出口", tone: .information)
  }
}
