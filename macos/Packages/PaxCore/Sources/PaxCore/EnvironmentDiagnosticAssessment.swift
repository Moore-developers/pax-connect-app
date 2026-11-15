import Foundation
import PaxClient

extension EnvironmentDiagnosticPresentation {
  /// Compact conclusions preserve full readings in the same row's details.
  public static func assessedRows(
    _ report: EnvironmentDiagnosticReport?, local: NetworkDiagnosticEnvironment?, waiting: Bool,
    includePhysical: Bool = true
  ) -> [Row] {
    let raw = mergedRows(report, local: local, waiting: waiting)
    guard let report else { return raw }
    let mac = report.mac
    let web = report.browser
    return raw.map { row in
      if row.title == locationTitle || row.title == ipv6Title { return row }
      var value = row.value
      var tone = row.tone
      var extra = riskExplanation(for: row.title)
      switch row.title {
      case "公网 IP":
        let pairs = [("IPv4", mac.ipv4, web.ipv4), ("IPv6", mac.ipv6, web.ipv6)].filter {
          !$0.1.isEmpty && !$0.2.isEmpty
        }
        if let different = pairs.first(where: { $0.1 != $0.2 }) {
          value = "发现出口差异 · Mac 与浏览器 \(different.0) 出口不同"
          tone = .difference
        } else if let first = pairs.first {
          value = "未发现出口差异 · \(first.0) 出口一致 · \(first.1)"
          tone = .information
        } else {
          value = waiting ? "正在获取两侧出口" : "出口风险待确认 · 资料不全，无法对照"
          tone = waiting ? .pending : .failure
        }
      case "语言":
        let a = local?.languages.map { Locale(identifier: $0).identifier } ?? []
        let b = web.languages.map { Locale(identifier: $0).identifier }
        if !a.isEmpty && !b.isEmpty {
          let locale = Locale(identifier: "zh_CN")
          let names = web.languages.map { locale.localizedString(forIdentifier: $0) ?? $0 }
          value =
            a == b
            ? "未发现语言差异 · " + names.joined(separator: " → ")
            : "发现语言差异 · Mac 与浏览器的语言顺序不同"
          tone = .information
        } else {
          value = waiting ? "正在读取两侧语言" : "语言差异待确认 · 资料不全，无法对照"
        }
      case "时区":
        if let zone = local?.timezone, !zone.isEmpty, !web.timezone.isEmpty,
          let a = TimeZone(identifier: zone), let b = TimeZone(identifier: web.timezone)
        {
          if zone == web.timezone {
            value =
              "未发现时区差异 · " + timezone(zone, at: report.at)
          } else {
            value =
              a.secondsFromGMT(for: report.at) == b.secondsFromGMT(for: report.at)
              ? "发现时区差异 · 名称不同，本次 UTC 偏移相同" : "发现时区差异 · Mac 与浏览器的时差不同"
          }
          tone = zone == web.timezone ? .information : .difference
        } else {
          value = waiting ? "正在读取两侧时区" : "时区差异待确认 · 资料不全，无法对照"
        }
      case "DNS":
        let verdict = dnsPathAssessment(report, waiting: waiting)
        value = verdict.text
        tone = verdict.tone
        let a = Set(mac.resolvers.map(\.ip))
        let b = Set(web.resolvers.map(\.ip))
        extra =
          "Mac \(a.count) 个解析器，浏览器 \(b.count) 个解析器。\n"
          + dnsPathDetails(mac, name: "Mac") + "\n" + dnsPathDetails(web, name: "浏览器")
      case "WebRTC":
        let verdict = webRTCVerdict(web, pending: web.complete != true && waiting)
        value = verdict.text
        tone = verdict.tone
        if includePhysical, let physical = verdict.physicalDetail { extra = physical }
      default: break
      }
      var detail = row.value + "\n" + row.detail + (extra.isEmpty ? "" : "\n" + extra)
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

  /// Shared by structured UI details and the report's evidence text.
  public static func riskExplanation(for title: String) -> String {
    switch title {
    case "公网 IP": "仅对照本次 Mac 与浏览器的同地址族出口；出口一致不代表已确认安全。"
    case "语言": "只对照两侧语言顺序，不判断是否与出口所在地一致；设置一致不代表安全。"
    case "时区": "只对照两侧时区，不判断是否与出口所在地一致；设置一致不代表安全。"
    case "DNS": "解析器地区和集合差异不能判定请求是否经代理，以下以本轮请求路径证据为准。"
    default: ""
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
