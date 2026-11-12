import Foundation
import PaxCore
import PaxUI

/// Recent actual observations; the query filters the source, not the chart's spacing.
func workbenchLatencySeries(
  _ row: PaxCore.TrafficRow, from: Date, asOf: Date, coverageStart: Date? = nil,
  compact: Bool = false
) -> PaxTimeSeries? {
  workbenchResponseSeries(
    row.recentResponses, from: from, asOf: asOf,
    limit: compact ? 20 : 60, dns: row.kind == "dns")
}

func workbenchResponseSeries(
  _ responses: [TrafficResponsePoint], from: Date, asOf: Date, limit: Int, dns: Bool = false
) -> PaxTimeSeries {
  let points = responses.suffix(limit).map {
    PaxTimeSeriesPoint(
      at: $0.startedAt, value: Double($0.milliseconds), sampleID: $0.connectionKey,
      note: dns ? "查询开始时间" : "连接开始时间")
  }
  return PaxTimeSeries(domain: .init(start: from, end: asOf), points: points, axis: .samples)
}

func workbenchResponseContext(state: String, count: Int, dns: Bool) -> String {
  let noun = dns ? "解析耗时" : "有效响应"
  switch state {
  case "loading": return "正在读取近期\(noun)"
  case "failed": return "近期\(noun)读取失败"
  case "partial": return "最近 \(count) 次\(noun) · 记录不完整"
  case "ready": return count == 0 ? "暂无\(noun)" : "最近 \(count) 次\(noun)"
  default: return "近期\(noun)暂不可用"
  }
}
