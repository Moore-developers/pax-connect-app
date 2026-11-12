import AppKit
import PaxCore
import PaxUI

/// 排名表：行高按两行内容（费用/缓存条）写定，序号列宽与行内间距互不借用。
private let rankingRowHeight: CGFloat = 72
private let rankingEmptyHeight: CGFloat = 120

extension AIUsageViewController {
  func rankingPanel() -> NSView {
    var groups = store.ranking
    let header = PaxSectionHeader("模型使用 TOP 10")
    let empty = PaxEmptyState(
      symbol: "sparkles", title: "暂无模型用量", caption: "", identifier: "ai-ranking-empty")
    let emptyCard = PaxCard()
    emptyCard.contentView = empty
    empty.heightAnchor.constraint(equalToConstant: rankingEmptyHeight).isActive = true
    emptyCard.heightAnchor.constraint(
      equalToConstant: rankingEmptyHeight + PaxTheme.Spacing.card * 2
    ).isActive = true
    let table = PaxDataTable(
      columns: [
        .init(
          "", width: PaxTheme.Routes.ordinalWidth, growth: 0, alignment: .center,
          inset: 0, sortable: false),
        .init("模型", width: 170, growth: 3, symbol: "sparkles", sortable: false),
        .init(
          "费用 / Token", width: 148, growth: 2, symbol: "chart.bar",
          sortable: false),
        .init(
          "请求数", width: 78, growth: 0.5, alignment: .center, symbol: "arrow.up.arrow.down",
          sortable: false),
        .init(
          "成功率", width: 78, growth: 0.5, alignment: .center, symbol: "checkmark.circle",
          sortable: false),
        .init(
          "输出 TPS", width: 104, growth: 1, alignment: .center, symbol: "speedometer",
          sortable: false),
        .init("耗时", width: 90, growth: 1, alignment: .center, symbol: "timer", sortable: false),
        .init(
          "平均缓存率", width: 104, growth: 0, symbol: "internaldrive", sortable: false,
          toolTip: "按 Token 加权：缓存读取总和 ÷ 输入总和"),
      ],
      rows: groups.map { m, s in
        .init(
          id: m.id,
          values: [
            "", m.name, s.officialCostLabel + "（官方）", String(s.count),
            AIUsageFormatting.percent(s.successRate),
            s.timingText(\.tps, "%.0f tok/s") ?? "—",
            s.timingText(\.elapsed, "%.1fs") ?? "—",
            AIUsageFormatting.percent(s.cache),
          ])
      },
      metrics: .init(rowHeight: rankingRowHeight, rounded: true),
      selection: .browse)
    let makeCell: (String, Int) -> NSView? = { [weak self] id, column in
      guard let self, let pair = groups.first(where: { $0.0.id == id }) else { return nil }
      let s = pair.1
      switch column {
      case 0:
        let rank = PaxText.tableLabel(
          String((groups.firstIndex(where: { $0.0.id == id }) ?? 0) + 1),
          numeric: true, alignment: .center)
        rank.textColor = PaxTheme.Table.muted
        return rank
      case 1:
        let source = PaxText.tableLabel(
          s.topClient.map { "主要来自 " + $0.rawValue } ?? "—", secondary: true)
        source.lineBreakMode = .byTruncatingTail
        let identity = AIUsageLayout.stack([
          PaxText.tableLabel(pair.0.name, identity: true), source,
        ])
        identity.spacing = PaxTheme.Spacing.xs
        return identity
      case 2:
        let share: Double? =
          self.store.summary.originalCost > 0
          ? s.originalCost / self.store.summary.originalCost : nil
        // Without any cost there is no share; the slot is omitted instead of showing a
        // bare "—" that reads as an unlabeled column.
        let line = PaxText.tableLabel(s.officialCostLabel + "（官方）", numeric: true)
        let bar = PaxShareBar(
          segments: [.init("费用占比", value: s.originalCost)], colors: [PaxTheme.Chart.cacheSeries],
          gradient: true, total: self.store.summary.originalCost)
        if share == nil {
          bar.isHidden = true
          bar.setAccessibilityElement(false)
        }
        // 列头已写明“费用 / Token”，单元格只留数值；份额与 Token 同行，让整列收窄。
        var footer: [NSView] = [
          PaxText.tableLabel(
            AIUsageFormatting.tokens(s.tokens), secondary: true, numeric: true),
          NSView(),
        ]
        if share != nil {
          footer.append(
            PaxText.tableLabel(
              AIUsageFormatting.percent(share), secondary: true, numeric: true))
        }
        let stack = AIUsageLayout.stack([line, bar, AIUsageLayout.stack(footer, vertical: false)])
        stack.spacing = PaxTheme.Spacing.xs
        return stack
      case 3:
        let count = PaxText.tableLabel(String(s.count), numeric: true)
        count.alignment = .center
        return count
      case 4:
        return PaxText.tableLabel(
          AIUsageFormatting.percent(s.successRate), numeric: true, alignment: .center)
      case 5:
        let value = PaxText.tableLabel(
          s.timingText(\.tps, "%.0f tok/s") ?? "—", numeric: true)
        if let tps = s.mean(\.tps) {
          value.textColor =
            PaxTheme.RTT.endpoints(band: tps >= 50 ? 0 : tps >= 25 ? 1 : 2, role: .text).0
        }
        let average = AIUsageLayout.stack(
          [PaxText.tableLabel("平均", secondary: true), value], vertical: false)
        average.spacing = PaxTheme.Spacing.xs
        return AIUsageLayout.wrapCentered(average)
      case 6:
        let average = AIUsageLayout.stack(
          [
            PaxText.tableLabel("平均", secondary: true),
            PaxText.tableLabel(s.timingText(\.elapsed, "%.1fs") ?? "—", numeric: true),
          ], vertical: false)
        average.spacing = PaxTheme.Spacing.xs
        average.toolTip = "发送到结束的平均耗时"
        return AIUsageLayout.wrapCentered(average)
      case 7:
        let text = AIUsageFormatting.percent(s.cache)
        let gauge = PaxMiniGauge(
          fraction: s.cache, text: text, color: PaxTheme.Chart.cacheSeries)
        gauge.toolTip = "缓存读取占输入 Token 的比例：" + text
        let cell = NSStackView(views: [gauge])
        cell.alignment = .centerY
        return cell
      default: return nil
      }
    }
    func signature(_ pair: (AIModel, AIUsageSummary), total: Double) -> String {
      let s = pair.1
      return [
        pair.0.name, s.officialCostLabel + "（官方）", String(s.count), String(s.tokens),
        AIUsageFormatting.percent(s.successRate), s.timingText(\.tps, "%.0f tok/s") ?? "—",
        s.timingText(\.elapsed, "%.1fs") ?? "—", AIUsageFormatting.percent(s.cache),
        s.topClient?.rawValue ?? "", String(total),
      ].joined(separator: "|")
    }
    var keys = Dictionary(
      uniqueKeysWithValues: groups.map {
        ($0.0.id, signature($0, total: store.summary.originalCost))
      })
    table.cellProvider = { id, column in
      guard let view = makeCell(id, column) else { return nil }
      return PaxDataTable.reusableContent(view, reuseKey: (keys[id] ?? "") + "-\(column)")
    }
    table.cellUpdater = { existing, id, column in
      PaxDataTable.updateContent(existing, reusing: (keys[id] ?? "") + "-\(column)") {
        makeCell(id, column) ?? NSView()
      }
    }
    contentRefreshers.append { [weak self, weak table, weak emptyCard, weak empty] in
      guard let self else { return }
      groups = self.store.ranking
      let next = Dictionary(
        uniqueKeysWithValues: groups.map {
          ($0.0.id, signature($0, total: self.store.summary.originalCost))
        })
      let changed = Set(next.keys.filter { keys[$0] != next[$0] })
      keys = next
      table?.apply(
        rows: groups.map { .init(id: $0.0.id, values: []) }, emptyMessage: "", reloading: changed)
      table?.isHidden = groups.isEmpty
      emptyCard?.isHidden = !groups.isEmpty
      empty?.update(caption: self.store.usageEmptyReason ?? "所选时段没有模型用量")
    }
    table.isHidden = groups.isEmpty
    emptyCard.isHidden = !groups.isEmpty
    table.setTableIdentifier("ai-model-ranking")
    table.onRowClick = { [weak self] id in self?.show(.model(id)) }
    table.onActivate = table.onRowClick
    return AIUsageLayout.stack([header, table, emptyCard])
  }
}
