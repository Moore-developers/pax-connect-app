import PaxCore
import PaxUI

/// Present only this route's frozen sample; rank the six directions by threshold utilization.
@MainActor enum RouteAlertProgressPresentation {
  static func apply(
    _ progress: RouteAlertProgress?, to view: PaxRouteAlertReadout,
    context: PresentationContext, unavailable: String
  ) {
    guard let progress else {
      view.apply(focus: nil, periods: [], note: unavailable)
      return
    }
    let settings = progress.settings
    let state = progress.monitorState
    guard settings.enabled, state == "collecting" || state == "partial" else {
      let knownStates = ["disabled", "checking", "failed", "proxy_off", "collection_interrupted"]
      let key = knownStates.contains(state) ? state : "checking"
      view.apply(focus: nil, periods: [], note: context.text("alerts.state." + key))
      return
    }
    let rules: [(UInt32, TrafficAlertRule)] = [
      (300, settings.short), (settings.mediumSeconds, settings.medium), (86400, settings.day),
    ]
    var periods: [PaxRouteAlertReadout.Period] = []
    var focus: PaxRouteAlertReadout.Focus?
    var highest: Double = -1
    var incomplete = false
    for (seconds, rule) in rules {
      let sample = progress.windows.first { $0.seconds == seconds }
      let window = context.text("alerts.window." + String(seconds))
      let partial = sample?.partial == true
      incomplete = incomplete || partial
      let missing = context.text(
        rule.enabled ? "route_progress.unavailable" : "alerts.state.disabled")
      func value(_ used: UInt64?, _ threshold: UInt64) -> PaxRouteAlertReadout.DirectionValue {
        let limit = TrafficFormatting.bytes(threshold)
        guard rule.enabled, let used, threshold > 0 else {
          return .init(balance: missing, detail: "— / " + limit, usedFraction: nil, reached: false)
        }
        let usedText = TrafficFormatting.bytes(used)
        // A shared unit is printed once, e.g. 10 / 100 MB.
        let unit = limit.split(separator: " ").last.map { " " + $0 } ?? ""
        let compact = usedText.hasSuffix(unit) ? String(usedText.dropLast(unit.count)) : usedText
        let balance: String
        if used == threshold {
          balance = context.text("route_progress.reached")
        } else if used > threshold {
          balance = context.text(
            "route_progress.over_amount",
            ["amount": TrafficFormatting.bytes(used - threshold)])
        } else {
          balance = TrafficFormatting.bytes(threshold - used)
        }
        return .init(
          balance: balance, detail: compact + " / " + limit,
          usedFraction: Double(used) / Double(threshold), reached: used >= threshold)
      }
      let up = value(sample?.upload, rule.upload)
      let down = value(sample?.download, rule.download)
      let period = PaxRouteAlertReadout.Period(
        title: context.text("route_progress.period." + String(seconds)),
        detailCaption: context.text(
          partial ? "route_progress.observed_limit" : "route_progress.used_limit"),
        help: sample.map {
          context.text("route_progress.until", ["time": TrafficFormatting.time($0.until)])
        } ?? missing,
        upload: up, download: down)
      periods.append(period)
      guard let sample, let upRatio = up.usedFraction, let downRatio = down.usedFraction else {
        continue
      }
      let isUpload = upRatio >= downRatio
      let ratio = max(upRatio, downRatio)
      guard ratio > highest, !partial || ratio >= 1 else { continue }
      highest = ratio
      let (used, limit) = isUpload ? (sample.upload, rule.upload) : (sample.download, rule.download)
      let direction = context.text(isUpload ? "routes.usage_upload" : "routes.usage_download")
      let prefix: String
      if used == limit {
        prefix = ""
      } else if used > limit {
        prefix = context.text("route_progress.over")
      } else {
        prefix = context.text("route_progress.left")
      }
      focus = .init(
        title: window + " · " + direction,
        prefix: prefix,
        amount: used == limit
          ? context.text("route_progress.reached")
          : TrafficFormatting.bytes(used > limit ? used - limit : limit - used),
        used: context.text("route_progress.used_amount", ["amount": TrafficFormatting.bytes(used)]),
        threshold: context.text("route_progress.limit", ["amount": TrafficFormatting.bytes(limit)]),
        fraction: ratio,
        upload: upRatio.formatted(.percent.precision(.fractionLength(0))),
        download: downRatio.formatted(.percent.precision(.fractionLength(0))),
        help: period.help,
        reached: used >= limit)
    }
    let note: String
    if incomplete {
      note = context.text("route_progress.incomplete")
    } else if focus == nil {
      note = context.text("route_progress.no_rules")
    } else {
      note = ""
    }
    view.apply(focus: focus, periods: periods, note: note)
  }
}
