import AppKit
import PaxCore
import PaxUI

@MainActor
enum RoutingTrafficCellKey: Hashable {
  struct Exit: Hashable {
    let direction: String
    let name: String
    let id: String
  }
  struct Latency: Hashable {
    let samples: ArraySlice<TrafficResponsePoint>
    let state: String
    let dns: Bool
    let pinKey: String

    var latest: String {
      TrafficFormatting.milliseconds(samples.last.map { Double($0.milliseconds) })
    }
    var title: String { dns ? "解析耗时" : "响应延迟" }
    var context: String { workbenchResponseContext(state: state, count: samples.count, dns: dns) }
    func series(in page: TrafficPage) -> PaxTimeSeries {
      workbenchResponseSeries(
        samples, from: page.from, asOf: page.asOf, limit: samples.count, dns: dns)
    }
  }
  struct Count: Hashable {
    let connections: UInt64
    let failures: UInt64
    let blocked: Bool
    let ipv6Limited: Bool
    let system: Bool

    var value: String { system && failures == 0 ? "\(connections) 条" : String(connections) }
    var result: String { blocked ? "阻断" : "失败" }
    var failureText: String { "\(failures) 次" + result }
    var evidence: String? {
      guard failures > 0 else { return nil }
      let reason = ipv6Limited ? "（IPv6 未启用；该次 Pax 连接仅使用 IPv4）" : ""
      return "范围内新建连接 \(connections) 条；\(result) \(failures) 次" + reason
    }
  }
  struct Transfer: Hashable {
    let upload: UInt64
    let download: UInt64
    let known: Bool

    var uploadText: String { known ? TrafficFormatting.bytes(upload) : "—" }
    var downloadText: String { known ? TrafficFormatting.bytes(download) : "—" }
    var note: String? { known ? nil : "DNS查询没有独立字节计数证据" }
  }

  case identity(String, Int, String, String, String)
  case route([Exit])
  case rule(Date?, String, String, String, String)
  case latency(Latency)
  case count(Count)
  case transfer(Transfer)
}

@MainActor
func routingTrafficCellKey(
  _ fact: PaxCore.TrafficRow, column: Int, dimension: RoutingWorkbenchStore.Dimension,
  pinKey: @autoclosure () -> String
) -> RoutingTrafficCellKey? {
  switch column {
  case 0:
    return .identity(
      fact.id, dimension.rawValue, fact.objectID, fact.name,
      dimension == .system ? fact.sourceLabel : "")
  case 1:
    let exits =
      fact.exits.isEmpty
      ? [.init(direction: fact.direction, name: fact.routeName, id: fact.routeID)]
      : fact.exits.map {
        RoutingTrafficCellKey.Exit(direction: $0.direction, name: $0.routeName, id: $0.routeID)
      }
    return .route(exits)
  case 2:
    let match = fact.latestMatch
    return .rule(
      match?.at, match?.rule ?? "", match?.source ?? "", match?.applicationID ?? "",
      match?.application ?? "")
  case 3:
    // Compact response charts space points by sample order. Cutoff-only updates
    // do not change the curve, its readout or its interaction.
    return .latency(
      .init(
        samples: fact.recentResponses.suffix(20), state: fact.recentResponsesState,
        dns: fact.kind == "dns", pinKey: pinKey()))
  case 4:
    return .count(
      .init(
        connections: fact.connections, failures: fact.failedCount,
        blocked: fact.failedCount > 0 && fact.direction == "blocked",
        ipv6Limited: fact.failedCount > 0 && TrafficFormatting.isIPv6LimitedPath(fact),
        system: dimension == .system))
  case 5:
    return .transfer(
      .init(
        upload: fact.bytesKnown ? fact.upload : 0, download: fact.bytesKnown ? fact.download : 0,
        known: fact.bytesKnown))
  default: return nil
  }
}

@MainActor
func updateRoutingTrafficCell(
  _ cell: NSView, key: RoutingTrafficCellKey, store: RoutingWorkbenchStore,
  tooltip: PaxChartTooltipCoordinator
) -> Bool {
  switch key {
  case .latency(let latency):
    guard let view = cell as? PaxTimeLatencyView, view.chart.pinKey == latency.pinKey,
      view.chart.style.title == latency.title
    else { return false }
    view.updateCompact(
      series: latency.series(in: store.livePage), latest: latency.latest, context: latency.context)
  case .count(let count):
    guard let view = cell as? RoutingTrafficCountCell else { return false }
    view.apply(count)
  case .transfer(let transfer):
    return PaxTrafficCells.updateTransfer(
      cell, uploadText: transfer.uploadText, downloadText: transfer.downloadText,
      upload: Double(transfer.upload), download: Double(transfer.download),
      tooltip: transfer.known ? tooltip : nil, note: transfer.note)
  default: return false
  }
  return true
}

@MainActor
func makeRoutingTrafficCell(
  _ id: String, column: Int, key: RoutingTrafficCellKey?,
  store: RoutingWorkbenchStore, icons: WorkbenchObjectIcons,
  tooltip: PaxChartTooltipCoordinator, onActivate: @escaping () -> Void
) -> NSView? {
  if id == "load-more", column == 0 {
    return PaxTrafficCells.objectButton("加载更多") {
      store.liveFrozen = true
      store.scheduleLive(more: true)
    }
  }
  if case .count(let count) = key { return RoutingTrafficCountCell(count) }
  if case .transfer(let transfer) = key {
    return PaxTrafficCells.transfer(
      uploadText: transfer.uploadText, downloadText: transfer.downloadText,
      upload: Double(transfer.upload), download: Double(transfer.download),
      tooltip: transfer.known ? tooltip : nil, note: transfer.note)
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
  if case .latency(let latency) = key {
    display.latencyText = latency.latest
    display.latencyTitle = latency.title
    display.latencyContext = latency.context
    display.latencyPinKey = latency.pinKey
    display.latencySeries = latency.series(in: store.livePage)
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
  let identity = fact?.objectID ?? row.name
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

@MainActor
private final class RoutingTrafficCountCell: NSView {
  private let count = PaxText.label(
    "", font: PaxTheme.Typography.metric(size: PaxTheme.Dashboard.bodyFont))
  private let failures = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)

  init(_ value: RoutingTrafficCellKey.Count) {
    super.init(frame: .zero)
    failures.alignment = .center
    failures.lineBreakMode = .byTruncatingTail
    let labels = NSStackView(views: [count, failures])
    labels.orientation = .vertical
    labels.alignment = .centerX
    labels.spacing = PaxTheme.Spacing.xs
    labels.translatesAutoresizingMaskIntoConstraints = false
    addSubview(labels)
    NSLayoutConstraint.activate([
      labels.leadingAnchor.constraint(equalTo: leadingAnchor),
      labels.trailingAnchor.constraint(equalTo: trailingAnchor),
      labels.centerYAnchor.constraint(equalTo: centerYAnchor),
      count.widthAnchor.constraint(equalTo: labels.widthAnchor),
    ])
    apply(value)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  override var intrinsicContentSize: NSSize {
    NSSize(width: NSView.noIntrinsicMetric, height: PaxTheme.Table.memberRow)
  }

  func apply(_ value: RoutingTrafficCellKey.Count) {
    let text = value.value
    if count.stringValue != text { count.stringValue = text }
    let alignment: NSTextAlignment = value.system && value.failures == 0 ? .natural : .center
    if count.alignment != alignment { count.alignment = alignment }
    let failureText = value.failures > 0 ? value.failureText : ""
    if failures.stringValue != failureText { failures.stringValue = failureText }
    if failures.isHidden != failureText.isEmpty { failures.isHidden = failureText.isEmpty }
    let evidence = value.evidence
    if toolTip != evidence {
      count.toolTip = evidence
      failures.toolTip = evidence
      toolTip = evidence
      setAccessibilityLabel(evidence)
    }
  }
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
