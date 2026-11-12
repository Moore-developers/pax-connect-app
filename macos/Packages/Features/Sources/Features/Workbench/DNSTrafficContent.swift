import AppKit
import PaxCore
import PaxUI

/// DNS is a query observation. Groups precede pagination; raw rows are fetched
/// only after an explicit group selection, using the same frozen read token.
@MainActor
func dnsTrafficContent(
  _ snapshot: WorkbenchTrafficDetail, overview: Bool,
  tooltip: PaxChartTooltipCoordinator, onGroup: @escaping (String?) -> Void
) -> NSView {
  let stack = NSStackView()
  stack.orientation = .vertical
  stack.alignment = .leading
  stack.spacing = PaxTheme.Spacing.medium
  guard let detail = snapshot.aggregate else { return stack }
  if overview {
    stack.addArrangedSubview(
      PaxText.label(
        "域名解析 · \(snapshot.connectionCount) 次查询",
        font: PaxTheme.Typography.label(size: PaxTheme.TrafficDetail.latencyValueFont)))
    stack.addArrangedSubview(
      PaxRouteSectionHeading("解析耗时", symbol: "timer", detail: snapshot.latencyRangeText))
    stack.addArrangedSubview(snapshot.latencyView(tooltip: tooltip))
    stack.addArrangedSubview(
      PaxText.label(
        snapshot.sampleSummary, font: PaxTheme.Typography.caption(), secondary: true, wrapping: true
      ))
    stack.addArrangedSubview(
      PaxText.label(
        "全部解析记录，不受连接去向筛选；无独立字节证据，不计入连接流量。", font: PaxTheme.Typography.caption(), secondary: true,
        wrapping: true))
  } else if detail.dnsGroupID.isEmpty {
    let rows = detail.dnsGroups
    stack.addArrangedSubview(
      PaxText.label(
        "按完整域名、类型和结果归组 · 已加载 \(rows.count) / \(detail.dnsGroupTotal) 组",
        font: PaxTheme.Typography.caption(), secondary: true))
    let table = dnsTable(
      ["查询域名／类型", "结果／最近答案", "查询次数", "最近查询", "解析耗时"], count: rows.count,
      identifier: "traffic-dns-groups")
    table.cellProvider = { id, column in
      guard let row = rows.first(where: { $0.id == id }) else { return nil }
      if column == 0 {
        let name = PaxTrafficCells.objectButton(row.domain.isEmpty ? "域名未记录" : row.domain) {
          onGroup(id)
        }
        let content = NSStackView(views: [
          name,
          PaxText.label(
            row.queryType.isEmpty ? "类型未记录" : row.queryType, font: PaxTheme.Typography.caption(),
            secondary: true),
        ])
        content.orientation = .vertical
        content.alignment = .leading
        content.spacing = PaxTheme.Spacing.xs
        return content
      }
      let value: String
      switch column {
      case 1:
        value =
          row.result + "\n" + (row.answers.isEmpty ? "未记录答案" : row.answers.joined(separator: ", "))
      case 2: value = "\(row.queries) 次"
      case 3: value = TrafficFormatting.dateMinute(row.lastAt)
      default:
        value = TrafficFormatting.milliseconds(row.latencyKnown ? Double(row.latencyMS) : nil)
      }
      return dnsLabel(value)
    }
    table.onActivate = { onGroup($0) }
    table.apply(rows: rows.map { .init(id: $0.id, values: []) })
    stack.addArrangedSubview(table)
  } else {
    if let group = detail.dnsGroups.first {
      stack.addArrangedSubview(
        PaxText.label(
          group.domain + " · " + group.queryType + " · " + group.result + " · \(group.queries) 次查询",
          font: PaxTheme.Typography.caption(), wrapping: true))
    }
    stack.addArrangedSubview(PaxTrafficCells.objectButton("返回全部查询分组") { onGroup(nil) })
    let rows = detail.items
    let table = dnsTable(
      ["时间", "查询域名／类型", "结果／答案", "来源", "解析耗时"], count: rows.count, identifier: "traffic-dns-queries"
    )
    table.cellProvider = { id, column in
      guard let f = rows.first(where: { $0.key == id }) else { return nil }
      let value: String
      switch column {
      case 0: value = TrafficFormatting.dateMinute(f.startedAt)
      case 1: value = f.domain + "\n" + (f.dns?.queryType ?? "类型未记录")
      case 2:
        value =
          (f.dns?.result.isEmpty == false
            ? f.dns?.result ?? "" : FlowPresentation.failure(f.failure)) + "\n"
          + (f.dns?.answers.joined(separator: ", ") ?? "答案未记录")
      case 3:
        value = f.process.isEmpty ? (f.ruleSource.isEmpty ? "来源未记录" : f.ruleSource) : f.process
      default: value = TrafficFormatting.milliseconds(TrafficFormatting.responseLatency(f))
      }
      return dnsLabel(value)
    }
    table.apply(rows: rows.map { .init(id: $0.key, values: []) })
    stack.addArrangedSubview(table)
  }
  for view in stack.arrangedSubviews {
    view.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
  }
  return stack
}

@MainActor private func dnsLabel(_ value: String) -> NSTextField {
  let label = PaxText.label(value, font: PaxTheme.Typography.caption())
  label.maximumNumberOfLines = 2
  label.lineBreakMode = .byTruncatingTail
  label.toolTip = value
  return label
}
@MainActor private func dnsTable(_ titles: [String], count: Int, identifier: String) -> PaxDataTable
{
  let table = PaxDataTable(
    columns: titles.enumerated().map { i, title in
      .init(title, width: 0, growth: PaxTheme.TrafficDetail.dnsColumns[i], sortable: false)
    }, metrics: .init(rowHeight: PaxTheme.Logs.eventRow), selection: .single)
  table.setTableIdentifier(identifier)
  table.heightAnchor.constraint(
    equalToConstant: PaxTheme.Table.compactHeader + CGFloat(max(1, count)) * PaxTheme.Logs.eventRow
  ).isActive = true
  return table
}
