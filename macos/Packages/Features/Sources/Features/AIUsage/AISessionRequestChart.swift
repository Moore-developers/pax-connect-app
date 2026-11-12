import AppKit
import PaxCore
import PaxUI

extension AISessionAnalysisView {
  func refreshRequests() {
    let rows = (data?.detail?.requests ?? []).filter { $0.key == data?.selected.first }
    let turnRows = (data?.detail?.rows ?? []).filter { $0.key == data?.selected.first }
    guard
      renderedRequests != rows || renderedTurnRows != turnRows
        || renderedCacheChannel != options.cacheChannel
    else { return }
    renderedRequests = rows
    renderedTurnRows = turnRows
    renderedCacheChannel = options.cacheChannel
    let rounds = data?.rounds ?? []
    let basis = options.cacheChannel ? "渠道费用" : "官方费用"
    let otherBasis = options.cacheChannel ? "官方费用" : "渠道费用"
    var values: [[Double?]] = []
    var tips: [PaxAnalysisChart.Hover] = []
    var annotations: [PaxAnalysisChart.Annotation] = []
    var previousCachePriceMicro: Double?
    var previousModel: String?
    var modelChanges = 0
    var rises = 0
    var falls = 0
    for (index, row) in rows.enumerated() {
      let totals = row.totals
      // Chart costs include only ordinary input and cache read, excluding cache write/output.
      let costs = options.cacheChannel ? totals.billedCosts : totals.costs
      let otherCosts = options.cacheChannel ? totals.costs : totals.billedCosts
      let cachePrice = costs[1]
      let inputPrice = costs[0]
      let price = cachePrice + inputPrice
      let otherPrice = otherCosts[0] + otherCosts[1]
      let priced = row.pricedRequests == totals.count
      values.append([Double(totals.read), priced ? cachePrice : nil, priced ? inputPrice : nil])
      // Only a recorded change between two known models is evidence of a switch.
      if !row.model.isEmpty, let previousModel, previousModel != row.model {
        modelChanges += 1
        annotations.append(
          .init(
            index: index, series: nil, value: 0, label: row.model,
            detail: row.effort.isEmpty ? "未记录" : row.effort,
            color: PaxTheme.Routes.muted))
      }
      previousModel = row.model.isEmpty ? nil : row.model
      let cachePriceMicro = (cachePrice * 1_000_000).rounded()
      let share = totals.input > 0 ? Double(totals.read) / Double(totals.input) : nil
      let cacheDominant = priced && (share ?? 0) >= 0.5 && cachePriceMicro > 0
      if cacheDominant, let previous = previousCachePriceMicro {
        // Integer micro-USD includes exactly +60% and -60% without threshold drift.
        let rising = cachePriceMicro * 5 >= previous * 8
        let falling = cachePriceMicro * 5 <= previous * 2
        if rising || falling {
          if rising { rises += 1 } else { falls += 1 }
          annotations.append(
            .init(
              index: index, series: 1, value: cachePrice,
              label: (rising ? "↑ " : "↓ ") + AIUsageFormatting.cost(cachePrice, precision: 6),
              detail: AIUsageFormatting.tokens(totals.read) + " token",
              color: rising ? PaxTheme.DashboardColor.red : PaxTheme.DashboardColor.green))
        }
      }
      // Skip known low-cache requests without losing the last valid cost, even across models.
      if cacheDominant {
        previousCachePriceMicro = cachePriceMicro
      } else if !priced {
        previousCachePriceMicro = nil
      }
      let round = rounds.firstIndex { $0.id == row.turn }.map { "第 \($0+1) 轮" } ?? "轮次未知"
      tips.append(
        .init(
          title: "第 \(index+1) 次请求 · \(round)",
          subtitle: AIUsageFormatting.date(row.at, time: true),
          summary: [
            ("缓存读取 Token", AIUsageFormatting.tokens(totals.read)),
            ("费用合计 · \(basis)", priced ? AIUsageFormatting.cost(price, precision: 6) : "未定价"),
          ],
          proportion: share, proportionLabel: "缓存命中率",
          proportionValue: AIUsageFormatting.percent(share),
          details: [
            ("总输入 Token", AIUsageFormatting.tokens(totals.input)),
            (
              "费用合计 · \(otherBasis)",
              priced ? AIUsageFormatting.cost(otherPrice, precision: 6) : "未定价"
            ),
          ],
          context: "费用合计 = 普通输入 + 缓存读取；不含缓存写入和输出",
          badges: [
            .init(row.model.isEmpty ? "模型未记录" : row.model, color: PaxFeatureTag.Tone.purple.color),
            .init(row.effort.isEmpty ? "未记录" : row.effort, color: effortTone(row.effort).color),
          ],
          breakdown: priced
            ? [
              (PaxTheme.Chart.analysisRequestSeries[1], "缓存读取费用", cachePrice),
              (PaxTheme.Chart.analysisRequestSeries[2], "其余输入费用", inputPrice),
            ].map { color, name, amount in
              .init(
                color: color, name: name, amount: AIUsageFormatting.cost(amount, precision: 6),
                share: price > 0 ? AIUsageFormatting.percent(amount / price) : "—", weight: amount)
            } : []
        ))
    }
    cacheBasis.selectedIndex = options.cacheChannel ? 1 : 0
    cacheChart.names = ["缓存读 token", "缓存读费用", "普通输入"]
    cacheChart.colors = PaxTheme.Chart.analysisRequestSeries
    cacheChart.labels = rows.indices.map { "第\($0+1)次" }
    cacheChart.values = values
    cacheChart.hoverDetails = tips
    cacheChart.annotations = annotations
    cacheChart.unit = "Token"
    cacheChart.format = { AIUsageFormatting.tokens(Int($0)) }
    cacheChart.secondaryFormat = { AIUsageFormatting.cost($0, precision: 6) }
    cacheChart.setAccessibilityLabel(
      "缓存读取趋势；\(rows.count)次模型请求；缓存读token蓝色2pt虚线与\(basis)堆叠面积叠加；逐请求展示费用，包含缓存读取和普通输入，不含缓存写入和输出；左轴单次Token，右轴单次费用合计"
    )
    cacheChart.setAccessibilityValue(
      "缓存读取费用较最近有效请求上涨至少60%：\(rises)处；下跌至少60%：\(falls)处；模型切换：\(modelChanges)处"
    )
    cacheChart.needsDisplay = true
  }

}
