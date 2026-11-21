import AppKit
import PaxCore
import PaxUI

/// Trend analysis of P07d-ai-session-limits §4.3 for one session's main-line requests: context
/// layers over a request index above, single-request input cost below. This card turns trend
/// rows into display data; `PaxContextTrendChart` only draws it.
@MainActor final class AILimitTrendCard {
  let interfaceCopy: InterfaceCopyBindings
  var context: PresentationContext { interfaceCopy.context }

  let view: NSView
  private let options: AISessionOptions
  private let basis = PaxTabs([])
  private let chart = PaxContextTrendChart(frame: .zero)
  private let layerLegend = PaxSeriesLegend()
  private let bandLegend = PaxSeriesLegend()
  private var trend: AILimitTrend?

  init(options: AISessionOptions, interfaceCopy: InterfaceCopyBindings = InterfaceCopyBindings()) {
    self.interfaceCopy = interfaceCopy
    let context = interfaceCopy.context
    chart.tokenTitle = context.text("session_chart.context_title")
    chart.costTitle = context.text("session_chart.cost_title")
    chart.emptyMessage = context.text("session_chart.empty")
    basis.apply(labels: [
      context.text("session_limits.c0b69a3163"), context.text("session_limits.4d14ea51f5"),
    ])

    self.options = options
    let note = interfaceCopy.bind(
      PaxFeatureTag(
        context.text("session_limits.265751ff1a"), tone: .red, foregroundColor: PaxTheme.Color.text),
      key: "session_limits.265751ff1a"
    ) { $0.applyTitle($1) }
    note.toolTip = context.text("session_limits.1a959ea681")
    let header = AIUsageLayout.stack(
      [
        interfaceCopy.usageHeading("session_limits.95b0287904"), note, NSView(), basis,
      ], vertical: false)
    header.spacing = PaxTheme.Spacing.tight
    basis.setAccessibilityIdentifier("ai-session-trend-basis")
    bandLegend.items = zip(AILimitStyle.bandNames(context: context), AILimitStyle.bandColors).map {
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

  func invalidateLanguage() {
    chart.tokenTitle = context.text("session_chart.context_title")
    chart.costTitle = context.text("session_chart.cost_title")
    chart.emptyMessage = context.text("session_chart.empty")
    basis.apply(labels: [
      context.text("session_limits.c0b69a3163"), context.text("session_limits.4d14ea51f5"),
    ])
    bandLegend.items = zip(AILimitStyle.bandNames(context: context), AILimitStyle.bandColors).map {
      .init($0, color: $1)
    }
    if let trend { apply(trend) }
  }
  func apply(_ trend: AILimitTrend) {
    self.trend = trend
    let useChannel = options.cacheChannel
    let rows = trend.requests
    let write = trend.hasWriteLayer
    let switches = Dictionary(trend.switches.map { ($0.index, $0) }, uniquingKeysWith: { $1 })
    var layers: [(String, NSColor, (AILimitRequest) -> Int)] = [
      (context.text("session_limits.1ded6559f6"), AILimitStyle.cacheRead, \.read)
    ]
    if write {
      layers.append((context.text("session_limits.6078a681a3"), AILimitStyle.cacheWrite, \.write))
    }
    layers.append((context.text("session_limits.faa58c14d8"), AILimitStyle.fresh, \.fresh))
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
        index: start,
        text: context.text("session_limits.6e7d5e2572", ["arg0": String(describing: start + 1)]),
        color: AILimitStyle.bandLabels[2])
    }
    content.markers = trend.switches.map { marker in
      switch marker.kind {
      case .model:
        let row = rows[marker.index]
        return .init(
          index: marker.index,
          kind: .label(
            title: row.model,
            annotation: row.effort.isEmpty ? context.text("session_limits.7450cc5b16") : row.effort,
            color: AILimitStyle.effortTone(row.effort).color,
            details: Self.comparisons(marker, compact: true, context: context)))
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
      context.text("session_limits.c051957f0d", ["arg0": String(describing: rows.count)]))
  }

  /// Warning line orange, limit line Coral; the flat-case limit one step darker.
  private func lines(_ trend: AILimitTrend) -> [PaxContextTrendChart.Line] {
    // Lines appear only once the session has come near the limit.
    guard let limit = trend.limit else { return [] }
    var lines: [PaxContextTrendChart.Line] = [
      .init(
        value: Double(limit.warn),
        label: context.text("session_limits.b56413a95e") + AILimitStyle.k(limit.warn),
        color: AILimitStyle.warnLine, labelColor: AILimitStyle.bandLabels[1], dash: [2, 4]),
      .init(
        value: Double(limit.limit),
        label: context.text("session_limits.8c6b2b8843") + AILimitStyle.k(limit.limit),
        color: AILimitStyle.limitLine, labelColor: AILimitStyle.bandLabels[2], dash: [6, 4]),
    ]
    if limit.hasSecondLimit {
      lines.append(
        .init(
          value: Double(limit.flatLimit),
          label: context.text("session_limits.8c6b2b8843") + AILimitStyle.k(limit.flatLimit),
          color: AILimitStyle.flatLine, dash: [6, 4]))
    }
    return lines
  }

  /// Both ratios use official prices. Describe the change, rather than the resulting ratio:
  /// 9.9 times the previous cost is an increase of 890%, not 990%.
  private static func comparisons(
    _ marker: AILimitSwitch, compact: Bool = false, context: PresentationContext = .chinese
  ) -> [PaxContextTrendChart.Marker.Detail] {
    [
      change(
        compact
          ? context.text("session_limits.ee9c090242") : context.text("session_limits.c12ee2cd8c"),
        ratio: marker.turnMultiple, compact: compact, context: context),
      change(
        compact
          ? context.text("session_limits.e0b6229b64") : context.text("session_limits.6ea649f731"),
        ratio: marker.afterReadMultiple, compact: compact, context: context),
    ]
  }

  private static func change(
    _ name: String, ratio: Double?, compact: Bool, context: PresentationContext = .chinese
  ) -> PaxContextTrendChart.Marker.Detail {
    guard let ratio else {
      return .init(name + (compact ? " —" : context.text("session_limits.8c7e743590")))
    }
    guard ratio != 1 else {
      return .init(
        name
          + (compact
            ? context.text("session_limits.91eccfe04c") : context.text("session_limits.494bdd740c"))
      )
    }
    let increase = ratio > 1
    let difference = abs(ratio - 1)
    let percentage = AIUsageFormatting.percent(difference)
    let value =
      percentage == "0.0%" ? "<0.1%" : percentage.replacingOccurrences(of: ".0%", with: "%")
    let direction = increase ? "↑" : "↓"
    return .init(
      name
        + (compact
          ? " "
          : (increase
            ? context.text("session_limits.86f79ba47b") : context.text("session_limits.108946e20c"))),
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
      var parts: [(NSColor, String, Double)] = [
        (AILimitStyle.cacheRead, context.text("session_limits.1ded6559f6"), costs.read)
      ]
      if write {
        parts.append(
          (AILimitStyle.cacheWrite, context.text("session_limits.6078a681a3"), costs.write))
      }
      parts.append((AILimitStyle.fresh, context.text("session_limits.faa58c14d8"), costs.fresh))
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
      tag = .init(AILimitStyle.bandNames(context: context)[point.band], color: color)
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
        label: context.text("session_limits.47f7f8fd52") + AILimitStyle.k(row.context) + " / "
          + AILimitStyle.k(point.limit))
    }
    var footer: [String] = []
    if let other {
      footer.append(
        (channel
          ? context.text("session_limits.5aee18ffc3") : context.text("session_limits.06b07fb548"))
          + AILimitStyle.money(other.total))
    }
    if row.context > 0 {
      footer.append(
        context.text("session_limits.62efe36d50")
          + AIUsageFormatting.percent(Double(row.read) / Double(row.context)))
    }
    var details: [(label: String, value: String)] = []
    if let marker {
      switch marker.kind {
      case .model(let from, let to):
        details.append(
          (
            context.text(
              "session_limits.25e0b56eef",
              ["arg0": String(describing: from), "arg1": String(describing: to)]), ""
          ))
        details += Self.comparisons(marker, context: context).map {
          (label: $0.text, value: $0.value)
        }
        details.append((context.text("session_limits.35c7450883"), ""))
        details.append((context.text("session_limits.ee90c55bb6"), ""))
      case .effort(let from, let to):
        details.append(
          (
            context.text(
              "session_limits.8508a18362",
              ["arg0": String(describing: from), "arg1": String(describing: to)]), ""
          ))
      }
    }
    return .init(
      title: context.text("session_limits.9b25a9939f", ["arg0": String(describing: index + 1)])
        + (row.turn.map {
          context.text("session_limits.d4e2849875", ["arg0": String(describing: $0 + 1)])
        } ?? ""),
      subtitle: AIUsageFormatting.date(row.at, time: true),
      summary: [
        (
          costs == nil
            ? context.text("session_limits.d8d0854706")
            : context.text("session_limits.6953d04809")
              + (channel
                ? context.text("session_limits.4d14ea51f5")
                : context.text("session_limits.c0b69a3163")),
          costs.map { AILimitStyle.money($0.total) } ?? context.text("session_limits.b769180d02")
        )
      ],
      proportion: nil, proportionLabel: "", proportionValue: "", details: details,
      context: footer.joined(separator: " · "),
      badges: [
        .init(row.model, color: PaxFeatureTag.Tone.purple.color),
        .init(
          row.effort.isEmpty ? context.text("session_limits.7450cc5b16") : row.effort,
          color: AILimitStyle.effortTone(row.effort).color),
      ], tag: tag, gauge: gauge, breakdown: breakdown)
  }
}
