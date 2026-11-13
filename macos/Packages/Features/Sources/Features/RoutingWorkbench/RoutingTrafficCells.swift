import AppKit
import PaxCore
import PaxUI

@MainActor
enum RoutingTrafficCellKey: Hashable {
  case identity(String, Int, String, String, String)
  case route(String, String, String, [TrafficExit])
  case rule(Date?, String, String, String, String, String)
  case latency(
    UInt64, Date?, [TrafficLatencySample], Bool, String, Date, Date, Date?, [TrafficResponsePoint],
    String, String)
  case count(UInt64, UInt64, String)
  case transfer(UInt64, UInt64, Bool)
}
@MainActor
func routingTrafficCellKey(_ id: String, column: Int, store: RoutingWorkbenchStore)
  -> RoutingTrafficCellKey?
{
  guard let fact = store.liveFact(id: id) else { return nil }
  switch column {
  case 0: return .identity(id, store.dimension.rawValue, fact.objectID, fact.name, fact.sourceLabel)
  case 1:
    return .route(fact.direction, TrafficFormatting.actualConnections(fact), fact.kind, fact.exits)
  case 2:
    let match = fact.latestMatch
    return .rule(
      match?.at, match?.rule ?? "", match?.source ?? "", match?.applicationID ?? "",
      match?.application ?? "", match?.identity ?? "")
  case 3:
    return .latency(
      fact.latestLatencyMS, fact.latestLatencyAt, fact.latencySamples, fact.latencySampled,
      fact.kind, store.livePage.from, store.livePage.asOf,
      store.livePage.history.aggregateCoverageStart, fact.recentResponses,
      fact.recentResponsesState,
      "\(store.period)-\(store.direction)-\(store.search)")
  case 4:
    return .count(
      fact.connections, fact.failedCount,
      fact.kind + (TrafficFormatting.isIPv6LimitedPath(fact) ? ":ipv6" : ""))
  case 5: return .transfer(fact.upload, fact.download, fact.bytesKnown)
  default: return nil
  }
}
@MainActor
func makeRoutingTrafficCell(
  _ id: String, column: Int, store: RoutingWorkbenchStore, icons: WorkbenchObjectIcons,
  tooltip: PaxChartTooltipCoordinator, onActivate: @escaping () -> Void
) -> NSView? {
  if id == "load-more", column == 0 {
    return PaxTrafficCells.objectButton("加载更多") {
      store.liveFrozen = true
      store.scheduleLive(more: true)
    }
  }
  guard
    let row = store.telemetry == nil
      ? store.visibleRows.first(where: { $0.id == id }) : store.liveRow(id: id)
  else { return nil }
  let fact = store.liveFact(id: id)
  if column == 0, store.dimension == .system {
    let name = PaxTrafficCells.objectButton(row.name, onActivate: onActivate)
    return icons.identityView(
      kind: .application, id: fact?.objectID ?? row.id, name: row.name,
      content: routingSourceContent(name, title: row.name, subtitle: fact?.sourceLabel ?? ""),
      size: PaxTheme.Logs.directoryCompactIconSize)
  }
  if column == 5, fact?.bytesKnown == false {
    return PaxTrafficCells.transfer(
      uploadText: "—", downloadText: "—", upload: 0, download: 0,
      note: "DNS查询没有独立字节计数证据")
  }
  if column == 4, let fact, fact.failedCount > 0 {
    let count = PaxText.label(
      String(fact.connections),
      font: PaxTheme.Typography.metric(size: PaxTheme.Dashboard.bodyFont))
    count.alignment = .center
    let failures = PaxText.label(
      "\(fact.failedCount) 次" + (fact.direction == "blocked" ? "阻断" : "失败"),
      font: PaxTheme.Typography.caption(), secondary: true)
    failures.alignment = .center
    failures.lineBreakMode = .byTruncatingTail
    let stack = NSStackView(views: [count, failures])
    stack.orientation = .vertical
    stack.alignment = .centerX
    stack.spacing = PaxTheme.Spacing.xs
    let result = fact.direction == "blocked" ? "阻断" : "失败"
    let reason = TrafficFormatting.isIPv6LimitedPath(fact) ? "（IPv6 未启用；该次 Pax 连接仅使用 IPv4）" : ""
    let complete = "范围内新建连接 \(fact.connections) 条；\(result) \(fact.failedCount) 次" + reason
    count.toolTip = complete
    failures.toolTip = complete
    stack.toolTip = complete
    stack.setAccessibilityLabel(stack.toolTip)
    return stack
  }
  if column == 4, store.dimension == .system {
    return PaxText.label(
      "\(row.count) 条", font: PaxTheme.Typography.metric(size: PaxTheme.Dashboard.bodyFont))
  }
  if column == 2 {
    let title = fact.map { TrafficRuleFormatting.latestTitle($0.latestMatch) } ?? row.basisTitle
    let label = PaxText.label(title, font: PaxTheme.Typography.caption())
    let paragraph = NSMutableParagraphStyle()
    paragraph.alignment = .center
    paragraph.lineSpacing = PaxTheme.Spacing.xs
    label.attributedStringValue = NSAttributedString(
      string: title, attributes: [.paragraphStyle: paragraph])
    label.alignment = .center
    label.maximumNumberOfLines = 1
    label.lineBreakMode = .byTruncatingTail
    label.toolTip = fact.map { TrafficRuleFormatting.latestEvidence($0.latestMatch) } ?? title
    return label
  }
  let baseDisplay = PaxTrafficAnalysisRow(
    id: row.id, name: row.name, symbol: row.symbol, direction: row.direction.title,
    route: column == 1
      ? (fact.map(TrafficFormatting.actualConnections)
        ?? TrafficFormatting.actualConnection(direction: row.direction.title, route: row.route))
      : "",
    count: row.count,
    upload: column == 5 ? TrafficFormatting.bytes(row.upload) : "",
    download: column == 5 ? TrafficFormatting.bytes(row.download) : "",
    uploadValue: Double(row.upload),
    downloadValue: Double(row.download), requests: row.establishment,
    latencyText: column == 3
      ? TrafficFormatting.milliseconds(
        store.telemetry == nil
          ? row.establishment.last(where: { $0.value != nil })?.value : row.latestLatency) : "")
  var display = baseDisplay
  // Only the latency column consumes the time-series projection. Building it for
  // every cell multiplies first-visible row work by the number of columns.
  if column == 3, store.telemetry != nil, let fact {
    display.latencyText = TrafficFormatting.milliseconds(
      fact.recentResponses.last.map { Double($0.milliseconds) })
    display.latencyTitle = fact.kind == "dns" ? "解析耗时" : "响应延迟"
    display.latencyContext = workbenchResponseContext(
      state: fact.recentResponsesState,
      count: min(20, fact.recentResponses.count), dns: fact.kind == "dns")
    display.latencyPinKey = "traffic-list-\(id)-\(store.period)-\(store.direction)-\(store.search)"
    display.latencySeries = workbenchLatencySeries(
      fact, from: store.livePage.from, asOf: store.livePage.asOf,
      coverageStart: store.livePage.history.aggregateCoverageStart, compact: true)
  }
  if column == 3, store.telemetry == nil {
    let lower = store.now.addingTimeInterval(-store.period.seconds)
    let points = row.connections.filter {
      $0.started > lower && $0.started <= store.now && $0.establishmentMS != nil
    }
    .sorted { $0.started == $1.started ? $0.id < $1.id : $0.started < $1.started }.suffix(20).map {
      PaxTimeSeriesPoint(at: $0.started, value: $0.establishmentMS, sampleID: $0.id, note: "连接开始时间")
    }
    display.latencySeries = .init(
      domain: .init(start: lower, end: store.now), points: points, axis: .samples)
    display.latencyText = TrafficFormatting.milliseconds(points.last?.value)
  }
  let kind: WorkbenchObjectIcons.Kind =
    store.dimension == .application ? .application : store.dimension == .domain ? .domain : .ip
  let identity = store.livePage.items.first { $0.id == id }?.objectID ?? row.name
  let cell = PaxTrafficCells.make(
    display, column: column > 2 ? column - 1 : column, tooltip: tooltip,
    onActivate: onActivate,
    objectContent: { content in
      icons.identityView(
        kind: kind, id: identity, name: row.name,
        content: content,
        size: PaxTheme.Dashboard.nodeTitle, gap: PaxTheme.Dashboard.titleGap,
        tint: PaxTheme.Color.textSecondary)
    })
  if column == 1, let fact {
    let exits =
      fact.exits.isEmpty
      ? ["记录的线路：" + fact.routeName + "\n线路标识：" + fact.routeID]
      : fact.exits.map { "记录的线路：" + $0.routeName + "\n线路标识：" + $0.routeID }
    let evidence = exits.joined(separator: "\n")
    cell.toolTip = evidence
    for child in cell.subviews { child.toolTip = evidence }
  }
  return cell
}

/// 副标题为空或与名称相同时只显示一行，由身份视图垂直居中；否则保持双行。
@MainActor func routingSourceContent(_ name: NSView, title: String, subtitle: String) -> NSView {
  let trimmed = subtitle.trimmingCharacters(in: .whitespacesAndNewlines)
  return trimmed.isEmpty || trimmed == title.trimmingCharacters(in: .whitespacesAndNewlines)
    ? name : RoutingSourceIdentity(name: name, subtitle: subtitle)
}

@MainActor private final class RoutingSourceIdentity: NSView {
  private let name: NSView
  private let caption: NSTextField
  init(name: NSView, subtitle: String) {
    self.name = name
    caption = PaxText.label(subtitle, font: PaxTheme.Typography.caption(), secondary: true)
    super.init(frame: .zero)
    name.translatesAutoresizingMaskIntoConstraints = true
    caption.translatesAutoresizingMaskIntoConstraints = true
    addSubview(name)
    addSubview(caption)
  }
  required init?(coder: NSCoder) { fatalError("代码构建") }
  override var isFlipped: Bool { true }
  override var intrinsicContentSize: NSSize {
    NSSize(
      width: max(name.intrinsicContentSize.width, caption.intrinsicContentSize.width),
      height: PaxTheme.RTT.textLine + PaxTheme.RTT.badgeTextHeight + PaxTheme.Spacing.xs)
  }
  override func layout() {
    super.layout()
    name.frame = NSRect(x: 0, y: 0, width: bounds.width, height: PaxTheme.RTT.textLine)
    caption.frame = NSRect(
      x: 0, y: name.frame.maxY + PaxTheme.Spacing.xs, width: bounds.width,
      height: PaxTheme.RTT.badgeTextHeight)
  }
}
