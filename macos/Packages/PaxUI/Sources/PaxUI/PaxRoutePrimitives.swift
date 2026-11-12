import AppKit

/// 标签外观按用途区分；当前线路保留原稿的实蓝、白勾与轻阴影。
public final class PaxFeatureTag: NSView {
  public enum Tone {
    case neutral, blue, purple, warning, green, yellow, amber, red
    public var color: NSColor {
      switch self {
      case .neutral: PaxTheme.Routes.muted
      case .blue: PaxTheme.Routes.selectedBlue
      case .purple: PaxTheme.Routes.purple
      case .warning: PaxTheme.Color.warning
      case .green: PaxTheme.Color.healthy
      case .yellow: PaxTheme.Color.yellowLabel
      case .amber: PaxTheme.Color.amberLabel
      case .red: PaxTheme.Color.error
      }
    }
  }
  public enum Style { case feature, current, protocolLabel }
  private let title: String
  private let symbol: String?
  private let tone: Tone
  private let style: Style
  private let solidFill: NSColor?
  private let font: NSFont?
  private let foregroundColor: NSColor?
  /// A solid fill uses white text; omitted values preserve the standard tag appearance.
  public init(
    _ title: String, symbol: String? = nil, tone: Tone = .neutral, style: Style = .feature,
    solidFill: NSColor? = nil, font: NSFont? = nil, foregroundColor: NSColor? = nil
  ) {
    self.title = title
    self.symbol = symbol
    self.tone = tone
    self.style = style
    self.solidFill = solidFill
    self.font = font
    self.foregroundColor = foregroundColor
    super.init(frame: .zero)
    setAccessibilityElement(true)
    setAccessibilityRole(.staticText)
    setAccessibilityLabel(title)
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var isFlipped: Bool { true }
  public override var intrinsicContentSize: NSSize {
    let text = (title as NSString).size(
      withAttributes: [.font: font ?? PaxTheme.Typography.caption()])
    return NSSize(
      width: text.width + PaxTheme.Routes.tagInset * 2
        + (symbol == nil ? 0 : PaxTheme.Routes.metaFont + PaxTheme.Spacing.xs),
      height: style == .current ? PaxTheme.Routes.currentHeight : PaxTheme.Routes.tagHeight)
  }
  public override func draw(_ dirtyRect: NSRect) {
    let c = PaxTheme.Routes.self
    let ink = tone.color
    let color =
      foregroundColor
      ?? (solidFill != nil
        ? PaxTheme.Color.onSolid
        : style == .current
          ? c.card
          : ink)
    let radius = style == .feature ? c.tagRadius : PaxTheme.Radius.control / 2
    let shape = NSBezierPath(
      roundedRect: bounds.insetBy(dx: 0.5, dy: 0.5), xRadius: radius, yRadius: radius)
    if let solidFill {
      solidFill.setFill()
      shape.fill()
    } else if style == .current {
      NSGraphicsContext.saveGraphicsState()
      let shadow = NSShadow()
      shadow.shadowColor = c.selectedBlue.withAlphaComponent(0.19)
      shadow.shadowBlurRadius = PaxTheme.Spacing.xs
      shadow.shadowOffset = PaxTheme.Control.selectedShadowOffset
      shadow.set()
      c.selectedBlue.setFill()
      shape.fill()
      NSGraphicsContext.restoreGraphicsState()
      NSGradient(colors: c.currentGradient)?.draw(in: shape, angle: 90)
    } else {
      let base =
        style == .protocolLabel
        ? c.canvas
        : (tone == .blue ? c.blue : ink)
          .blended(
            withFraction: 1 - c.tagOpacity, of: c.card) ?? c.card
      base.setFill()
      shape.fill()
    }
    var x = c.tagInset
    if let symbol, let image = PaxRouteIcon.image(symbol, size: c.metaFont) {
      PaxRouteDrawing.icon(
        image,
        in: NSRect(
          x: x, y: (bounds.height - c.metaFont) / 2, width: c.metaFont, height: c.metaFont),
        color: color)
      x += c.metaFont + PaxTheme.Spacing.xs
    }
    let attributes: [NSAttributedString.Key: Any] = [
      .font: font
        ?? PaxTheme.Typography.label(
          size: c.metaFont, weight: style == .current ? .medium : .regular),
      .foregroundColor: color,
    ]
    let size = (title as NSString).size(withAttributes: attributes)
    (title as NSString).draw(
      at: NSPoint(x: x, y: (bounds.height - size.height) / 2), withAttributes: attributes)
  }
}

/// 已格式化的节点路径，图标座与地址行用于概览及链式预览。
public final class PaxConnectionPath: NSStackView {
  public struct Node: Equatable {
    public let title: String
    public let address: String
    public let symbol: String
    public let tone: PaxFeatureTag.Tone
    public init(
      _ title: String, address: String = "", symbol: String, tone: PaxFeatureTag.Tone = .neutral
    ) {
      self.title = title
      self.address = address
      self.symbol = symbol
      self.tone = tone
    }
  }
  public init(nodes: [Node]) {
    super.init(frame: .zero)
    orientation = .horizontal
    alignment = .centerY
    spacing = PaxTheme.Spacing.medium
    distribution = .fill
    var previous: NSView?
    for (index, node) in nodes.enumerated() {
      if index > 0 {
        addArrangedSubview(PaxRouteIcon.view("chevron.right", size: PaxTheme.Routes.nameFont))
      }
      let symbol = PaxRouteIconTile(node.symbol, tone: node.tone)
      symbol.widthAnchor.constraint(equalToConstant: PaxTheme.Routes.pathIcon).isActive = true
      let text = NSStackView(views: [
        PaxText.label(
          node.title,
          font: PaxTheme.Typography.label(size: PaxTheme.Routes.bodyFont, weight: .medium)),
        PaxText.label(node.address, font: PaxTheme.Typography.caption(), secondary: true),
      ])
      text.orientation = .vertical
      text.alignment = .leading
      text.spacing = PaxTheme.Spacing.xs
      let row = NSStackView(views: [symbol, text])
      row.spacing = PaxTheme.Routes.toolsGap
      addArrangedSubview(row)
      if let previous { row.widthAnchor.constraint(equalTo: previous.widthAnchor).isActive = true }
      previous = row
    }
    setAccessibilityLabel(nodes.map { $0.title + " " + $0.address }.joined(separator: " → "))
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
}
