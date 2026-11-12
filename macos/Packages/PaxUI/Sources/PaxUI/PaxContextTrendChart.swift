import AppKit

/// Two-panel context trend (P07d-ai-session-limits §4.3): stacked token layers on top, one cost
/// stick per request below, sharing the request-index axis. It only draws what it is given;
/// limits, bands, prices and switches are decided by the product and arrive as display data.
@MainActor public final class PaxContextTrendChart: NSView {
  /// One stacked layer; `values` holds this layer's own tokens per request, nil breaks the stack.
  public struct Layer {
    public let name: String
    public let color: NSColor
    public let values: [Double?]
    public init(name: String, color: NSColor, values: [Double?]) {
      self.name = name
      self.color = color
      self.values = values
    }
  }
  /// A horizontal line on the token axis with an end-of-line pill.
  public struct Line {
    public let value: Double
    public let label: String
    public let color: NSColor
    public let labelColor: NSColor
    public let dash: [CGFloat]
    public init(
      value: Double, label: String, color: NSColor, labelColor: NSColor? = nil,
      dash: [CGFloat] = [6, 4]
    ) {
      self.value = value
      self.label = label
      self.color = color
      self.labelColor = labelColor ?? color
      self.dash = dash
    }
  }
  /// Very light tint from `start` (token value) up to the top of the upper panel.
  public struct Danger {
    public let start: Double
    public let color: NSColor
    public init(start: Double, color: NSColor) {
      self.start = start
      self.color = color
    }
  }
  /// Thin vertical line across both panels with a small label, e.g. "第12次起超限".
  public struct FirstOver {
    public let index: Int
    public let text: String
    public let color: NSColor
    public init(index: Int, text: String, color: NSColor) {
      self.index = index
      self.text = text
      self.color = color
    }
  }
  /// Lower panel stick; nil value draws a hollow circle.
  public struct Cost {
    public let value: Double?
    public let color: NSColor
    public init(value: Double?, color: NSColor) {
      self.value = value
      self.color = color
    }
  }
  public struct Marker {
    /// An inline description/value pair; a label joins these into one summary row.
    public struct Detail {
      public let text: String
      public let value: String
      public let color: NSColor
      public init(_ text: String, value: String = "", color: NSColor = PaxTheme.Color.textSecondary)
      {
        self.text = text
        self.value = value
        self.color = color
      }
    }
    public enum Kind {
      /// Two-row floating card: title/annotation, then inline description/value pairs.
      case label(title: String, annotation: String, color: NSColor, details: [Detail])
      /// Short top mark with small text, dropped when there is no room.
      case tick(String)
    }
    public let index: Int
    public let kind: Kind
    public init(index: Int, kind: Kind) {
      self.index = index
      self.kind = kind
    }
  }
  public struct Content {
    public var layers: [Layer] = []
    public var lines: [Line] = []
    public var danger: Danger?
    public var firstOver: FirstOver?
    public var costs: [Cost] = []
    public var markers: [Marker] = []
    public var hovers: [PaxAnalysisChart.Hover] = []
    public init(
      layers: [Layer] = [], lines: [Line] = [], danger: Danger? = nil,
      firstOver: FirstOver? = nil, costs: [Cost] = [], markers: [Marker] = [],
      hovers: [PaxAnalysisChart.Hover] = []
    ) {
      self.layers = layers
      self.lines = lines
      self.danger = danger
      self.firstOver = firstOver
      self.costs = costs
      self.markers = markers
      self.hovers = hovers
    }
  }

  public static let preferredHeight: CGFloat = 400

  public var content = Content() {
    didSet {
      hoveredIndex = nil
      needsDisplay = true
    }
  }
  public var tokenFormat: (Double) -> String = { String(Int($0)) }
  public var costFormat: (Double) -> String = { String($0) }
  public var tokenTitle = "上下文 token"
  public var costTitle = "单次输入费用"
  public var emptyMessage = "没有已记录的请求数据"
  var hoveredIndex: Int? {
    didSet { if hoveredIndex != oldValue { needsDisplay = true } }
  }
  var hoverY: CGFloat = 0
  private var tracking: NSTrackingArea?

  public override var isFlipped: Bool { true }
  public override var intrinsicContentSize: NSSize {
    .init(width: NSView.noIntrinsicMetric, height: Self.preferredHeight)
  }
  public override init(frame: NSRect) {
    super.init(frame: frame)
    wantsLayer = true
    setAccessibilityElement(true)
    setAccessibilityRole(.image)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }

  // MARK: Geometry

  struct Scale {
    let max: Double
    let ticks: [Double]
    /// 1/2/5 x 10^n step giving about `target` intervals; the top rounds up to a whole step.
    init(_ peak: Double, target: Int) {
      let top = peak.isFinite && peak > 0 ? peak : 1
      let raw = top / Double(target)
      let magnitude = pow(10, floor(log10(raw)))
      let ratio = raw / magnitude
      let step = (ratio <= 1 ? 1 : ratio <= 2 ? 2 : ratio <= 5 ? 5 : 10) * magnitude
      let count = Int(ceil(top / step - 1e-9))
      max = Double(count) * step
      ticks = (0...count).map { Double($0) * step }
    }
  }
  struct Pill {
    let y: CGFloat
    let text: String
    let color: NSColor
    let textColor: NSColor
    let width: CGFloat
  }
  struct Geometry {
    var left: CGFloat
    var right: CGFloat
    var top1: CGFloat
    var bottom1: CGFloat
    var top2: CGFloat
    var bottom2: CGFloat
    var count: Int
    var tokens: Scale
    var costs: Scale
    var pills: [Pill]
    var step: CGFloat { (right - left) / CGFloat(max(1, count)) }
    func x(_ index: Int) -> CGFloat { left + (CGFloat(index) + 0.5) * step }
    func tokenY(_ value: Double) -> CGFloat {
      bottom1 - CGFloat(value / tokens.max) * (bottom1 - top1)
    }
    func costY(_ value: Double) -> CGFloat {
      bottom2 - CGFloat(value / costs.max) * (bottom2 - top2)
    }
  }

  static let axisFont = NSFont.systemFont(ofSize: 10.5)
  static let boldFont = NSFont.systemFont(ofSize: 10.5, weight: .semibold)
  static let pillHeight: CGFloat = 18
  static let tickHeight: CGFloat = 8
  static let labelLine: CGFloat = 13
  private static let gap: CGFloat = 16
  private static let axisBottom: CGFloat = 22

  var count: Int { content.layers.map(\.values.count).max() ?? 0 }

  /// Token values of the stack at `index`, bottom first; nil when any layer is missing there.
  func stack(at index: Int) -> [Double]? {
    var total = 0.0
    var result: [Double] = []
    for layer in content.layers {
      guard layer.values.indices.contains(index), let value = layer.values[index] else {
        return nil
      }
      total += value
      result.append(total)
    }
    return result
  }

  /// Indices where the whole stack exists, grouped into consecutive runs.
  func runs() -> [[Int]] {
    var runs: [[Int]] = []
    var run: [Int] = []
    for index in 0..<count {
      if stack(at: index) != nil {
        run.append(index)
      } else if !run.isEmpty {
        runs.append(run)
        run = []
      }
    }
    if !run.isEmpty { runs.append(run) }
    return runs
  }

  func geometry() -> Geometry? {
    let count = count
    guard count > 0, !content.layers.isEmpty else { return nil }
    let hasLabel = content.markers.contains { if case .label = $0.kind { true } else { false } }
    let hasTick = content.markers.contains { if case .tick = $0.kind { true } else { false } }
    let top = hasLabel ? 43 : (hasTick ? 24 : CGFloat(10))
    let usable = bounds.height - top - Self.axisBottom - Self.gap
    guard usable > 40 else { return nil }
    let height1 = (usable * 0.65).rounded()
    let top1 = top
    let bottom1 = top1 + height1
    let top2 = bottom1 + Self.gap
    let bottom2 = top2 + usable - height1

    let peak = (0..<count).compactMap { stack(at: $0)?.last }.max() ?? 0
    let tokens = Scale(
      max(peak, content.lines.map(\.value).max() ?? 0) * 1.06, target: 5)
    let priced = content.costs.compactMap(\.value).sorted()
    var costTop = priced.last ?? 0
    if !priced.isEmpty {
      // Sticks above P95 x 1.2 are cut off and labelled.
      costTop = min(costTop, priced[Int(floor(0.95 * Double(priced.count - 1)))] * 1.2)
    }
    let costs = Scale(costTop, target: 3)

    let labels =
      tokens.ticks.map(tokenFormat) + costs.ticks.dropFirst().map(costFormat)
    let labelWidth = labels.map { width($0, Self.axisFont) }.max() ?? 0
    var geometry = Geometry(
      left: ceil(labelWidth) + 12, right: bounds.width, top1: top1, bottom1: bottom1,
      top2: top2, bottom2: bottom2, count: count, tokens: tokens, costs: costs, pills: [])
    geometry.pills = pills(geometry)
    let pillWidth = geometry.pills.map(\.width).max() ?? 0
    geometry.right = bounds.width - (pillWidth > 0 ? pillWidth + 12 : 8)
    guard geometry.right - geometry.left > 20 else { return nil }
    return geometry
  }

  /// Lines closer than 16pt share one pill, e.g. "预警 250k · 超限 272k".
  private func pills(_ geometry: Geometry) -> [Pill] {
    let placed = content.lines.map { ($0, geometry.tokenY($0.value)) }.sorted { $0.1 < $1.1 }
    var groups: [[(Line, CGFloat)]] = []
    for item in placed {
      if let last = groups.last?.last, item.1 - last.1 < 16 {
        groups[groups.count - 1].append(item)
      } else {
        groups.append([item])
      }
    }
    return groups.map { group in
      let ordered = group.sorted { $0.0.value < $1.0.value }
      let strongest = ordered[ordered.count - 1].0
      let text = ordered.map(\.0.label).joined(separator: " · ")
      return Pill(
        y: group.reduce(0) { $0 + $1.1 } / CGFloat(group.count), text: text,
        color: strongest.color, textColor: strongest.labelColor,
        width: ceil(width(text, Self.boldFont)) + 14)
    }
  }

  // MARK: Text helpers

  func width(_ text: String, _ font: NSFont) -> CGFloat {
    (text as NSString).size(withAttributes: [.font: font]).width
  }

  enum Align { case left, center, right }

  /// Draws `text` vertically centred on `centerY`; `halo` first strokes it in the card colour.
  @discardableResult
  func drawText(
    _ text: String, x: CGFloat, centerY: CGFloat, font: NSFont = PaxContextTrendChart.axisFont,
    color: NSColor = PaxTheme.Color.textSecondary, align: Align = .left, halo: Bool = false
  ) -> NSRect {
    let size = (text as NSString).size(withAttributes: [.font: font])
    let originX: CGFloat =
      switch align {
      case .left: x
      case .center: x - size.width / 2
      case .right: x - size.width
      }
    let point = NSPoint(x: originX, y: centerY - size.height / 2)
    if halo {
      let offsets: [(CGFloat, CGFloat)] = [
        (-1.2, 0), (1.2, 0), (0, -1.2), (0, 1.2), (-0.9, -0.9), (0.9, 0.9),
      ]
      for (dx, dy) in offsets {
        (text as NSString).draw(
          at: .init(x: point.x + dx, y: point.y + dy),
          withAttributes: [.font: font, .foregroundColor: PaxTheme.Color.card])
      }
    }
    (text as NSString).draw(
      at: point, withAttributes: [.font: font, .foregroundColor: color])
    return NSRect(origin: point, size: size)
  }

  func stroke(
    _ from: NSPoint, _ to: NSPoint, color: NSColor, width: CGFloat = 1, dash: [CGFloat] = []
  ) {
    let path = NSBezierPath()
    path.move(to: from)
    path.line(to: to)
    path.lineWidth = width
    if !dash.isEmpty { path.setLineDash(dash, count: dash.count, phase: 0) }
    color.setStroke()
    path.stroke()
  }

  public override func draw(_ dirtyRect: NSRect) {
    guard let g = geometry() else {
      drawText(
        emptyMessage, x: bounds.midX, centerY: bounds.midY, font: NSFont.systemFont(ofSize: 12),
        align: .center)
      return
    }
    drawDanger(g)
    drawGrid(g)
    drawLayers(g)
    drawLines(g)
    drawFirstOver(g)
    drawCosts(g)
    drawXAxis(g)
    drawMarkers(g)
    drawPills(g)
    drawHoverOverlay(g)
  }

  // MARK: Hover

  public override func updateTrackingAreas() {
    super.updateTrackingAreas()
    if let tracking { removeTrackingArea(tracking) }
    tracking = NSTrackingArea(
      rect: bounds,
      options: [.mouseMoved, .mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect],
      owner: self)
    if let tracking { addTrackingArea(tracking) }
  }

  public override func mouseMoved(with event: NSEvent) {
    guard let geometry = geometry() else { return }
    let point = convert(event.locationInWindow, from: nil)
    guard point.x >= geometry.left, point.x <= geometry.right, point.y >= geometry.top1,
      point.y <= geometry.bottom2
    else {
      hoveredIndex = nil
      return
    }
    hoverY = point.y
    hoveredIndex = min(
      geometry.count - 1, max(0, Int((point.x - geometry.left) / geometry.step)))
  }

  public override func mouseExited(with event: NSEvent) {
    hoveredIndex = nil
  }
}
