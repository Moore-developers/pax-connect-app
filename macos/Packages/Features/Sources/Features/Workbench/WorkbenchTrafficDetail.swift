import Foundation
import PaxCore
import PaxUI

/// Dashboard 隔离演示的连接事实；字节是指定窗口的增量，不是连接生命周期累计。
struct WorkbenchTrafficRecord: Equatable, Identifiable {
  struct Hit: Hashable {
    let condition: String
    let kind: String
    let source: String
    var sourceKey: String? = nil
  }
  let id: String
  let applicationID: String
  let application: String
  let process: String
  let domain: String?
  let ip: String
  let routeID: String
  let route: String
  let direction: String
  var directionCode = ""
  var actualConnectionKey = ""
  let started: Date
  let transport: String
  let ended: Bool
  let failure: String?
  let establishmentMS: Double?
  let upload: UInt64
  let download: UInt64
  let hit: Hit
  var port: UInt16? = nil

  var stateUnknown = false
  var latencyNote = "历史未采集响应计时"
  var state: String { state(context: .chinese) }
  func state(context: PresentationContext) -> String {
    if stateUnknown { return context.text("traffic_details.1e3d78c3da") }
    return failure != nil
      ? context.text("traffic_details.73ee1fca67")
      : ended
        ? context.text("traffic_details.d79b1d0e5c") : context.text("traffic_details.3c01089ea0")
  }
  var target: String { target(context: .chinese) }
  func target(context: PresentationContext) -> String {
    domain ?? (ip.isEmpty ? context.text("traffic_details.947d020039") : ip)
  }
  var rawIP: String? = nil
  var sourceNote = ""
}

/// 打开时捕获的只读视图输入，所有汇总都由同一组连接记录计算。
@MainActor
struct WorkbenchTrafficDetail: Identifiable {
  struct Related: Equatable, Identifiable {
    let id: String
    let name: String
    let connectionIDs: Set<String>
    let download: UInt64
  }
  struct Basis: Equatable {
    let hit: WorkbenchTrafficRecord.Hit
    let count: Int
    var target: String = ""
    var route: String = ""
    var rawEvidence: [String] = []
  }
  var context: PresentationContext = .chinese
  let id: String
  let name: String
  let identity: String
  let symbol: String
  let dimension: TrafficDimension
  let capturedAt: Date
  let lowerBound: Date
  let periodTitle: String
  let interruption: DateInterval?
  var recordingMessage: String = ""
  let connections: [WorkbenchTrafficRecord]
  var aggregate: TrafficDetail? = nil
  var retainedConnectionCount: Int {
    aggregate.map { Int($0.retainedConnectionCount) } ?? connections.count
  }
  var connectionCount: Int { aggregate.map { Int($0.summary.connections) } ?? connections.count }
  var route: String { connections.first?.route ?? "—" }
  var direction: String { connections.first?.direction ?? "—" }
  var upload: UInt64 {
    if let aggregate { return aggregate.summary.upload }
    return connections.reduce(0) { $0 + $1.upload }
  }
  var download: UInt64 {
    if let aggregate { return aggregate.summary.download }
    return connections.reduce(0) { $0 + $1.download }
  }
  var successful: [WorkbenchTrafficRecord] {
    (aggregate.map { $0.timings.map { WorkbenchTrafficRecord($0, context: context) } }
      ?? windowConnections).filter {
        ($0.failure == nil || aggregate != nil) && $0.establishmentMS != nil
      }
  }
  var windowConnections: [WorkbenchTrafficRecord] {
    connections.filter { $0.started > lowerBound && $0.started <= capturedAt }
  }
  var slowest: Double? {
    if let aggregate {
      return aggregate.summary.latencyKnown
        ? Double(
          aggregate.summary.latencyAggregated
            ? aggregate.summary.p95MS : aggregate.summary.slowestLatencyMS) : nil
    }
    return successful.compactMap(\.establishmentMS).max()
  }
  var latestLatency: Double? {
    if let aggregate { return TrafficFormatting.latestLatency(aggregate.summary) }
    return successful.max(by: { $0.started < $1.started })?.establishmentMS
  }
  var samples: [PaxTrendSample] {
    if let aggregate {
      return workbenchLatencySamples(aggregate.summary)
    }
    guard !successful.isEmpty else { return [] }
    var values: [(Date, PaxTrendSample)] =
      (aggregate.map { $0.timings.map { WorkbenchTrafficRecord($0, context: context) } }
      ?? windowConnections).map {
        connection in
        (
          connection.started,
          PaxTrendSample(
            value: connection.establishmentMS,
            label: TrafficFormatting.dateMinute(connection.started) + " · "
              + (connection.failure != nil && connection.establishmentMS == nil
                ? context.text("traffic_details.4cab7e2408")
                : TrafficFormatting.milliseconds(connection.establishmentMS)))
        )
      }
    if let interruption {
      for time in [interruption.start, interruption.end] {
        values.append(
          (
            time,
            .init(
              value: nil, label: context.text("traffic_details.008b524f87"))
          ))
      }
    }
    return values.sorted { $0.0 < $1.0 }.map(\.1)
  }
  var quality: String {
    if aggregate?.summary.basis == "历史规则包未细分" {
      return context.text("traffic_details.3ae0843f52")
    }
    if aggregate?.summary.objectID == "unknown" && dimension == .domain {
      return context.text("traffic_details.e5a53ddc6c")
    }
    guard let interruption else { return recordingMessage }
    return context.text("traffic_details.ebd24ab2b5")
      + TrafficFormatting.dateMinute(interruption.start) + " — "
      + TrafficFormatting.dateMinute(interruption.end) + context.text("traffic_details.b6db0d17be")
  }
  var sampleSummary: String {
    if let aggregate {
      if TrafficFormatting.isIPv6LimitedPath(aggregate.summary) {
        return
          context.text(
            "traffic_details.026f760f62",
            [
              "arg0": String(describing: aggregate.summary.connections),
              "arg1": String(describing: aggregate.summary.failedCount),
              "arg2": String(describing: retainedConnectionCount),
            ])
      }
      if ["hour", "day"].contains(aggregate.window) {
        return
          context.text(
            "traffic_details.3e39dbbbd5",
            [
              "arg0": String(describing: aggregate.summary.connections),
              "arg1": String(describing: aggregate.summary.failedCount),
            ])
      }
      if aggregate.aggregateOnly {
        return
          context.text(
            "traffic_details.a90d75e9cb",
            [
              "arg0": String(describing: aggregate.summary.connections),
              "arg1": String(describing: aggregate.summary.validLatencyCount),
              "arg2": String(describing: aggregate.summary.failedCount),
            ])
      }
      return
        context.text(
          "traffic_details.5ba03e319b",
          [
            "arg0": String(describing: aggregate.summary.connections),
            "arg1": String(describing: aggregate.summary.validLatencyCount),
            "arg2": String(describing: aggregate.summary.missingLatencyCount),
            "arg3": String(describing: aggregate.summary.failedCount),
            "arg4": String(describing: aggregate.summary.earlierCount),
          ])

    }
    let failures = windowConnections.filter { $0.failure != nil }.count
    let missing = windowConnections.count - successful.count - failures
    let earlier = connections.count - windowConnections.count
    return [
      context.text("traffic_details.86a18339f0", ["arg0": String(describing: successful.count)]),
      missing > 0
        ? context.text("traffic_details.1d0a0bc001", ["arg0": String(describing: missing)]) : "",
      failures > 0
        ? context.text("traffic_details.8e8ed60db8", ["arg0": String(describing: failures)]) : "",
      earlier > 0
        ? context.text("traffic_details.865ce263c3", ["arg0": String(describing: earlier)]) : "",
    ].filter { !$0.isEmpty }.joined(separator: " · ")
  }
  var basis: [Basis] {
    if let aggregate {
      var result: [Basis] = []
      for saved in aggregate.basis {
        let raw =
          context.text("traffic_details.7851c3fac4")
          + TrafficRuleFormatting.recordedRuleText(saved.rule)
          + context.text("traffic_details.563cd1e4d3") + saved.source
        let route =
          saved.direction.isEmpty
          ? saved.routeName
          : TrafficFormatting.actualConnection(
            direction: saved.direction, route: saved.routeName, context: context)
        let ipv6 = TrafficRuleFormatting.isIPv4OnlyRule(rule: saved.rule, source: saved.source)
        if ipv6,
          let index = result.firstIndex(where: {
            TrafficRuleFormatting.isIPv4OnlyRule(rule: $0.hit.condition, source: $0.hit.source)
              && $0.target == saved.target && $0.route == route
          })
        {
          let old = result[index]
          result[index] = .init(
            hit: old.hit, count: old.count + Int(saved.connections),
            target: old.target, route: old.route,
            rawEvidence: old.rawEvidence.contains(raw) ? old.rawEvidence : old.rawEvidence + [raw])
        } else {
          result.append(
            .init(
              hit: .init(
                condition: saved.rule, kind: context.text("traffic_details.3fcb97b8dc"),
                source: saved.source),
              count: Int(saved.connections), target: saved.target, route: route,
              rawEvidence: [raw]))
        }
      }
      return result
    }
    var hits: [WorkbenchTrafficRecord.Hit] = []
    for connection in connections where !hits.contains(connection.hit) {
      hits.append(connection.hit)
    }
    return hits.map { hit in .init(hit: hit, count: connections.filter { $0.hit == hit }.count) }
  }
  var related: [Related] {
    if let aggregate {
      return aggregate.related.map {
        .init(
          id: $0.id, name: $0.name, connectionIDs: Set($0.connectionKeys), download: $0.download)
      }
    }
    let grouped = Dictionary(grouping: connections) { connection in
      dimension == .application ? connection.target : connection.applicationID
    }
    return grouped.map { key, values in
      Related(
        id: key,
        name: dimension == .application
          ? key : values.first?.application ?? context.text("traffic_details.c03d35ce7a"),
        connectionIDs: Set(values.map(\.id)), download: values.reduce(0) { $0 + $1.download })
    }.sorted { $0.download == $1.download ? $0.id < $1.id : $0.download > $1.download }
  }
}

extension WorkbenchDemoStore {
  var trafficDetails: [WorkbenchTrafficDetail] {
    guard !statisticsUnavailable else { return [] }
    let records = trafficRecords
    func key(_ record: WorkbenchTrafficRecord) -> String {
      let target: String
      switch dimension {
      case .application: target = record.applicationID
      case .domain: target = record.target
      case .ip: target = record.ip
      }
      return "\(dimension.rawValue)|\(target)|\(record.routeID)"
    }
    var order: [String] = []
    for record in records where !order.contains(key(record)) { order.append(key(record)) }
    let groups = Dictionary(grouping: records, by: key)
    return order.compactMap { id in
      guard let values = groups[id], let first = values.first else { return nil }
      let name: String
      let identity: String
      let symbol: String
      switch dimension {
      case .application:
        name = first.application
        identity = "应用 · " + first.applicationID
        symbol = "app"
      case .domain:
        name = first.target
        identity = "域名 · \(Set(values.map(\.ip)).count) 个已记录 IP"
        symbol = "globe"
      case .ip:
        name = first.ip
        identity = "IP · \(Set(values.compactMap(\.domain)).count) 个已观测域名"
        symbol = "network"
      }
      guard direction == "全部" || first.direction == direction,
        search.isEmpty || (name + first.route).localizedCaseInsensitiveContains(search)
      else { return nil }
      return WorkbenchTrafficDetail(
        id: id, name: name, identity: identity, symbol: symbol,
        dimension: TrafficDimension(rawValue: dimension.rawValue) ?? .application,
        capturedAt: trafficCapturedAt, lowerBound: trafficCapturedAt.addingTimeInterval(-60),
        periodTitle: "最近 1 分钟", interruption: trafficInterruption,
        recordingMessage: scenario == .disconnected ? "后台服务断开，保留最后记录" : "",
        connections: values)
    }
  }
  private var trafficInterruption: DateInterval? {
    scenario == .trafficIncomplete
      ? DateInterval(start: trafficCapturedAt.addingTimeInterval(-35), duration: 5) : nil
  }
  private var trafficRecords: [WorkbenchTrafficRecord] {
    // 确定的内存记录；列表与详情共用这一份，运行线路/规则修改不能重写历史。
    let fixtures: [(String, String, String, String, String, Int, UInt64, UInt64)] = [
      (
        "Safari", "www.youtube.com", "192.0.2.10", "代理", "hy2-racknerd-132944b", 38, 2_400_000,
        128_000_000
      ),
      (
        "Telegram", "api.telegram.org", "192.0.2.20", "代理", "hy2-racknerd-132944b", 28, 640_000,
        8_600_000
      ),
      ("Safari", "www.bilibili.com", "198.51.100.10", "直连", "本地网络", 16, 820_000, 64_000_000),
      ("App Store", "apps.apple.com", "198.51.100.20", "直连", "本地网络", 16, 180_000, 24_000_000),
      ("Safari", "github.com", "192.0.2.30", "代理", "美国-B", 30, 96_000, 3_200_000),
    ]
    let timings: [[Double]] = [
      [180, 220, 195, 410, 260, 172, 185, 340, 210, 168, 192, 201],
      [92, 112, 98, 105, 184, 118, 102, 96, 121, 108, 94, 101],
      [28, 33, 40, 31, 66, 35, 29, 30, 42, 34, 27, 32],
      [42, 55, 48, 70, 49, 45, 96, 51, 44, 47, 53, 46],
      [240, 218, 265, 310, 228, 205, 420, 260, 232, 218, 245, 226],
    ]
    let count = scenario == .dense ? 18 : scenario == .few ? 2 : fixtures.count
    return (0..<count).flatMap { index -> [WorkbenchTrafficRecord] in
      let f = fixtures[index % fixtures.count]
      let suffix = index >= fixtures.count ? " · \(index + 1)" : ""
      let appID =
        [
          "Safari": "com.apple.Safari", "Telegram": "org.telegram.desktop",
          "App Store": "com.apple.AppStore",
        ][f.0] ?? f.0
      let long = scenario == .longText && index == 0
      return (0..<f.5).compactMap { sample in
        let started = trafficCapturedAt.addingTimeInterval(
          -59 + Double(sample) * 57 / Double(f.5 - 1))
        guard trafficInterruption?.contains(started) != true else { return nil }
        let failed = sample == f.5 - 1
        let udp = sample == f.5 - 2
        let domain: String? =
          index == 0 && failed
          ? nil
          : index == 0 && sample % 3 == 1 ? "rr1.googlevideo.com" : f.1
        let fallback = failed || sample % 11 == 10
        let condition = domain == "rr1.googlevideo.com" ? "googlevideo.com" : (domain ?? f.2)
        let hit = WorkbenchTrafficRecord.Hit(
          condition: fallback ? "未命中规则" : condition,
          kind: fallback ? "走当前线路" : "域名及子域",
          source: fallback ? "无" : index == 0 ? "影音服务" : f.3 == "直连" ? "国内服务" : "常用服务")
        func share(_ total: UInt64) -> UInt64 {
          guard !failed else { return 0 }
          let divisor = UInt64(f.5 - 1)
          return total / divisor + (sample == 0 ? total % divisor : 0)
        }
        return WorkbenchTrafficRecord(
          id: "traffic-\(index)-\(sample)", applicationID: appID + suffix,
          application: f.0 + suffix + (long ? " · 用于检查长文本截断的应用对象" : ""),
          process: f.0 == "Safari" ? "com.apple.WebKit.Networking" : appID,
          domain: domain, ip: failed && index == 0 ? "192.0.2.40" : f.2,
          routeID: f.4, route: f.4 + (long ? "-very-long-route-identifier-443" : ""),
          direction: f.3, started: started, transport: udp ? "UDP" : "TCP", ended: sample % 3 == 0,
          failure: failed ? "连接超时" : nil,
          establishmentMS: failed || udp || scenario == .establishmentMissing
            ? nil : timings[index % timings.count][sample % timings[0].count],
          upload: share(f.6), download: share(f.7), hit: hit, port: 443)
      }
    }
  }
}
