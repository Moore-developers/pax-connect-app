import AppKit

/// 聚合后的显示输入；请求样本与节点探测历史互相独立。
public struct PaxTrafficAnalysisRow: Equatable {
  public let id: String
  public let name: String
  public let symbol: String
  public let direction: String
  public let route: String
  public let count: Int
  public let upload: String
  public let download: String
  public let uploadValue: Double
  public let downloadValue: Double
  public let requests: [PaxTrendSample]
  public var latencyText: String
  public var latencyContext = "最新"
  public var latencyTitle = "响应延迟"
  public var latencyPinKey: String?
  /// Real query-window latency series (SCT). When set it replaces the sequence-spaced `requests` chart.
  public var latencySeries: PaxTimeSeries?
  public init(
    id: String, name: String, symbol: String, direction: String, route: String, count: Int,
    upload: String, download: String, uploadValue: Double, downloadValue: Double,
    requests: [PaxTrendSample], latencyText: String
  ) {
    self.id = id
    self.name = name
    self.symbol = symbol
    self.direction = direction
    self.route = route
    self.count = count
    self.upload = upload
    self.download = download
    self.uploadValue = uploadValue
    self.downloadValue = downloadValue
    self.requests = requests
    self.latencyText = latencyText
  }
}

/// 上传、下载各一条渐变段；上方数值同基线，比例由调用方的值决定。
public final class PaxTransferView: NSView {
  private let up: NSTextField, down: NSTextField
  private static let uploadArrow = PaxIcon.image("arrow.up", size: PaxTheme.Dashboard.bodyFont).map
  { PaxImageInk.normalized($0) }
  private static let downloadArrow = PaxIcon.image("arrow.down", size: PaxTheme.Dashboard.bodyFont)
    .map { PaxImageInk.normalized($0) }
  private let upIcon = NSImageView()
  private let downIcon = NSImageView()
  private var arrowInkBottom: CGFloat = 0
  private let tooltip: PaxChartTooltipCoordinator?
  private let readout: String
  private let upload: Double, download: Double
  public override var isFlipped: Bool { true }
  public init(
    uploadText: String, downloadText: String, upload: Double, download: Double,
    tooltip: PaxChartTooltipCoordinator? = nil
  ) {
    self.tooltip = tooltip
    readout = "上传 " + uploadText + " · 下载 " + downloadText
    self.upload = max(0, upload)
    self.download = max(0, download)
    up = PaxText.label(
      uploadText,
      font: PaxTheme.Typography.metric(size: PaxTheme.Dashboard.bodyFont, weight: .medium))
    down = PaxText.label(
      downloadText,
      font: PaxTheme.Typography.metric(size: PaxTheme.Dashboard.bodyFont, weight: .medium))
    super.init(frame: .zero)
    for label in [up, down] {
      label.cell?.usesSingleLineMode = true
      label.lineBreakMode = .byTruncatingTail
    }
    upIcon.image = Self.uploadArrow
    downIcon.image = Self.downloadArrow
    upIcon.imageScaling = .scaleProportionallyUpOrDown
    downIcon.imageScaling = .scaleProportionallyUpOrDown
    upIcon.contentTintColor = PaxTheme.DashboardColor.uploadLabel
    downIcon.contentTintColor = PaxTheme.DashboardColor.greenLabel
    for v in [upIcon, up, downIcon, down] { addSubview(v) }
    setAccessibilityElement(true)
    setAccessibilityRole(.image)
    setAccessibilityLabel(readout)
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override func layout() {
    super.layout()
    let icon = PaxTheme.Dashboard.bodyFont
    let gap = PaxTheme.Spacing.xs
    let font =
      up.font ?? PaxTheme.Typography.metric(size: PaxTheme.Dashboard.bodyFont, weight: .medium)
    // NSTextField draws its glyph baseline after the cell's top inset. Offset the
    // field by font metrics. Number and arrow gaps are independent user settings.
    let line = max(up.intrinsicContentSize.height, down.intrinsicContentSize.height)
    let baseline = ceil(font.ascender)
    let imageHeight = icon
    let numberBaseline = max(baseline, imageHeight)
    arrowInkBottom =
      numberBaseline + PaxTheme.Dashboard.transferGlyphRasterOffset
      + PaxTheme.Dashboard.transferNumberInkGap - PaxTheme.Dashboard.transferArrowInkGap
    let textY = numberBaseline - baseline
    upIcon.frame = NSRect(x: 0, y: arrowInkBottom - imageHeight, width: icon, height: imageHeight)
    up.frame = NSRect(
      x: icon + gap, y: textY, width: max(1, bounds.width / 2 - icon - gap), height: line)
    down.alignment = .right
    let dw = min(ceil(down.intrinsicContentSize.width) + gap, max(1, bounds.width / 2 - icon - gap))
    down.frame = NSRect(x: bounds.width - dw, y: textY, width: dw, height: line)
    downIcon.frame = NSRect(
      x: down.frame.minX - gap - icon, y: arrowInkBottom - imageHeight, width: icon,
      height: imageHeight)

  }
  public override func updateTrackingAreas() {
    super.updateTrackingAreas()
    for area in trackingAreas { removeTrackingArea(area) }
    addTrackingArea(
      NSTrackingArea(
        rect: bounds,
        options: [.activeInKeyWindow, .inVisibleRect, .mouseEnteredAndExited, .mouseMoved],
        owner: self))
  }
  public override func mouseEntered(with event: NSEvent) { tooltip?.show(readout, from: self) }
  public override func mouseMoved(with event: NSEvent) { tooltip?.show(readout, from: self) }
  public override func mouseExited(with event: NSEvent) { tooltip?.close(from: self) }
  public override func viewDidMoveToWindow() {
    super.viewDidMoveToWindow()
    if window == nil { tooltip?.close(from: self) }
  }
  var zeroDirections: [Bool] { [upload == 0, download == 0] }
  var transferRects: [NSRect] {
    let d = PaxTheme.Dashboard.self
    let width = max(0, bounds.width - d.transferGap)
    let total = upload + download
    let fraction = total > 0 ? upload / total : 0.5
    let upWidth = max(d.transferGap, min(max(0, width - d.transferGap), width * fraction))
    let y = arrowInkBottom + d.transferArrowInkGap
    return [
      NSRect(x: 0, y: y, width: upWidth, height: d.transferHeight),
      NSRect(
        x: upWidth + d.transferGap, y: y, width: max(0, width - upWidth), height: d.transferHeight),
    ]
  }
  public override func draw(_ dirtyRect: NSRect) {
    let d = PaxTheme.Dashboard.self
    for (index, rect) in transferRects.enumerated() {
      let path = NSBezierPath(roundedRect: rect, xRadius: d.transferGap, yRadius: d.transferGap)
      if zeroDirections[index] {
        PaxTheme.Table.line.setFill()
        path.fill()
      } else {
        NSGradient(
          colors: index == 0
            ? PaxTheme.DashboardColor.transferUpload : PaxTheme.DashboardColor.transferDownload)?
          .draw(in: path, angle: 0)
      }
    }
  }
}

/// 概览「流量分析」外壳：标题、维度与去向页签、空态。列表由调用方注入，
/// 列、单元格与行高同流量页共用同一份定义，本类不持有业务数据。
public final class PaxTrafficAnalysisCard: PaxCard {
  public var onDimension: ((Int) -> Void)?
  public var onFilter: ((Int) -> Void)?
  private let heading = PaxCardHeading("流量分析", symbol: "arrow.triangle.branch")
  private let dimension: PaxTabs
  private let filter = PaxTabs(["全部", "代理", "直连"])
  private let empty = PaxText.label("", font: PaxTheme.Typography.caption(), secondary: true)
  private var table: PaxDataTable?
  public var preferredHeight: CGFloat {
    PaxTheme.Dashboard.headerHeight + (table?.intrinsicContentSize.height ?? 0)
      + PaxTheme.Layout.cardBorder * 2
  }
  public override var isFlipped: Bool { true }
  public init(periodTitle: String, dimensions: [String]) {
    dimension = PaxTabs(dimensions)
    heading.text = "流量分析 · " + periodTitle
    super.init(insets: NSEdgeInsets())
    clipsContent = true
    for v in [heading, dimension, filter, empty] { addSubview(v) }
    empty.alignment = .center
    dimension.onSelect = { [weak self] in self?.onDimension?($0) }
    filter.onSelect = { [weak self] in self?.onFilter?($0) }
    dimension.setAccessibilityIdentifier("workbench-dimension")
    filter.setAccessibilityIdentifier("workbench-direction")
    setAccessibilityIdentifier("workbench-traffic-table")
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  /// 展示给定表格；维度切换时可换成另一张表，旧表只摘下不销毁。
  public func show(_ next: PaxDataTable) {
    guard next !== table else { return }
    table?.removeFromSuperview()
    table = next
    addSubview(next, positioned: .below, relativeTo: empty)
    needsLayout = true
  }
  /// `emptyMessage` 非空时显示在占位行上方；数据行数只用于辅助功能描述。
  public func apply(
    dimensionIndex: Int, filterIndex: Int, rowCount: Int, emptyMessage: String?,
    controlsEnabled: Bool = true
  ) {
    dimension.isEnabled = controlsEnabled
    filter.isEnabled = controlsEnabled
    dimension.selectedIndex = dimensionIndex
    filter.selectedIndex = filterIndex
    setAccessibilityLabel("流量分析，\(rowCount) 项")
    empty.stringValue = emptyMessage ?? ""
    empty.isHidden = emptyMessage == nil
    needsLayout = true
  }
  public override func layout() {
    super.layout()
    let d = PaxTheme.Dashboard.self
    let fw = filter.intrinsicContentSize.width
    let dw = dimension.intrinsicContentSize.width
    filter.frame = NSRect(
      x: bounds.width - d.inset - fw, y: d.inset, width: fw, height: d.tabHeight)
    dimension.frame = NSRect(
      x: filter.frame.minX - PaxTheme.Spacing.medium - dw, y: d.inset, width: dw,
      height: d.tabHeight)
    heading.frame = NSRect(
      x: d.inset, y: d.inset,
      width: max(1, dimension.frame.minX - d.inset - PaxTheme.Spacing.medium), height: d.icon)
    table?.frame = NSRect(
      x: PaxTheme.Layout.cardBorder, y: d.headerHeight,
      width: bounds.width - PaxTheme.Layout.cardBorder * 2,
      height: max(0, bounds.height - d.headerHeight - PaxTheme.Layout.cardBorder))
    empty.frame = NSRect(
      x: d.inset, y: d.headerHeight + d.tableHeader + d.rowHeight * 2,
      width: bounds.width - d.inset * 2, height: d.rowHeight)
  }
}
