import AppKit

/// RTT 历史的视图值，不是网络探测或持久化模型。
public struct PaxRTTSample: Equatable {
  public enum Outcome: Equatable {
    case measured(Double)
    case timeout, missing
  }
  public let time: String
  public let outcome: Outcome
  public init(time: String, outcome: Outcome) {
    self.time = time
    self.outcome = outcome
  }
  public var milliseconds: Double? {
    if case .measured(let value) = outcome, value.isFinite, value >= 0 { return value }
    return nil
  }
  public var description: String {
    switch outcome {
    case .measured: milliseconds.map { "\(Int($0)) ms" } ?? "未测"
    case .timeout: "超时"
    case .missing: "未测"
    }
  }
}

/// 同一阈值映射用于数字与色条；文字版本留出对比度，条纹版本提高饱和度。
@MainActor
public enum PaxRTTColor {
  public static func band(_ value: Double) -> Int { value <= 100 ? 0 : value <= 300 ? 1 : 2 }
  public static func solid(_ value: Double) -> NSColor { PaxTheme.DashboardColor.rtt[band(value)] }
  public static func color(_ value: Double, role: PaxTheme.RTT.Role) -> NSColor {
    let (start, end) = PaxTheme.RTT.endpoints(band: band(value), role: role)
    let fraction =
      band(value) == 0
      ? value / 100
      : band(value) == 1 ? (value - 100) / 200 : (value - 300) / 300
    return start.blended(withFraction: min(1, max(0, fraction)), of: end) ?? start
  }

}

/// 圆头竖条历史：每个真实样本对应一根竖条，不复制采样。
public final class PaxRTTHistoryView: NSView {
  public var samples: [PaxRTTSample] = [] {
    didSet {
      if oldValue != samples {
        tooltipCoordinator?.close(from: self)
        highlight(nil)
      }
      updateSummary()
      needsDisplay = true
    }
  }
  public var barHeight = PaxTheme.RTT.rowStrip
  public var tooltipCoordinator: PaxChartTooltipCoordinator?
  public var onSample: ((Int) -> Void)?
  private var selectedIndex: Int?
  public func highlight(_ index: Int?) {
    selectedIndex = index.flatMap { samples.indices.contains($0) ? $0 : nil }
    setAccessibilityValue(
      selectedIndex.map { samples[$0].time + " " + samples[$0].description } ?? "")
    needsDisplay = true
  }
  public override var isFlipped: Bool { true }
  public override var acceptsFirstResponder: Bool { true }
  public override init(frame frameRect: NSRect) {
    super.init(frame: frameRect)
    setAccessibilityElement(true)
    setAccessibilityRole(.image)
  }
  public convenience init() { self.init(frame: .zero) }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var intrinsicContentSize: NSSize {
    NSSize(width: NSView.noIntrinsicMetric, height: PaxTheme.RTT.slotHeight)
  }
  public override func layout() {
    super.layout()
    updateSummary()
  }
  private func updateSummary() {
    setAccessibilityLabel(
      "RTT历史，每格5分钟，" + samples.map { $0.time + " " + $0.description }.joined(separator: "；"))
  }
  public override func updateTrackingAreas() {
    super.updateTrackingAreas()
    for area in trackingAreas { removeTrackingArea(area) }
    addTrackingArea(
      NSTrackingArea(
        rect: bounds,
        options: [.activeInKeyWindow, .inVisibleRect, .mouseMoved, .mouseEnteredAndExited],
        owner: self))
  }
  public override func mouseMoved(with event: NSEvent) {
    guard !samples.isEmpty else { return }
    let x = convert(event.locationInWindow, from: nil).x
    showSample(
      min(samples.count - 1, max(0, Int(x / max(1, bounds.width) * CGFloat(samples.count)))))
  }
  public override func mouseExited(with event: NSEvent) { tooltipCoordinator?.close(from: self) }
  private func showSample(_ index: Int) {
    tooltipCoordinator?.show(
      samples[index].time + " · 节点 RTT " + samples[index].description, from: self,
      anchor: NSRect(
        x: (CGFloat(index) + 0.5) * bounds.width / CGFloat(samples.count),
        y: (bounds.height - barHeight) / 2, width: 1, height: 1))
  }
  public override func viewDidMoveToWindow() {
    super.viewDidMoveToWindow()
    if window == nil { tooltipCoordinator?.close(from: self) }
  }
  public override func mouseDown(with event: NSEvent) {
    guard !samples.isEmpty else { return }
    window?.makeFirstResponder(self)
    let x = convert(event.locationInWindow, from: nil).x
    select(min(samples.count - 1, max(0, Int(x / max(1, bounds.width) * CGFloat(samples.count)))))
  }
  private func select(_ index: Int) {
    selectedIndex = index
    showSample(index)
    onSample?(index)
    setAccessibilityValue(samples[index].time + " " + samples[index].description)
    needsDisplay = true
  }
  public override func keyDown(with event: NSEvent) {
    guard !samples.isEmpty else { return }
    if event.keyCode == 123 || event.keyCode == 124 {
      select(min(samples.count - 1, max(0, (selectedIndex ?? 0) + (event.keyCode == 124 ? 1 : -1))))
    } else {
      if event.keyCode == 53 {
        highlight(nil)
        tooltipCoordinator?.close(from: self)
      } else {
        super.keyDown(with: event)
      }
    }
  }
  public override func draw(_ dirtyRect: NSRect) {
    guard !samples.isEmpty else { return }
    let strip = NSRect(
      x: 0, y: (bounds.height - barHeight) / 2, width: bounds.width, height: barHeight)
    let step = strip.width / CGFloat(samples.count)
    for (index, sample) in samples.enumerated() {
      let color =
        sample.milliseconds.map { PaxRTTColor.color($0, role: .bar) }
        ?? PaxTheme.Color.navigationHover
      color.setFill()
      let width = min(PaxTheme.RTT.historyPillWidth, step)
      let x = CGFloat(index) * step + (step - width) / 2
      NSBezierPath(
        roundedRect: NSRect(x: x, y: strip.minY, width: width, height: strip.height),
        xRadius: width / 2, yRadius: width / 2
      ).fill()
    }
    if let selectedIndex, samples.indices.contains(selectedIndex) {
      PaxTheme.Color.textSecondary.setStroke()
      let selection = NSBezierPath(
        roundedRect: NSRect(
          x: CGFloat(selectedIndex) * step, y: strip.minY - 2, width: step, height: strip.height + 4
        ), xRadius: 2, yRadius: 2)
      selection.lineWidth = 1
      selection.stroke()
    }
  }
  public override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }
}
