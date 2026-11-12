import AppKit
import PaxCore
import PaxUI

/// Trend analysis of P07d-ai-session-limits §4.3 for one session's main-line requests: context
/// layers over a request index above, single-request input cost below. This card turns trend
/// rows into display data; `PaxContextTrendChart` only draws it.
@MainActor final class AILimitTrendCard {
  let view: NSView
  private let options: AISessionOptions
  private let basis = PaxTabs(["官方费用", "渠道费用"])
  private let chart = PaxContextTrendChart(frame: .zero)
  private let layerLegend = PaxSeriesLegend()
  private let bandLegend = PaxSeriesLegend()
  private var trend: AILimitTrend?

  init(options: AISessionOptions) {
    self.options = options
    let note = PaxFeatureTag(
      "上下文水位与单次输入费用 · 悬停看明细", tone: .red, foregroundColor: PaxTheme.Color.text)
    note.toolTip = "仅看主会话，不含子代理；展示所保留的全部请求。"
    let header = AIUsageLayout.stack(
      [
        AIUsageLayout.heading("趋势分析"), note, NSView(), basis,
      ], vertical: false)
    header.spacing = PaxTheme.Spacing.tight
    basis.setAccessibilityIdentifier("ai-session-trend-basis")
    bandLegend.items = zip(AILimitStyle.bandNames, AILimitStyle.bandColors).map {
      .init($0, color: $1)
    }
    let card = AIUsageLayout.card(
      AIUsageLayout.stack([
        AIUsageLayout.stack([layerLegend, NSView(), bandLegend], vertical: false),
        chart,
      ]))
    view = AIUsageLayout.stack([header, card])
    view.setAccessibilityIdentifier("ai-session-trend-section")
    chart.heightAnchor.constraint(equalToConstant: PaxContextTrendChart.preferredHeight)
      .isActive = true
    chart.tokenFormat = AILimitStyle.axisTokens
    chart.costFormat = AILimitStyle.axisCost
    basis.onSelect = { [weak self] index in
      self?.options.cacheChannel = index == 1
      if let trend = self?.trend { self?.apply(trend) }
    }
  }

  func apply(_ trend: AILimitTrend) {
    self.trend = trend
    let useChannel = options.cacheChannel
    let rows = trend.requests
    let write = trend.hasWriteLayer
    let switches = Dictionary(trend.switches.map { ($0.index, $0) }, uniquingKeysWith: { $1 })
    var layers: [(String, NSColor, (AILimitRequest) -> Int)] = [
      ("缓存读取", AILimitStyle.cacheRead, \.read)
    ]
    if write { layers.append(("缓存写入", AILimitStyle.cacheWrite, \.write)) }
    layers.append(("普通输入", AILimitStyle.fresh, \.fresh))
    var content = PaxContextTrendChart.Content(
      layers: layers.map { name, color, tokens in
        // Tokens are known even when unpriced; only the cost panel breaks (hollow circle).
        .init(name: name, color: color, values: rows.map { Double(tokens($0)) })
      },
      lines: lines(trend),
      costs: rows.map { row in
        .init(
          value: row.costs(channel: useChannel)?.total,
          color: AISessionLimitsMath.pointBand(row).map { AILimitStyle.bandColors[$0.band] }
            ?? AILimitStyle.neutral)
      })
    if let limit = trend.limit {
      content.danger = .init(start: Double(limit.limit), color: AILimitStyle.limitLine)
    }
    if let start = trend.overStart {
      content.firstOver = .init(
        index: start, text: "第\(start + 1)次起超限", color: AILimitStyle.bandLabels[2])
    }
    content.markers = trend.switches.map { marker in
      switch marker.kind {
      case .model:
        let row = rows[marker.index]
        return .init(
          index: marker.index,
          kind: .label(
            title: row.model, annotation: row.effort.isEmpty ? "未记录" : row.effort,
            color: AILimitStyle.effortTone(row.effort).color,
            details: Self.comparisons(marker, compact: true)))
      case .effort(let from, let to):
        return .init(index: marker.index, kind: .tick("\(from)→\(to)"))
      }
    }
    content.hovers = rows.indices.map {
      hover(rows[$0], index: $0, marker: switches[$0], write: write, channel: useChannel)
    }
    basis.selectedIndex = useChannel ? 1 : 0
    layerLegend.items = layers.map { .init($0.0, color: $0.1) }
    chart.content = content
    chart.setAccessibilityLabel(
      "趋势分析；\(rows.count)次主会话请求；上图为上下文堆叠，下图为单次输入费用")
  }

  /// Warning line orange, limit line Coral; the flat-case limit one step darker.
  private func lines(_ trend: AILimitTrend) -> [PaxContextTrendChart.Line] {
    // Lines appear only once the session has come near the limit.
    guard let limit = trend.limit else { return [] }
    var lines: [PaxContextTrendChart.Line] = [
      .init(
        value: Double(limit.warn), label: "预警 " + AILimitStyle.k(limit.warn),
        color: AILimitStyle.warnLine, labelColor: AILimitStyle.bandLabels[1], dash: [2, 4]),
      .init(
        value: Double(limit.limit), label: "超限 " + AILimitStyle.k(limit.limit),
        color: AILimitStyle.limitLine, labelColor: AILimitStyle.bandLabels[2], dash: [6, 4]),
    ]
    if limit.hasSecondLimit {
      lines.append(
        .init(
          value: Double(limit.flatLimit), label: "超限 " + AILimitStyle.k(limit.flatLimit),
          color: AILimitStyle.flatLine, dash: [6, 4]))
    }
    return lines
  }

  /// Both ratios use official prices. Describe the change, rather than the resulting ratio:
  /// 9.9 times the previous cost is an increase of 890%, not 990%.
  private static func comparisons(
    _ marker: AILimitSwitch, compact: Bool = false
  ) -> [PaxContextTrendChart.Marker.Detail] {
    [
      change(compact ? "输入费" : "官方输入费", ratio: marker.turnMultiple, compact: compact),
      change(compact ? "缓存读单价" : "官方缓存读单价", ratio: marker.afterReadMultiple, compact: compact),
    ]
  }

  private static func change(
    _ name: String, ratio: Double?, compact: Bool
  ) -> PaxContextTrendChart.Marker.Detail {
    guard let ratio else { return .init(name + (compact ? " —" : "无法比较")) }
    guard ratio != 1 else { return .init(name + (compact ? " 不变" : "不变")) }
    let increase = ratio > 1
    let difference = abs(ratio - 1)
    let percentage = AIUsageFormatting.percent(difference)
    let value =
      percentage == "0.0%" ? "<0.1%" : percentage.replacingOccurrences(of: ".0%", with: "%")
    let direction = increase ? "↑" : "↓"
    return .init(
      name + (compact ? " " : (increase ? "增长 " : "降低 ")),
      value: (compact ? direction : "") + value,
      color: increase ? PaxTheme.DashboardColor.redLabel : PaxTheme.DashboardColor.greenLabel)
  }

  private func hover(
    _ row: AILimitRequest, index: Int, marker: AILimitSwitch?, write: Bool, channel: Bool
  ) -> PaxAnalysisChart.Hover {
    let costs = row.costs(channel: channel)
    let other = row.costs(channel: !channel)
    var breakdown: [PaxAnalysisChart.Hover.Breakdown] = []
    if let costs, costs.total > 0 {
      var parts: [(NSColor, String, Double)] = [(AILimitStyle.cacheRead, "缓存读取", costs.read)]
      if write { parts.append((AILimitStyle.cacheWrite, "缓存写入", costs.write)) }
      parts.append((AILimitStyle.fresh, "普通输入", costs.fresh))
      breakdown = parts.map { color, name, amount in
        .init(
          color: color, name: name, amount: AILimitStyle.money(amount),
          share: AIUsageFormatting.percent(amount / costs.total), weight: amount)
      }
    }
    var tag: PaxAnalysisChart.Hover.Tag?
    var gauge: PaxAnalysisChart.Hover.Gauge?
    if let limit = row.lines,
      let point = AISessionLimitsMath.pointBand(row)
    {
      let color = AILimitStyle.bandLabels[point.band]
      tag = .init(AILimitStyle.bandNames[point.band], color: color)
      var ticks: [PaxAnalysisChart.Hover.Gauge.Tick] = [
        .init(Double(limit.warn), color: AILimitStyle.warnLine),
        .init(Double(limit.limit), color: AILimitStyle.limitLine),
      ]
      if limit.hasSecondLimit {
        ticks.append(.init(Double(limit.flatLimit), color: AILimitStyle.flatLine))
      }
      let total = Double(limit.hasSecondLimit ? limit.flatLimit : limit.limit) * 1.2
      gauge = .init(
        value: Double(row.context), total: max(total, Double(row.context)), ticks: ticks,
        color: AILimitStyle.bandColors[point.band],
        label: "上下文 " + AILimitStyle.k(row.context) + " / " + AILimitStyle.k(point.limit))
    }
    var footer: [String] = []
    if let other { footer.append((channel ? "官方 " : "渠道 ") + AILimitStyle.money(other.total)) }
    if row.context > 0 {
      footer.append(
        "缓存命中 " + AIUsageFormatting.percent(Double(row.read) / Double(row.context)))
    }
    var details: [(label: String, value: String)] = []
    if let marker {
      switch marker.kind {
      case .model(let from, let to):
        details.append(("换模型 \(from)→\(to)", ""))
        details += Self.comparisons(marker).map { (label: $0.text, value: $0.value) }
        details.append(("输入费含普通输入和缓存读写，与上次请求比较", ""))
        details.append(("读单价：切换后首笔对比切换前最近一笔缓存读", ""))
      case .effort(let from, let to):
        details.append(("换强度 \(from)→\(to)", ""))
      }
    }
    return .init(
      title: "第 \(index + 1) 次" + (row.turn.map { " · 第 \($0 + 1) 轮" } ?? ""),
      subtitle: AIUsageFormatting.date(row.at, time: true),
      summary: [
        (
          costs == nil ? "单次输入" : "单次输入 · " + (channel ? "渠道" : "官方"),
          costs.map { AILimitStyle.money($0.total) } ?? "未定价"
        )
      ],
      proportion: nil, proportionLabel: "", proportionValue: "", details: details,
      context: footer.joined(separator: " · "),
      badges: [
        .init(row.model, color: PaxFeatureTag.Tone.purple.color),
        .init(
          row.effort.isEmpty ? "未记录" : row.effort,
          color: AILimitStyle.effortTone(row.effort).color),
      ], tag: tag, gauge: gauge, breakdown: breakdown)
  }
}
