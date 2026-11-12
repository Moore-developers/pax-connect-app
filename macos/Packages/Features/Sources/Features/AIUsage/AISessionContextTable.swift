import AppKit
import PaxCore
import PaxUI

extension AISessionAnalysisView {
  func contextFooter() -> NSView {
    let font = PaxTheme.Typography.caption()
    contextAgentsFooter.lineBreakMode = .byTruncatingTail
    contextAgentsFooter.setAccessibilityIdentifier("ai-context-subagents-summary")
    let formula = PaxText.label("均价：费用 ÷ Token × 1M", font: font, secondary: true)
    formula.toolTip = "各档按每 M 输入均价独立取前三：累计输入费用 ÷ 累计输入 Token × 1,000,000"
    formula.setContentCompressionResistancePriority(.required, for: .horizontal)
    let legends = PaxTheme.AISession.costRankColors.enumerated().map { rank, color in
      let text = PaxText.label("TOP\(rank + 1)", font: font, secondary: true)
      text.setContentCompressionResistancePriority(.required, for: .horizontal)
      let item = AIUsageLayout.stack(
        [PaxLegendDot(color: color, shape: .square), text], vertical: false)
      item.spacing = PaxTheme.Spacing.xs
      item.toolTip = ["淡红", "橙", "淡黄"][rank] + "：本档每 M 输入均价第\(rank + 1)名"
      return item
    }
    let row = AIUsageLayout.stack(
      [contextAgentsFooter, NSView(), formula] + legends, vertical: false)
    row.spacing = PaxTheme.Spacing.tight
    return row
  }

  func refreshContext() {
    basis.selectedIndex = options.channel ? 1 : 0
    let source = data?.contextSupported == false ? [] : (data?.current?.contextUsage ?? [])
    let rows = source.filter { $0.session == data?.selected.first }
      .sorted {
        if $0.model.isEmpty != $1.model.isEmpty { return !$0.model.isEmpty }
        let left = contextCost($0.bands) ?? -.infinity
        let right = contextCost($1.bands) ?? -.infinity
        if left != right { return left > right }
        return ($0.model, $0.effort) < ($1.model, $1.effort)
      }
    func identity(_ row: AISessionContextUsage) -> String {
      "\(row.model.utf8.count):\(row.model)\(row.effort)"
    }
    let projected = Dictionary(uniqueKeysWithValues: rows.map { (identity($0), $0) })
    let message = data?.contextSupported == false ? "请更新后台" : "所选日期没有已记录用量"
    guard
      contextRows != projected || renderedContextChannel != options.channel
        || renderedContextMessage != message
    else { return }
    contextRows = projected
    let summary = rows.first { $0.model.isEmpty }
    contextAgentsFooter.stringValue = summary.map(subagentText) ?? ""
    contextAgentsFooter.toolTip = summary.map {
      "所选日期内有活动的不同子代理，含无请求代理及嵌套代理。费用 / Token仅为子代理输入，含缓存、不含输出；表格统计包含主会话和子代理，轮次按原生身份去重。"
        + ($0.subagentsIncomplete ? "\n部分原生记录缺失、未关联或仍在索引，代理数与子代理小计可能不完整。" : "")
    }
    contextUnitCostRanks = (0..<3).map { index in
      let ranked = rows.filter { !$0.model.isEmpty }.compactMap {
        row -> (id: String, unitCost: Double, model: String, effort: String)? in
        guard let unitCost = contextUnitCost([row.bands[index]]), unitCost > 0 else { return nil }
        return (identity(row), unitCost, row.model, row.effort)
      }.sorted {
        if $0.unitCost != $1.unitCost { return $0.unitCost > $1.unitCost }
        return ($0.model, $0.effort) < ($1.model, $1.effort)
      }
      return Dictionary(
        uniqueKeysWithValues: ranked.prefix(3).enumerated().map {
          ($0.element.id, $0.offset)
        })
    }
    renderedContextChannel = options.channel
    renderedContextMessage = message
    contextTable.apply(
      rows: rows.map { row in
        let turnText = row.knownTurnRequests == row.requests ? "\(row.turns)" : "\(row.turns)*"
        return .init(
          id: identity(row),
          values: [row.model.isEmpty ? "总计" : row.model, ""]
            + row.bands.map { band in
              contextPrice([band]) + " / " + AIUsageFormatting.tokens(band.input)
            } + [
              contextPrice(row.bands) + " / " + AIUsageFormatting.tokens(row.input),
              "\(row.requests)", turnText,
            ])
      }, emptyMessage: message)
  }

  func contextCell(id: String, column: Int) -> NSView? {
    guard let row = contextRows[id] else { return nil }
    if column == 0 {
      if row.model.isEmpty {
        let label = PaxText.tableLabel("总计", identity: true)
        label.toolTip = "所选范围内主会话及子代理的输入、费用、请求与去重轮次总计"
        return label
      }
      let label = PaxText.tableLabel(row.model, identity: true)
      label.lineBreakMode = .byTruncatingTail
      let tone = effortTone(row.effort)
      let tag = PaxFeatureTag(row.effort.isEmpty ? "未记录" : row.effort, tone: tone)
      let tagRow = AIUsageLayout.stack([tag, NSView()], vertical: false)
      let cell = AIUsageLayout.stack([label, tagRow])
      cell.spacing = PaxTheme.AISession.contextLineSpacing
      cell.toolTip = row.model + " · 推理强度：" + (row.effort.isEmpty ? "未记录" : row.effort)
      return cell
    }
    if column == 1 {
      let total = Double(row.input)
      let shares = row.bands.map { total > 0 ? Double($0.input) / total * 100 : 0 }
      var rounded = shares.map { Int($0.rounded(.down)) }
      let order = shares.indices.sorted {
        shares[$0] - Double(rounded[$0]) > shares[$1] - Double(rounded[$1])
      }
      if total > 0 {
        for index in order.prefix(max(0, 100 - rounded.reduce(0, +))) { rounded[index] += 1 }
      }
      let view = PaxProportionView(
        segments: row.bands.enumerated().map {
          .init("\(rounded[$0.offset])%", value: Double($0.element.input))
        })
      view.compact = false
      view.percentageLabels = rounded.map { "\($0)%" }
      view.showsLegend = false
      view.legendFont = PaxTheme.Typography.caption()
      view.gradients = [
        PaxTheme.Chart.cacheSeries, PaxTheme.Chart.analysisRequestSeries[1],
        PaxTheme.Chart.inputSeries,
      ].map { [$0, $0] }
      let bands = ["0–150k", "150–250k", ">250k"]
      view.toolTip = row.bands.indices.map { "\(bands[$0])：\(rounded[$0])%" }.joined(
        separator: " · ")
      view.setAccessibilityLabel(view.toolTip)
      let colors = [
        PaxTheme.DashboardColor.greenLabel, PaxTheme.Chart.cacheLabel, PaxTheme.Color.warning,
      ]
      let legend = AIUsageLayout.stack(
        bands.indices.map { index in
          let label = PaxText.label(
            bands[index], font: PaxTheme.Typography.caption(), secondary: true)
          label.setContentCompressionResistancePriority(.required, for: .horizontal)
          label.textColor = colors[index]
          return label
        }, vertical: false)
      legend.distribution = .equalSpacing
      legend.spacing = PaxTheme.Spacing.xs
      let cell = AIUsageLayout.stack([legend, view])
      cell.spacing = PaxTheme.AISession.contextLineSpacing
      cell.toolTip = "按单次请求输入大小分档；百分比为各档累计输入量占本行输入量的比例，非模型窗口占用率。"
      cell.setAccessibilityLabel(view.toolTip)
      return cell
    }
    if (2...4).contains(column), row.bands.indices.contains(column - 2) {
      let colors = [
        PaxTheme.DashboardColor.greenLabel, PaxTheme.Chart.cacheLabel, PaxTheme.Color.warning,
      ]
      return contextAmountCell(
        bands: [row.bands[column - 2]], color: colors[column - 2],
        rank: contextUnitCostRanks[column - 2][id])
    }
    if column == 5 { return contextAmountCell(bands: row.bands) }
    if column == 7, row.knownTurnRequests < row.requests {
      let label = PaxText.tableLabel("\(row.turns)*", numeric: true, alignment: .right)
      label.toolTip = "已识别 \(row.turns) 轮；\(row.requests - row.knownTurnRequests) 次请求缺少轮次记录，不按请求数补算"
      return label
    }
    return nil
  }

  private func subagentAmount(_ row: AISessionContextUsage) -> String {
    contextPrice(row.subagentBands) + " / "
      + AIUsageFormatting.tokens(row.subagentBands.reduce(0) { $0 + $1.input })
  }
  private func subagentText(_ row: AISessionContextUsage) -> String {
    "子代理 \(row.subagents) 个" + (row.subagentsIncomplete ? "*" : "") + "，" + subagentAmount(row)
  }

  private func contextAmountCell(
    bands: [AISessionContextBand], color: NSColor? = nil, rank: Int? = nil
  ) -> NSView {
    let amount = contextPrice(bands)
    let tokens = bands.reduce(0) { $0 + $1.input }
    let price = PaxText.tableLabel(amount, numeric: true)
    let input = PaxText.tableLabel(
      AIUsageFormatting.tokens(tokens), secondary: true, numeric: true)
    for label in [price, input] {
      label.alignment = .right
      if let color { label.textColor = color }
    }
    let amountView: NSView
    if let rank, let unitCost = contextUnitCost(bands) {
      let tag = PaxFeatureTag(
        amount, solidFill: PaxTheme.AISession.costRankColors[rank], font: price.font)
      tag.toolTip =
        "本档\(options.channel ? "渠道" : "官方")每 M 输入均价第\(rank + 1)名"
        + "\n均价：\(AIUsageFormatting.cost(unitCost, precision: 4))/M"
        + "\n按费用 ÷ 输入 Token × 1,000,000 计算；标签金额仍为累计输入费用"
      tag.setAccessibilityLabel(amount + "，" + (tag.toolTip ?? ""))
      let aligned = AIUsageLayout.stack([NSView(), tag], vertical: false)
      aligned.spacing = 0
      amountView = aligned
    } else {
      amountView = price
    }
    let stack = AIUsageLayout.stack([amountView, input])
    stack.spacing = PaxTheme.AISession.contextLineSpacing
    stack.toolTip =
      "\(options.channel ? "渠道" : "官方")输入费用：\(amount)\n输入用量：\(tokens.formatted())"
    return stack
  }

  private func contextPrice(_ bands: [AISessionContextBand]) -> String {
    guard let cost = contextCost(bands) else { return "—" }
    return AIUsageFormatting.cost(cost, precision: 4)
  }

  private func contextCost(_ bands: [AISessionContextBand]) -> Double? {
    guard
      bands.allSatisfy({
        $0.pricedRequests == $0.requests && (!options.channel || $0.unconfirmedChannel == 0)
      })
    else { return nil }
    return bands.reduce(0) { $0 + (options.channel ? $1.channelCost : $1.officialCost) }
  }

  private func contextUnitCost(_ bands: [AISessionContextBand]) -> Double? {
    let input = bands.reduce(0) { $0 + $1.input }
    guard input > 0, let cost = contextCost(bands) else { return nil }
    return cost / Double(input) * 1_000_000
  }
}
