import AppKit

/// Shared traffic plot. Drawing anchors join known observations;
/// they never become measured values or tooltip records.
public final class PaxRouteTrafficChart: NSView {
  public var compact = false { didSet { needsDisplay = true } }
  public var samples: [PaxChartSample] {
    didSet {
      guard samples != oldValue else { return }
      hover = nil
      pinned = false
      tooltipCoordinator?.close(from: self)
      updateScale()
      needsDisplay = true
    }
  }
  private(set) var scaleMaximum: Double = 1
  private(set) var plotLeftInset: CGFloat = 0
  public var tooltipCoordinator: PaxChartTooltipCoordinator?
  private var hover: Int?
  private var pinned = false
  private let formatValue: (Double) -> String
  public init(samples: [PaxChartSample], formatValue: @escaping (Double) -> String) {
    self.formatValue = formatValue
    self.samples = samples
    super.init(frame: .zero)
    setAccessibilityElement(true)
    setAccessibilityRole(.image)
    updateScale()
  }
  private func updateScale() {
    scaleMaximum = Self.upperBound(samples)
    let labelWidth =
      [0, scaleMaximum / 2, scaleMaximum].map {
        (formatValue($0) as NSString).size(withAttributes: [.font: PaxTheme.Typography.caption()])
          .width
      }.max() ?? 0
    plotLeftInset = max(PaxTheme.Routes.chartLeft, ceil(labelWidth) + PaxTheme.Spacing.tight)
    setAccessibilityLabel(
      "流量趋势，上传和下载堆叠曲线；缺测间连线仅为趋势；" + samples.indices.map(readout).joined(separator: "；"))
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var isFlipped: Bool { true }
  public override var acceptsFirstResponder: Bool { true }
  private var plot: NSRect {
    let c = PaxTheme.Routes.self
    if compact { return bounds.insetBy(dx: c.chartPointRadius, dy: c.chartPointRadius) }
    return NSRect(
      x: plotLeftInset, y: c.chartTop,
      width: max(1, bounds.width - plotLeftInset - PaxTheme.Spacing.tight),
      height: max(1, bounds.height - c.chartAxis - c.chartTop))
  }
  private static func upperBound(_ samples: [PaxChartSample]) -> Double {
    let maximum =
      samples.filter {
        $0.values.count >= 2 && $0.values.prefix(2).allSatisfy { $0.isFinite && $0 >= 0 }
      }.map { $0.values.prefix(2).reduce(0, +) }.max() ?? 0
    guard maximum > 0 else { return 1 }
    let padded = maximum * PaxTheme.Routes.chartScaleHeadroom
    let magnitude = pow(10, floor(log10(padded)))
    let fraction = padded / magnitude
    let rounded = [1.0, 2, 3, 4, 5, 6, 8, 10].first { $0 >= fraction } ?? 10
    return rounded * magnitude
  }
  private func value(_ sample: PaxChartSample, series: Int) -> Double? {
    guard sample.values.indices.contains(series) else { return nil }
    let value = sample.values[series]
    return value.isFinite && value >= 0 ? value : nil
  }
  private func x(_ index: Int) -> CGFloat {
    if let first = samples.first?.at, let last = samples.last?.at, last > first,
      let at = samples[index].at
    {
      return plot.minX + CGFloat(at.timeIntervalSince(first) / last.timeIntervalSince(first))
        * plot.width
    }
    if samples.count == 1 { return plot.midX }
    return plot.minX + CGFloat(index) / CGFloat(max(1, samples.count - 1)) * plot.width
  }
  private func point(_ index: Int, series: Int) -> NSPoint? {
    guard let upload = value(samples[index], series: 0),
      let download = value(samples[index], series: 1)
    else { return nil }
    let amount = series == 0 ? upload : upload + download
    return NSPoint(x: x(index), y: plot.maxY - CGFloat(amount / scaleMaximum) * plot.height)
  }
  func lineRuns(series: Int) -> [[NSPoint]] {
    let valid = samples.indices.filter { point($0, series: series) != nil }
    guard !valid.isEmpty else { return [] }
    // Time buckets already aggregate the whole window. Never manufacture
    // observations or pick arbitrary raw points to satisfy a visual count.
    let indices = Array(valid)
    let points = indices.compactMap { point($0, series: series) }
    return [points]
  }
  func boundaryPath(series: Int) -> NSBezierPath {
    let path = NSBezierPath()
    let lower = series == 1 ? (lineRuns(series: 0).first ?? []) : []
    for points in lineRuns(series: series) {
      path.append(PaxChartPlot.path(through: points, above: lower))
    }
    path.lineCapStyle = .round
    path.lineJoinStyle = .round
    return path
  }
  func readoutIndex(atX xPosition: CGFloat) -> Int? {
    guard !samples.isEmpty else { return nil }
    let previous = samples.indices.last { x($0) <= xPosition }
    if let previous, samples[previous].values.isEmpty, previous < samples.count - 1,
      xPosition < x(previous + 1)
    {
      return previous
    }
    return samples.indices.min { abs(x($0) - xPosition) < abs(x($1) - xPosition) }
  }
  func readout(at index: Int) -> String {
    guard samples.indices.contains(index) else { return "" }
    let sample = samples[index]
    guard let upload = value(sample, series: 0), let download = value(sample, series: 1) else {
      return sample.label + "\n该时段未采集流量"
    }
    let interval =
      sample.interval > 0
      ? " — "
        + (sample.at.map {
          DateFormatter.localizedString(
            from: $0.addingTimeInterval(sample.interval), dateStyle: .none, timeStyle: .short)
        } ?? "") : ""
    return sample.label + interval + (sample.partial ? "\n部分采集 · 仅含已观测流量" : "") + "\n上传 "
      + formatValue(upload)
      + "\n下载 "
      + formatValue(download) + "\n合计 " + formatValue(upload + download)
  }
  public override func updateTrackingAreas() {
    super.updateTrackingAreas()
    for area in trackingAreas { removeTrackingArea(area) }
    addTrackingArea(
      NSTrackingArea(
        rect: bounds,
        options: [.mouseMoved, .mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect],
        owner: self))
  }
  public override func mouseMoved(with event: NSEvent) {
    guard tooltipCoordinator?.suppressesHover(in: self) != true else { return }
    guard !pinned else {
      if tooltipCoordinator?.isShowing(from: self) == false { choose(hover) }
      return
    }
    let location = convert(event.locationInWindow, from: nil)
    guard plot.contains(location) else {
      choose(nil)
      return
    }
    choose(readoutIndex(atX: location.x))
  }
  public override func mouseDown(with event: NSEvent) {
    window?.makeFirstResponder(self)
    if pinned {
      pinned = false
      choose(nil)
    } else {
      mouseMoved(with: event)
      pinned = hover != nil
    }
  }
  public override func mouseExited(with event: NSEvent) {
    if !pinned { choose(nil) }
  }
  public override func keyDown(with event: NSEvent) {
    if event.keyCode == 123 {
      choose(max(0, (hover ?? samples.count) - 1))
    } else if event.keyCode == 124 {
      choose(min(samples.count - 1, (hover ?? -1) + 1))
    } else if event.keyCode == 53 {
      pinned = false
      choose(nil)
    } else {
      super.keyDown(with: event)
    }
  }
  private func choose(_ index: Int?) {
    hover = index.flatMap { samples.indices.contains($0) ? $0 : nil }
    needsDisplay = true
    guard let hover else {
      tooltipCoordinator?.close(from: self)
      setAccessibilityValue("")
      return
    }
    let text = readout(at: hover)
    setAccessibilityValue(text)
    tooltipCoordinator?.show(
      text, from: self, anchor: NSRect(x: x(hover), y: plot.minY, width: 1, height: 1)
    ) { [weak self] in
      self?.pinned = false
      self?.hover = nil
      self?.needsDisplay = true
    }
  }
  public override func draw(_ dirtyRect: NSRect) {
    guard !samples.isEmpty else { return }
    let c = PaxTheme.Routes.self
    let attrs: [NSAttributedString.Key: Any] = [
      .font: PaxTheme.Typography.caption(), .foregroundColor: c.muted,
    ]
    let maximum = scaleMaximum
    for value in compact ? [] : [0, maximum / 2, maximum] {
      let y = plot.maxY - CGFloat(value / maximum) * plot.height
      let grid = NSBezierPath()
      grid.move(to: NSPoint(x: plot.minX, y: y))
      grid.line(to: NSPoint(x: plot.maxX, y: y))
      c.line.setStroke()
      grid.lineWidth = PaxTheme.Layout.cardBorder
      grid.stroke()
      let text = formatValue(value) as NSString
      let size = text.size(withAttributes: attrs)
      text.draw(
        at: NSPoint(x: plot.minX - PaxTheme.Spacing.tight - size.width, y: y - size.height / 2),
        withAttributes: attrs)
    }
    if let hover {
      let crosshair = NSBezierPath()
      crosshair.move(to: NSPoint(x: x(hover), y: plot.minY))
      crosshair.line(to: NSPoint(x: x(hover), y: plot.maxY))
      c.muted.withAlphaComponent(PaxTheme.Color.noticeFillOpacity).setStroke()
      crosshair.stroke()
    }
    let upload = lineRuns(series: 0).first ?? []
    let total = lineRuns(series: 1).first ?? []
    PaxChartPlot(
      bounds: plot,
      series: [
        .init(
          points: upload, color: PaxTheme.DashboardColor.amber,
          lowerBoundary: upload.map { NSPoint(x: $0.x, y: plot.maxY) }),
        .init(points: total, color: PaxTheme.DashboardColor.green, lowerBoundary: upload),
      ]
    ).draw()
    if let hover {
      for series in [0, 1] {
        if let point = point(hover, series: series) {
          PaxChartPlot.selection(
            at: point,
            color: series == 0 ? PaxTheme.DashboardColor.amber : PaxTheme.DashboardColor.green)
        }
      }
    }
    for label in compact ? [] : axisLabelFrames() {
      (samples[label.index].label as NSString).draw(at: label.rect.origin, withAttributes: attrs)
    }
  }
  func axisLabelFrames() -> [(index: Int, rect: NSRect)] {
    guard !samples.isEmpty else { return [] }
    let attrs: [NSAttributedString.Key: Any] = [.font: PaxTheme.Typography.caption()]
    var labels: [(index: Int, rect: NSRect)] = []
    let candidates = [samples.startIndex, samples.index(before: samples.endIndex)]
    let midpoint = samples.indices.min { abs(x($0) - plot.midX) < abs(x($1) - plot.midX) }
    for index in candidates + [midpoint].compactMap({ $0 }) {
      let size = (samples[index].label as NSString).size(withAttributes: attrs)
      let left = min(plot.maxX - size.width, max(plot.minX, x(index) - size.width / 2))
      let rect = NSRect(
        x: left, y: bounds.height - size.height, width: size.width, height: size.height)
      guard
        !labels.contains(where: {
          $0.rect.insetBy(dx: -PaxTheme.Spacing.tight, dy: 0).intersects(rect)
        })
      else { continue }
      labels.append((index, rect))
    }
    return labels
  }

  public override func viewWillMove(toWindow newWindow: NSWindow?) {
    if newWindow == nil { tooltipCoordinator?.close(from: self) }
    super.viewWillMove(toWindow: newWindow)
  }
}
