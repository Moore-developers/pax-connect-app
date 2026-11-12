import AppKit
import Observation
import PaxCore
import PaxUI

@MainActor
final class LogEventDetailController: WorkbenchModalController {
  override var maximumPresentationWidth: CGFloat? { PaxTheme.Logs.detailSize.width }
  private let event: LogPreviewEvent?
  var onClose: (() -> Void)?
  private var expanded = false
  private var notesExpanded = false
  private var perspective: LogDetailPresentation.Perspective = .application
  private var liveDetail: HistoryLogDetail?
  private var telemetry: TelemetryStore?
  private var invalidation: UInt64 = 0
  private var session: UInt64 = 0
  private var onTraffic: ((String) -> Void)?
  private var onRoute: ((String) -> Void)?
  init(event: LogPreviewEvent) {
    self.event = event
    super.init(
      title: event.title,
      subtitle: "2026-10-13 \(event.time) · UTC+08:00",
      size: PaxTheme.Logs.detailSize, style: .event)
  }
  init(
    detail: HistoryLogDetail, telemetry: TelemetryStore? = nil,
    perspective: LogDetailPresentation.Perspective = .application,
    onTraffic: ((String) -> Void)?, onRoute: ((String) -> Void)?
  ) {
    event = nil
    liveDetail = detail
    self.perspective = perspective
    self.telemetry = telemetry
    invalidation = telemetry?.invalidationGeneration ?? 0
    session = telemetry?.sessionGeneration ?? 0
    self.onTraffic = onTraffic
    self.onRoute = onRoute
    let time = detail.record?.lastAt ?? detail.event?.at
    super.init(
      title: LogDetailPresentation(detail: detail, perspective: perspective).title,
      subtitle: time.map(TrafficFormatting.dateMinute) ?? "未记录时间", size: PaxTheme.Logs.detailSize,
      style: .event)
  }
  override func loadView() {
    super.loadView()
    pane.setAccessibilityIdentifier("logs-detail")
    let productLevel = liveDetail?.record?.level ?? liveDetail?.event?.level
    let level: LogsWorkbenchStore.Level =
      productLevel == .error ? .error : productLevel == .warning ? .warning : event?.level ?? .info
    let severity = PaxText.label(level.title, font: PaxTheme.Typography.caption())
    let color = level.presentationColor
    severity.textColor = color
    let icon = NSImageView(
      image: PaxIcon.image(event?.kind.symbol ?? "info.circle", size: PaxTheme.Logs.iconSize)
        ?? NSImage())
    icon.contentTintColor = color
    let kicker = NSStackView(views: [icon, severity])
    kicker.spacing = PaxTheme.Spacing.tight
    pane.identity.insertArrangedSubview(kicker, at: 0)
    pane.identity.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    pane.identity.widthAnchor.constraint(
      lessThanOrEqualTo: pane.header.widthAnchor,
      constant: -PaxTheme.Routes.controlHeight - PaxTheme.Spacing.regular
    ).isActive = true
    for field in pane.identity.arrangedSubviews.compactMap({ $0 as? NSTextField }) {
      field.lineBreakMode = .byTruncatingTail
      field.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
      field.widthAnchor.constraint(lessThanOrEqualTo: pane.identity.widthAnchor).isActive = true
      field.toolTip = field.stringValue
    }
    pane.status.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
    pane.status.stringValue =
      liveDetail == nil
      ? "来源：脱敏模拟数据 · 字段按 v2rayN / sing-box 日志结构"
      : "来源："
        + (liveDetail.map { LogDetailPresentation(detail: $0, perspective: perspective).source }
          ?? "未记录")
    render()
    observeRetention()
  }
  private func observeRetention() {
    guard let telemetry else { return }
    withObservationTracking {
      if telemetry.invalidationGeneration != invalidation
        || (telemetry.connected && session != telemetry.sessionGeneration)
      {
        liveDetail = nil
        pane.clear()
        close()
        return
      }
    } onChange: { [weak self] in
      Task { @MainActor in self?.observeRetention() }
    }
  }
  private func render() {
    pane.clear()
    if let liveDetail {
      renderLive(liveDetail)
      return
    }
    guard let event else { return }
    pane.add(callout(event.kind == .timeout ? "连接失败。等待上游代理响应时超时。" : event.summary))
    pane.add(
      facts([
        ("目标", event.target), ("线路", event.route), ("应用", event.application ?? "未识别"),
        ("域名", event.domain ?? "未记录"), ("结果", event.result),
      ]))
    let separator = NSBox()
    separator.boxType = .separator
    pane.add(separator)
    let toggle = PaxRouteButton(
      "诊断细节与原始日志", symbol: expanded ? "chevron.down" : "chevron.right", style: .quiet,
      target: self, action: #selector(toggle))
    toggle.contentTintColor = PaxTheme.Color.textSecondary
    toggle.font = PaxTheme.Typography.caption()
    toggle.setAccessibilityValue(expanded ? "已展开" : "已折叠")
    let disclosure = NSStackView(views: [toggle, NSView()])
    pane.add(disclosure)
    if expanded {
      pane.add(
        facts([
          ("上下文 ID", event.contextID), ("上下文耗时", event.duration),
          ("本地套接字", event.localSocket), ("上游套接字", event.target), ("出站类型", "VLESS"),
        ]))
      let raw = callout(event.raw, code: true)
      raw.setAccessibilityIdentifier("logs-raw")
      pane.add(raw)
    }
    pane.setActions([])
  }
  override func close() {
    if let onClose { onClose() } else { super.close() }
  }
  private func renderLive(_ detail: HistoryLogDetail) {
    let plan = LogDetailPresentation(detail: detail, perspective: perspective)
    pane.add(callout(plan.status))
    for section in plan.sections {
      pane.add(PaxRouteSectionHeading(section.title, symbol: "info.circle"))
      pane.add(facts(section.rows))
    }
    if !detail.timeline.isEmpty && (detail.record?.flow != nil || detail.timeline.count > 1) {
      pane.add(PaxRouteSectionHeading("已记录的过程", symbol: "list.bullet", detail: "只展示当时保存的事件"))
      for item in detail.timeline.reversed() {
        pane.add(callout(TrafficFormatting.dateMinute(item.at) + " · " + item.message))
      }
    }
    if !plan.notes.isEmpty {
      let notes = PaxRouteButton(
        "记录说明（\(plan.notes.count)）", symbol: notesExpanded ? "chevron.down" : "chevron.right",
        style: .quiet, target: self, action: #selector(toggleNotes))
      pane.add(notes)
      if notesExpanded { pane.add(callout(plan.notes.joined(separator: "\n"))) }
    }
    let f = detail.record?.flow ?? detail.flow
    let toggle = PaxRouteButton(
      "诊断细节与原始日志", symbol: expanded ? "chevron.down" : "chevron.right", style: .quiet, target: self,
      action: #selector(toggle))
    pane.add(toggle)
    if expanded {
      var diagnostic = [
        ("事件 ID", detail.record?.eventID ?? detail.event?.eventID ?? ""), ("来源", plan.source),
      ]
      if let f {
        diagnostic.append(("连接 ID", f.key))
        if !f.evidence.isEmpty { diagnostic.append(("证据", f.evidence)) }
      }
      if let e = detail.record?.systemEvent {
        diagnostic.append(("动作类型", e.type))
        if !e.errorCode.isEmpty { diagnostic.append(("错误码", e.errorCode)) }
      }
      pane.add(facts(diagnostic))
      pane.add(
        callout(
          detail.raw.isEmpty
            ? "未保存原文" + (detail.rawOmitted.isEmpty ? "" : " · " + detail.rawOmitted)
            : detail.raw.joined(separator: "\n"), code: true))
      if detail.truncated { pane.add(callout("原文超过保存上限，已截断")) }
    }
    var actions: [NSButton] = []
    if detail.record?.flow?.key.isEmpty == false || detail.event?.connectionKey.isEmpty == false {
      actions.append(
        button(
          detail.event?.recordID.hasPrefix("problem:") == true ? "代表连接流量" : "查看关联流量",
          #selector(showTraffic)))
    }
    if (detail.record?.flow?.routeID.isEmpty == false && detail.record?.flow?.direction == "proxy")
      || (detail.event?.routeID.isEmpty == false && detail.event?.direction == "proxy")
    {
      actions.append(button("查看线路", #selector(showRoute)))
    }
    pane.setActions(actions)
  }
  @objc private func toggleNotes() {
    notesExpanded.toggle()
    render()
  }
  @objc private func showTraffic() {
    guard let key = liveDetail?.record?.flow?.key ?? liveDetail?.event?.connectionKey else {
      return
    }
    close()
    onTraffic?(key)
  }
  @objc private func showRoute() {
    guard let key = liveDetail?.record?.flow?.routeID ?? liveDetail?.event?.routeID else { return }
    close()
    onRoute?(key)
  }
  private func facts(_ rows: [(String, String)]) -> NSView {
    let views: [[NSView]] = rows.map { name, value in
      let key = PaxText.label(
        name, font: PaxTheme.Typography.label(size: PaxTheme.Logs.bodyFont), secondary: true)
      let text = PaxText.label(
        value, font: PaxTheme.Typography.label(size: PaxTheme.Logs.bodyFont), wrapping: true)
      text.isSelectable = true
      text.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
      return [key, text]
    }
    let grid = NSGridView(views: views)
    grid.columnSpacing = PaxTheme.Logs.factGap
    grid.rowSpacing = PaxTheme.Spacing.medium
    grid.column(at: 0).width = PaxTheme.Logs.factLabel
    grid.column(at: 0).xPlacement = .leading
    grid.column(at: 1).xPlacement = .fill
    return grid
  }
  private func callout(_ text: String, code: Bool = false) -> NSView {
    let inset = PaxTheme.Logs.calloutInset
    let card = PaxCard(
      insets: NSEdgeInsets(top: inset, left: inset, bottom: inset, right: inset), style: .inset)
    let label = PaxText.label(
      text,
      font: code
        ? .monospacedSystemFont(ofSize: PaxTheme.Logs.codeFont, weight: .regular)
        : PaxTheme.Typography.label(size: PaxTheme.Logs.bodyFont), wrapping: true)
    label.isSelectable = true
    card.contentView = label
    label.bottomAnchor.constraint(equalTo: card.bottomAnchor, constant: -inset).isActive = true
    return card
  }
  @objc private func toggle() {
    expanded.toggle()
    render()
  }
}
