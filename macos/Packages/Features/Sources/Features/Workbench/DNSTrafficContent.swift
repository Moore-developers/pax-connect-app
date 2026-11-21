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
  let context = snapshot.context
  let stack = NSStackView()
  stack.orientation = .vertical
  stack.alignment = .leading
  stack.spacing = PaxTheme.Spacing.medium
  guard let detail = snapshot.aggregate else { return stack }
  if overview {
    stack.addArrangedSubview(
      PaxText.label(
        context.text(
          "traffic_details.257eada5ce", ["arg0": String(describing: snapshot.connectionCount)]),
        font: PaxTheme.Typography.label(size: PaxTheme.TrafficDetail.latencyValueFont)))
    stack.addArrangedSubview(
      PaxRouteSectionHeading(
        context.text("traffic_details.d824870c1d"), symbol: "timer",
        detail: snapshot.latencyRangeText))
    stack.addArrangedSubview(snapshot.latencyView(tooltip: tooltip))
    stack.addArrangedSubview(
      PaxText.label(
        snapshot.sampleSummary, font: PaxTheme.Typography.caption(), secondary: true, wrapping: true
      ))
    stack.addArrangedSubview(
      PaxText.label(
        context.text("traffic_details.3801e6ff63"), font: PaxTheme.Typography.caption(),
        secondary: true,
        wrapping: true))
  } else if detail.dnsGroupID.isEmpty {
    let rows = detail.dnsGroups
    stack.addArrangedSubview(
      PaxText.label(
        context.text(
          "traffic_details.6e5ea2a2c3",
          [
            "arg0": String(describing: rows.count),
            "arg1": String(describing: detail.dnsGroupTotal),
          ]),
        font: PaxTheme.Typography.caption(), secondary: true))
    let table = dnsTable(
      [
        context.text("traffic_details.184d0f8dce"), context.text("traffic_details.2bee44fe9d"),
        context.text("traffic_details.f791185da5"), context.text("traffic_details.69b14664c4"),
        context.text("traffic_details.d824870c1d"),
      ], count: rows.count,
      identifier: "traffic-dns-groups")
    table.cellProvider = { id, column in
      guard let row = rows.first(where: { $0.id == id }) else { return nil }
      if column == 0 {
        let name = PaxTrafficCells.objectButton(
          row.domain.isEmpty ? context.text("traffic_details.454f254d52") : row.domain
        ) {
          onGroup(id)
        }
        let content = NSStackView(views: [
          name,
          PaxText.label(
            row.queryType.isEmpty ? context.text("traffic_details.4bf23113b5") : row.queryType,
            font: PaxTheme.Typography.caption(),
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
          row.result + "\n"
          + (row.answers.isEmpty
            ? context.text("traffic_details.d172baa8ac") : row.answers.joined(separator: ", "))
      case 2:
        value = context.text(
          "traffic_details.8cc9b62351", ["arg0": String(describing: row.queries)])
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
          group.domain + " · " + group.queryType + " · " + group.result
            + context.text(
              "traffic_details.a04a1c8a3b", ["arg0": String(describing: group.queries)]),
          font: PaxTheme.Typography.caption(), wrapping: true))
    }
    stack.addArrangedSubview(
      PaxTrafficCells.objectButton(context.text("traffic_details.770fe7655d")) { onGroup(nil) })
    let rows = detail.items
    let table = dnsTable(
      [
        context.text("traffic_details.8b6ff49851"), context.text("traffic_details.184d0f8dce"),
        context.text("traffic_details.1fc0555696"), context.text("traffic_details.a488e93d69"),
        context.text("traffic_details.d824870c1d"),
      ], count: rows.count, identifier: "traffic-dns-queries"
    )
    table.cellProvider = { id, column in
      guard let f = rows.first(where: { $0.key == id }) else { return nil }
      let value: String
      switch column {
      case 0: value = TrafficFormatting.dateMinute(f.startedAt)
      case 1:
        value = f.domain + "\n" + (f.dns?.queryType ?? context.text("traffic_details.4bf23113b5"))
      case 2:
        value =
          (f.dns?.result.isEmpty == false
            ? f.dns?.result ?? "" : FlowPresentation.failure(f.failure)) + "\n"
          + (f.dns?.answers.joined(separator: ", ") ?? context.text("traffic_details.e94b9d24d9"))
      case 3:
        value =
          f.process.isEmpty
          ? (f.ruleSource.isEmpty ? context.text("traffic_details.3b310b2975") : f.ruleSource)
          : f.process
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
