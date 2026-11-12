import Foundation
import PaxCore
import PaxUI

/// All latency surfaces use the same ordered observations and historical labels.
func workbenchLatencySamples(_ row: PaxCore.TrafficRow) -> [PaxTrendSample] {
  if row.latencyAggregated {
    let ordered = row.latencySamples.sorted { $0.at < $1.at }
    let first = ordered.first?.at ?? Date()
    let last = ordered.last?.at ?? first
    let span = max(1, last.timeIntervalSince(first))
    return ordered.map { sample in
      return PaxTrendSample(
        value: sample.known ? Double(sample.milliseconds) : nil,
        label: TrafficFormatting.dateMinute(sample.at)
          + (sample.known
            ? " · " + TrafficFormatting.milliseconds(Double(sample.milliseconds)) + " · 桶内最新真实观测"
            : " · 未采集响应"),
        position: sample.at.timeIntervalSince(first) / span)
    }
  }
  return TrafficFormatting.latencySamples(row).map {
    PaxTrendSample(
      value: $0.milliseconds,
      label: TrafficFormatting.dateMinute($0.at) + " · "
        + TrafficFormatting.milliseconds($0.milliseconds)
        + (row.latencySampled ? " · 已抽样" : ""))
  }
}
