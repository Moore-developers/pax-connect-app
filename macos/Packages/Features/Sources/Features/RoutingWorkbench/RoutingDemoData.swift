import Foundation

extension RoutingWorkbenchStore {
  static var demoApplications: [Application] {
    [
      .init(id: "safari", name: "Safari", helpers: ["com.apple.WebKit.Networking"]),
      .init(id: "telegram", name: "Telegram", helpers: ["Telegram Helper"]),
      .init(id: "store", name: "App Store", helpers: []),
      .init(id: "notes", name: "备忘录", helpers: []),
    ]
  }
  static func demoConnections(session: WorkbenchDemoStore, now: Date) -> [Connection] {
    let routes = session.routes
    let route = routes.first?.id ?? "vless-fast"
    let second = routes.dropFirst().first?.id ?? route
    let title = routes.first?.name ?? "已保存线路"
    let secondTitle = routes.dropFirst().first?.name ?? title
    let definitions:
      [(
        String?, String, String?, String, String?, String, Direction, Hit, UInt64, UInt64, Int,
        Double?
      )] = [
        (
          "safari", "com.apple.WebKit.Networking", "github.com", "140.82.112.3", route, title,
          .proxy, .init(sourceID: "priority", ruleID: "github", title: "优先规则", match: "github.com"),
          2_000_000, 109_400_000, 16, 236
        ),
        (
          "safari", "com.apple.WebKit.Networking", "api.bilibili.com", "110.43.34.12", route, title,
          .proxy, .init(sourceID: nil, ruleID: nil, title: "无", match: "未命中已有规则"),
          420_000, 18_600_000, 8, 410
        ),
        (
          "safari", "Safari", "cn.bing.com", "13.107.21.200", nil, "本地网络", .direct,
          .init(sourceID: "domestic", ruleID: "bing", title: "国内服务直连", match: "精确域名"),
          820_000, 64_000_000, 16, 66
        ),
        (
          "store", "App Store", "apps.apple.com", "17.253.144.10", nil, "本地网络", .direct,
          .init(sourceID: "geo", ruleID: "cn-domain", title: "Geo 基础包", match: "中国大陆域名"),
          180_000, 24_000_000, 16, 96
        ),
        (
          "telegram", "Telegram Helper", "api.telegram.org", "149.154.167.220", second, secondTitle,
          .proxy,
          .init(sourceID: "priority", ruleID: "telegram", title: "优先规则", match: "应用 Telegram"),
          640_000, 8_600_000, 28, 184
        ),
        (
          nil, "unknown-helper", nil, "1.1.1.1", route, title, .proxy,
          .init(sourceID: nil, ruleID: nil, title: "无", match: "应用/域名未识别"),
          16_000, 32_000, 2, nil
        ),
      ]
    return definitions.enumerated().flatMap { definitionIndex, definition in
      let (
        application, process, domain, ip, routeID, routeName, direction, hit, up, down, count,
        latency
      ) = definition
      return (0..<count).map { index in
        let started = now.addingTimeInterval(index % 3 == 0 ? -7200 : -Double(10 + index))
        return Connection(
          id: "demo-\(definitionIndex)-\(index)", applicationID: application, process: process,
          domain: domain, ip: ip, routeID: routeID, routeName: routeName, direction: direction,
          hit: snapshotHit(hit), started: started, ended: nil,
          establishmentMS: latency.map { index == 0 ? $0 : $0 * Double(3 + index % 4) / 8 },
          transfers: (index % 3 == 0
            ? [
              .init(
                at: now.addingTimeInterval(-3600), upload: up / UInt64(count) * 6,
                download: down / UInt64(count) * 6)
            ] : []) + [
              .init(
                at: now.addingTimeInterval(-5),
                upload: up / UInt64(count) + (index == 0 ? up % UInt64(count) : 0),
                download: down / UInt64(count) + (index == 0 ? down % UInt64(count) : 0))
            ])
      }
    }
  }
  private static func snapshotHit(_ hit: Hit) -> Hit {
    var value = hit
    switch hit.ruleID {
    case "github": value.signature = Method.domain.rawValue + "|github.com"
    case "telegram": value.signature = Method.application.rawValue + "|telegram"
    case "bing": value.signature = Method.domain.rawValue + "|cn.bing.com"
    case "cn-domain": value.signature = Method.domainSuffix.rawValue + "|apple.com"
    default: break
    }
    return value
  }
}
