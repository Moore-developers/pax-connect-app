import AppKit

/// 一个 CLI 在比例条里的份额；`client` 是调用方的 CLI 名称，颜色按种类取。
public struct PaxClientShare: Equatable, Sendable {
  public let client: String
  public let value: Double
  public init(_ client: String, value: Double) {
    self.client = client
    self.value = value
  }
}

/// 按 CLI 分段的斜纹比例条：段宽按份额，每段 `hatched`，零 / 非法份额跳过，不画假比例。
public final class PaxClientShareBar: NSView {
  public struct Segment: Equatable {
    public let client: String
    public let rect: NSRect
    public let color: NSColor
  }

  public var shares: [PaxClientShare] {
    didSet {
      setAccessibilityLabel(Self.summary(shares))
      needsDisplay = true
    }
  }

  public init(shares: [PaxClientShare]) {
    self.shares = shares
    super.init(frame: .zero)
    setAccessibilityElement(true)
    setAccessibilityRole(.image)
    setAccessibilityLabel(Self.summary(shares))
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }

  public override var isFlipped: Bool { true }
  public override var intrinsicContentSize: NSSize {
    .init(width: NSView.noIntrinsicMetric, height: PaxTheme.Chart.shareBarHeight)
  }

  /// 有效份额的段矩形：段宽之和加段间隙恰为总宽。
  public static func segments(
    _ shares: [PaxClientShare], in bounds: NSRect, gap: CGFloat = PaxTheme.Layout.cardBorder
  ) -> [Segment] {
    let valid = shares.filter { $0.value.isFinite && $0.value > 0 }
    let total = valid.reduce(0) { $0 + $1.value }
    guard total > 0, bounds.width > 0 else { return [] }
    let usable = max(0, bounds.width - gap * CGFloat(valid.count - 1))
    var x = bounds.minX
    return valid.map { share in
      let width = usable * CGFloat(share.value / total)
      defer { x += width + gap }
      return Segment(
        client: share.client,
        rect: NSRect(x: x, y: bounds.minY, width: width, height: bounds.height),
        color: PaxTheme.Chart.clientSeries(for: share.client))
    }
  }

  private static func summary(_ shares: [PaxClientShare]) -> String {
    let valid = shares.filter { $0.value.isFinite && $0.value > 0 }
    let total = valid.reduce(0) { $0 + $1.value }
    guard total > 0 else { return "暂无来源占比" }
    return valid.map { "\($0.client) \(Int($0.value / total * 100))%" }.joined(separator: "，")
  }

  public override func draw(_ dirtyRect: NSRect) {
    let radius = bounds.height / 2
    NSGraphicsContext.saveGraphicsState()
    NSBezierPath(roundedRect: bounds, xRadius: radius, yRadius: radius).addClip()
    for segment in Self.segments(shares, in: bounds) {
      segment.color.setFill()
      NSBezierPath(rect: segment.rect).fill()
      PaxShareBar.stripes(in: segment.rect)
    }
    NSGraphicsContext.restoreGraphicsState()
  }

  public override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    needsDisplay = true
  }
}

/// CLI 图例：色点 + 名称，颜色与 `PaxClientShareBar` 同源。
public final class PaxClientLegend: NSStackView {
  public struct Entry: Equatable {
    public let title: String
    public let color: NSColor
  }
  public let entries: [Entry]

  /// `clients` 是 CLI 名称（按显示顺序），图例文字即该名称。
  public init(clients: [String]) {
    entries = clients.map { .init(title: $0, color: PaxTheme.Chart.clientSeries(for: $0)) }
    super.init(frame: .zero)
    orientation = .horizontal
    spacing = PaxTheme.Spacing.medium
    for entry in entries {
      let dot = PaxClientLegendDot(color: entry.color)
      let item = NSStackView(views: [
        dot, PaxText.label(entry.title, font: PaxTheme.Typography.caption(), secondary: true),
      ])
      item.spacing = PaxTheme.Spacing.xs
      addArrangedSubview(item)
    }
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
}

private final class PaxClientLegendDot: NSView {
  private let color: NSColor
  init(color: NSColor) {
    self.color = color
    super.init(frame: .zero)
  }
  @available(*, unavailable) required init?(coder: NSCoder) { fatalError("代码构建") }
  override var intrinsicContentSize: NSSize {
    .init(width: PaxTheme.Chart.proportionDot, height: PaxTheme.Chart.proportionDot)
  }
  override func draw(_ dirtyRect: NSRect) {
    color.setFill()
    NSBezierPath(ovalIn: bounds).fill()
  }
}
