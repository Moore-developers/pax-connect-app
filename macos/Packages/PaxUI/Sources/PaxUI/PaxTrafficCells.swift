import AppKit

/// 概览和分流共用的流量单元格；只收显示输入，保持图标/标签/曲线/数值同一布局。
@MainActor public enum PaxTrafficCells {
  /// 对象与规则来源的轻量文字入口；列表和详情共用截断、Return 与辅助访问行为。
  public static func objectButton(_ title: String, onActivate: (() -> Void)?) -> NSButton {
    let name = AnalysisNameButton()
    name.title = title
    name.toolTip = title
    name.isBordered = false
    name.focusRingType = .none
    name.alignment = .left
    name.font = PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyFont)
    name.attributedTitle = NSAttributedString(
      string: title,
      attributes: [
        .font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyFont),
        .foregroundColor: PaxTheme.Color.textSecondary,
      ])
    name.cell?.lineBreakMode = .byTruncatingTail
    name.onActivate = onActivate
    name.setAccessibilityLabel(title + "，查看连接详情")
    return name
  }
  public static func make(
    _ value: PaxTrafficAnalysisRow, column: Int, tooltip: PaxChartTooltipCoordinator? = nil,
    onActivate: (() -> Void)? = nil,
    objectContent: ((NSView) -> NSView)? = nil
  ) -> NSView {
    let d = PaxTheme.Dashboard.self
    switch column {
    case 0:
      let name = objectButton(value.name, onActivate: onActivate)
      if let objectContent { return objectContent(name) }
      let image = PaxIcon.image(value.symbol, size: d.nodeTitle) ?? NSImage()
      return PaxObjectIdentityView(
        image: image, content: name, iconSize: d.nodeTitle,
        gap: d.titleGap, tint: PaxTheme.Color.textSecondary)
    case 1:
      let names = (value.route.isEmpty ? "去向未记录" : value.route).components(separatedBy: "\n")
      let connections = names.map { name in
        let label = PaxText.label(name, font: PaxTheme.Typography.label(size: d.bodyFont))
        label.lineBreakMode = .byTruncatingTail
        label.toolTip = name
        return label
      }
      return TrafficCell(views: connections) { bounds in
        let height = CGFloat(connections.count) * PaxTheme.RTT.textLine
        for (index, connection) in connections.enumerated() {
          connection.frame = NSRect(
            x: 0, y: (bounds.height - height) / 2 + CGFloat(index) * PaxTheme.RTT.textLine,
            width: bounds.width, height: PaxTheme.RTT.textLine)
        }
      }
    case 2:
      let format: (Double?) -> String = { value in
        guard let value else { return "—" }
        return value.formatted(.number.precision(.fractionLength(0...1))) + " ms"
      }
      if let series = value.latencySeries {
        return PaxTimeLatencyView(
          series: series, latest: value.latencyText, compact: true, tooltip: tooltip,
          context: value.latencyContext, title: value.latencyTitle,
          pinKey: value.latencyPinKey ?? "traffic-list-" + value.id,
          format: format)
      }
      return PaxLatencyView(
        samples: value.requests, latest: value.latencyText, compact: true,
        tooltip: tooltip, context: "最新", format: format)
    case 3:
      let count = PaxText.label(
        String(value.count), font: PaxTheme.Typography.metric(size: d.bodyFont))
      count.alignment = .center
      return count
    case 4:
      return transfer(
        uploadText: value.upload, downloadText: value.download, upload: value.uploadValue,
        download: value.downloadValue, tooltip: tooltip)
    default: return NSView()
    }
  }
  /// Discrete latency observations use sequence order, not elapsed time.
  /// Keep explicit interruption markers, but trim empty edges and discard time positions.
  public static func latencyChartSamples(_ samples: [PaxTrendSample]) -> [PaxTrendSample] {
    guard let start = samples.firstIndex(where: { $0.value != nil }),
      let end = samples.lastIndex(where: { $0.value != nil })
    else { return [] }
    if samples.contains(where: { $0.position != nil }) { return samples }
    return samples[start...end].map { sample in
      PaxTrendSample(value: sample.value, label: sample.label)
    }
  }

  public static func transfer(
    uploadText: String, downloadText: String, upload: Double, download: Double,
    tooltip: PaxChartTooltipCoordinator? = nil, note: String? = nil
  ) -> NSView {
    let value = PaxTransferView(
      uploadText: uploadText, downloadText: downloadText, upload: upload,
      download: download, tooltip: tooltip)
    value.toolTip = note
    value.setAccessibilityHelp(note)
    let cell = TrafficCell(views: [value]) { bounds in
      let height = min(bounds.height, PaxTheme.Dashboard.transferContentHeight)
      value.frame = NSRect(
        x: 0, y: (bounds.height - height) / 2, width: bounds.width, height: height)
    }
    cell.toolTip = note
    return cell
  }
}

private final class TrafficCell: NSView {
  private let arrange: (NSRect) -> Void
  init(views: [NSView], arrange: @escaping (NSRect) -> Void) {
    self.arrange = arrange
    super.init(frame: .zero)
    for view in views { addSubview(view) }
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override var isFlipped: Bool { true }
  override func layout() {
    super.layout()
    arrange(bounds)
  }
}
private final class AnalysisNameButton: NSButton {
  var onActivate: (() -> Void)?
  override init(frame: NSRect) {
    super.init(frame: frame)
    target = self
    action = #selector(openDetails)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  @objc private func openDetails() { onActivate?() }
  override func keyDown(with event: NSEvent) {
    if event.keyCode == 36 || event.keyCode == 76 {
      performClick(nil)
    } else {
      super.keyDown(with: event)
    }
  }
}
