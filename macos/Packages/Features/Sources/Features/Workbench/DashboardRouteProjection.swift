import Foundation
import PaxCore
import PaxUI

/// 概览 v2 各卡的投影：输入 Snapshot / 历史 / 桶，输出 PaxUI 值模型。纯函数，不读 Store、不发请求。
enum DashboardFormat {
  /// 「HH:mm」，按调用方日历的时区。
  static func clock(_ date: Date, calendar: Calendar = .current) -> String {
    let parts = calendar.dateComponents([.hour, .minute], from: date)
    return String(format: "%02d:%02d", parts.hour ?? 0, parts.minute ?? 0)
  }
}

enum DashboardRouteProjection {
  /// 后台自动延迟的指标名，与 daemon 约定（decisions/route-auto-latency.md）。
  static let metric = "latency_auto"
  /// 与 daemon 对 `latency_auto` 的保留条数一致（config.HistoryLimit）。
  static let capacity = 120
  /// 当前卡只展示最近30次有效测量。
  static let displayCapacity = 30

  /// 当前线路本配置版本的自动测量，按时间升序、最多最近 120 条。
  /// 失败样本保留（用于「最近一次失败」原因），是否画点由调用方按 `success` 决定。
  static func samples(_ items: [RouteHistorySample], route: NodeProfile?) -> [RouteHistorySample] {
    guard let route else { return [] }
    let matching = items.filter {
      $0.metric == metric && $0.configRevision == route.configRevision
        && $0.at.timeIntervalSince1970 > 0
    }
    return Array(matching.sorted { $0.at < $1.at }.suffix(capacity))
  }

  static func isMeasured(_ sample: RouteHistorySample) -> Bool {
    sample.success && sample.value.isFinite && sample.value >= 0
  }

  /// 已保存的出口测量：只认 `exitSummary` 且与当前配置版本一致。成功取最新 IP / 地区；
  /// 最近一次失败时 IP 与地区一并回退 `lastLocation`。无任何可显示内容返回 nil。
  @MainActor static func savedExit(_ node: NodeProfile) -> (ip: String?, region: String?)? {
    guard let exit = node.exitSummary, exit.testedConfigRevision == node.configRevision else {
      return nil
    }
    let location = exit.location ?? exit.lastLocation
    let ip = [exit.ip, location?.ip ?? ""].first { !$0.isEmpty }
    let region = location.map { ExitLocationPresentation.region($0) }
    let shown = region == "地区未获取" ? nil : region
    return ip == nil && shown == nil ? nil : (ip, shown)
  }

  /// 「当前线路」卡。`running` 为代理是否开启；`exitIP` / `exitRegion` 是已保存测量的出口 IP 与地区（均可空；无地区只显示 IP）。
  static func card(
    route: NodeProfile?, badge: String, exitIP: String?, exitRegion: String? = nil,
    running: Bool,
    history: [RouteHistorySample], calendar: Calendar = .current
  ) -> PaxCurrentRouteModel {
    guard let route else {
      if running {
        return PaxCurrentRouteModel(
          name: "当前线路待确认", badge: badge, tags: [],
          note: "请先关闭代理，再重新连接", trendEmpty: "后台已恢复，尚未确认当前线路")
      }
      return PaxCurrentRouteModel(
        name: "", badge: badge, tags: [], note: "添加线路后开启代理", trendEmpty: "暂无线路")
    }
    var model = PaxCurrentRouteModel(
      name: route.name, badge: badge, tags: [route.protocolName.uppercased()])
    model.exitIP = exitIP
    model.exitRegion = exitRegion
    let all = samples(history, route: route)
    model.measurementTime = all.last.map { DashboardFormat.clock($0.at, calendar: calendar) }
    let good = Array(all.filter(isMeasured).suffix(displayCapacity))
    let waiting = running ? "等待首次自动测量" : "代理开启后自动测量"
    let waitingNote = running ? waiting : "代理未开启"
    model.trendEmpty =
      good.isEmpty ? waiting : ""
    if let last = all.last, !last.success {
      model.note = "最近一次测量失败 · " + DashboardFormat.clock(last.at, calendar: calendar)
    } else if all.isEmpty || good.isEmpty {
      model.note = waitingNote
    } else if let last = good.last {
      model.rtt = last.value.rounded()
    }
    if model.rtt != nil, let last = good.last {
      if good.count >= 2 {
        let change = Int((last.value - good[good.count - 2].value).rounded())
        model.delta = change == 0 ? "持平" : "\(change > 0 ? "↑" : "↓") \(abs(change)) ms"
      } else {
        model.delta = "首次测量"
      }
    }
    model.pinKey = "route-\(route.id)-\(route.configRevision)"
    let points = good.map { sample in
      PaxTimeSeriesPoint(
        at: sample.at, value: sample.value,
        sampleID:
          "\(sample.configRevision)|\(sample.metric)|\(sample.network)|\(sample.method)|\(sample.at.timeIntervalSince1970)|\(sample.value)",
        note: "测量时间")
    }
    model.series = PaxTimeSeries(
      domain: .init(start: good.first?.at ?? .distantPast, end: good.last?.at ?? .distantPast),
      points: points, axis: .samples)
    model.axis = points.isEmpty ? [] : ["最近 \(points.count) 次有效测量"]
    return model
  }
}
