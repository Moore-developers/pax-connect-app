import AppKit

/// 统一底色、内边距的原生输入；编辑/选区仍由 NSTextField 处理。
public final class PaxRouteField: NSTextField {
  public enum Style { case form, filter }
  private let style: Style
  private var controlHeight: CGFloat {
    style == .filter ? PaxTheme.Control.filterHeight : PaxTheme.Routes.fieldHeight
  }
  public init(_ value: String, style: Style = .form) {
    self.style = style
    super.init(frame: .zero)
    cell = RouteFieldCell(textCell: value)
    isEditable = true
    isSelectable = true
    stringValue = value
    font = PaxTheme.Typography.label(size: PaxTheme.Routes.bodyFont)
    textColor = PaxTheme.Routes.text
    backgroundColor = PaxTheme.Routes.canvas
    isBordered = false
    isBezeled = false
    drawsBackground = false
    focusRingType = .none
    heightAnchor.constraint(equalToConstant: controlHeight).isActive = true
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var intrinsicContentSize: NSSize {
    NSSize(width: super.intrinsicContentSize.width, height: controlHeight)
  }
  public override func draw(_ dirtyRect: NSRect) {
    let radius = style == .filter ? PaxTheme.Routes.controlRadius : PaxTheme.Radius.control
    let shape = NSBezierPath(
      roundedRect: bounds.insetBy(dx: 0.5, dy: 0.5), xRadius: radius, yRadius: radius)
    (style == .filter ? PaxTheme.Routes.card : PaxTheme.Routes.canvas).setFill()
    shape.fill()
    (style == .filter ? PaxTheme.Routes.line : PaxTheme.Routes.edge).setStroke()
    shape.lineWidth = PaxTheme.Layout.cardBorder
    shape.stroke()
    super.draw(dirtyRect)
  }
}
/// Secure twin of PaxRouteField: same surface, edge, inset and vertical centering.
public final class PaxRouteSecureField: NSSecureTextField {
  public init(placeholder: String = "") {
    super.init(frame: .zero)
    cell = RouteSecureFieldCell(textCell: "")
    isEditable = true
    isSelectable = true
    font = PaxTheme.Typography.label(size: PaxTheme.Routes.bodyFont)
    textColor = PaxTheme.Routes.text
    isBordered = false
    isBezeled = false
    drawsBackground = false
    focusRingType = .none
    placeholderString = placeholder
    heightAnchor.constraint(equalToConstant: PaxTheme.Routes.fieldHeight).isActive = true
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var intrinsicContentSize: NSSize {
    NSSize(width: super.intrinsicContentSize.width, height: PaxTheme.Routes.fieldHeight)
  }
  public override func draw(_ dirtyRect: NSRect) {
    let shape = NSBezierPath(
      roundedRect: bounds.insetBy(dx: 0.5, dy: 0.5), xRadius: PaxTheme.Radius.control,
      yRadius: PaxTheme.Radius.control)
    PaxTheme.Routes.canvas.setFill()
    shape.fill()
    PaxTheme.Routes.edge.setStroke()
    shape.lineWidth = PaxTheme.Layout.cardBorder
    shape.stroke()
    super.draw(dirtyRect)
  }
}
private final class RouteSecureFieldCell: NSSecureTextFieldCell {
  override func select(
    withFrame rect: NSRect, in controlView: NSView, editor textObj: NSText, delegate: Any?,
    start selStart: Int, length selLength: Int
  ) {
    super.select(
      withFrame: drawingRect(forBounds: rect), in: controlView, editor: textObj, delegate: delegate,
      start: selStart, length: selLength)
  }
  override func edit(
    withFrame rect: NSRect, in controlView: NSView, editor textObj: NSText, delegate: Any?,
    event: NSEvent?
  ) {
    super.edit(
      withFrame: drawingRect(forBounds: rect), in: controlView, editor: textObj, delegate: delegate,
      event: event)
  }
  override func drawingRect(forBounds rect: NSRect) -> NSRect {
    let base = super.drawingRect(forBounds: rect)
    let h = cellSize.height
    return NSRect(
      x: rect.minX + PaxTheme.Routes.controlInset, y: rect.midY - h / 2,
      width: max(0, base.width - PaxTheme.Routes.controlInset * 2), height: h)
  }
}
private final class RouteFieldCell: NSTextFieldCell {
  override func select(
    withFrame rect: NSRect, in controlView: NSView, editor textObj: NSText, delegate: Any?,
    start selStart: Int, length selLength: Int
  ) {
    super.select(
      withFrame: drawingRect(forBounds: rect), in: controlView, editor: textObj, delegate: delegate,
      start: selStart, length: selLength)
  }
  override func edit(
    withFrame rect: NSRect, in controlView: NSView, editor textObj: NSText, delegate: Any?,
    event: NSEvent?
  ) {
    super.edit(
      withFrame: drawingRect(forBounds: rect), in: controlView, editor: textObj, delegate: delegate,
      event: event)
  }
  override func drawingRect(forBounds rect: NSRect) -> NSRect {
    let base = super.drawingRect(forBounds: rect)
    let h = cellSize.height
    return NSRect(
      x: rect.minX + PaxTheme.Routes.controlInset, y: rect.midY - h / 2,
      width: max(0, base.width - PaxTheme.Routes.controlInset * 2), height: h)
  }
}

/// 两列常用表单共用一份网格，字段跨列时显式占满。
public final class PaxFormGrid: NSGridView {
  public enum Style { case fields, labelled }
  private let style: Style
  public init(rows: [[NSView]], spanning: Set<Int> = [], style: Style = .fields) {
    self.style = style
    super.init(frame: .zero)
    columnSpacing = PaxTheme.Routes.fieldColumnGap
    rowSpacing = PaxTheme.Routes.fieldGap
    for (index, views) in rows.enumerated() {
      let row = addRow(with: views.count == 1 ? [views[0], NSView()] : views)
      if spanning.contains(index) { row.mergeCells(in: NSRange(location: 0, length: 2)) }
    }
    xPlacement = .fill
    yPlacement = .fill
  }
  public override func layout() {
    super.layout()
    guard numberOfColumns == 2 else { return }
    let left =
      style == .labelled
      ? PaxTheme.Routes.formLabelWidth : max(0, (bounds.width - columnSpacing) / 2)
    column(at: 0).width = left
    column(at: 1).width = max(0, bounds.width - left - columnSpacing)
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
}

/// 小图标座用于详情标题和每个路径节点。
public final class PaxRouteIconTile: NSView {
  private let symbol: String
  private let tone: PaxFeatureTag.Tone
  private let size: CGFloat
  public init(
    _ symbol: String, tone: PaxFeatureTag.Tone = .neutral, size: CGFloat = PaxTheme.Routes.pathIcon
  ) {
    self.symbol = symbol
    self.tone = tone
    self.size = size
    super.init(frame: .zero)
    setAccessibilityElement(false)
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var isFlipped: Bool { true }
  public override var intrinsicContentSize: NSSize { NSSize(width: size, height: size) }
  public override func draw(_ dirtyRect: NSRect) {
    let c = PaxTheme.Routes.self
    let color = tone == .blue ? c.blue : tone == .purple ? c.purple : c.muted
    let base =
      tone == .neutral ? c.sidebar : color.blended(withFraction: 0.88, of: c.card) ?? c.card
    let shape = NSBezierPath(
      roundedRect: bounds.insetBy(dx: 0.5, dy: 0.5), xRadius: c.pathRadius, yRadius: c.pathRadius)
    NSGradient(starting: c.card, ending: base)?.draw(in: shape, angle: 90)
    c.edge.setStroke()
    shape.lineWidth = PaxTheme.Layout.cardBorder
    shape.stroke()
    PaxSurfaceHighlight.draw(
      in: shape.bounds.insetBy(dx: PaxTheme.Layout.cardBorder, dy: PaxTheme.Layout.cardBorder),
      radius: c.pathRadius - PaxTheme.Layout.cardBorder,
      topOpacity: c.pathTopHighlight, sideOpacity: c.pathSideHighlight,
      blur: c.pathHighlightBlur)
    if let image = PaxRouteIcon.image(symbol) {
      PaxRouteDrawing.icon(
        image,
        in: bounds.insetBy(
          dx: (size - PaxTheme.Control.navigationIcon) / 2,
          dy: (size - PaxTheme.Control.navigationIcon) / 2), color: color)
    }
  }
}

@MainActor
enum PaxRouteDrawing {
  static func icon(_ image: NSImage, in rect: NSRect, color: NSColor) {
    let scale = min(
      rect.width / max(1, image.size.width), rect.height / max(1, image.size.height))
    let size = NSSize(width: image.size.width * scale, height: image.size.height * scale)
    let target = NSRect(
      x: rect.midX - size.width / 2, y: rect.midY - size.height / 2, width: size.width,
      height: size.height)
    guard let context = NSGraphicsContext.current?.cgContext else { return }
    NSGraphicsContext.saveGraphicsState()
    NSBezierPath(rect: target).addClip()
    // Tint the vector in the destination context, preserving its alpha and backing scale.
    // A transparency group prevents sourceAtop from coloring the underlying card.
    context.beginTransparencyLayer(auxiliaryInfo: nil)
    image.draw(in: target)
    color.setFill()
    target.fill(using: .sourceAtop)
    context.endTransparencyLayer()
    NSGraphicsContext.restoreGraphicsState()
  }
}

@MainActor
public enum PaxRouteText {
  public static func label(
    _ text: String, font: NSFont = PaxTheme.Typography.label(), secondary: Bool = false,
    wrapping: Bool = false
  ) -> NSTextField {
    let result = PaxText.label(text, font: font, wrapping: wrapping)
    result.textColor = secondary ? PaxTheme.Routes.muted : PaxTheme.Routes.text
    return result
  }
}

/// 原生下拉行为，绘制与地址输入共用的边界/底色。
public final class PaxRoutePicker: NSPopUpButton {
  public enum Style { case form, toolbar, filter }
  private let style: Style
  /// 非 nil 时按钮上画它，菜单项仍显示各自标题。
  public var displayTitle: String? { didSet { needsDisplay = true } }
  private var controlHeight: CGFloat {
    style == .form
      ? PaxTheme.Routes.fieldHeight
      : style == .filter ? PaxTheme.Control.filterHeight : PaxTheme.Routes.controlHeight
  }
  public init(style: Style = .form) {
    self.style = style
    super.init(frame: .zero, pullsDown: false)
    focusRingType = .none
    isBordered = false
    font = PaxTheme.Typography.label(size: PaxTheme.Routes.bodyFont)
    heightAnchor.constraint(equalToConstant: controlHeight).isActive = true
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var intrinsicContentSize: NSSize {
    NSSize(
      width: super.intrinsicContentSize.width + PaxTheme.Routes.controlInset,
      height: controlHeight)
  }
  public override func draw(_ dirtyRect: NSRect) {
    let c = PaxTheme.Routes.self
    let shape = NSBezierPath(
      roundedRect: bounds.insetBy(dx: 0.5, dy: 0.5), xRadius: c.controlRadius,
      yRadius: c.controlRadius)
    c.card.setFill()
    shape.fill()
    c.line.setStroke()
    shape.lineWidth = PaxTheme.Layout.cardBorder
    shape.stroke()
    let attrs: [NSAttributedString.Key: Any] = [
      .font: font ?? PaxTheme.Typography.caption(),
      .foregroundColor: isEnabled ? c.text : PaxTheme.Color.textDisabled,
    ]
    let shown = (displayTitle ?? titleOfSelectedItem ?? "") as NSString
    let size = shown.size(withAttributes: attrs)
    shown.draw(
      in: NSRect(
        x: c.controlInset, y: (bounds.height - size.height) / 2,
        width: bounds.width - c.controlInset * 3, height: size.height), withAttributes: attrs)
    if let image = PaxRouteIcon.image("chevron.down", size: c.metaFont) {
      PaxRouteDrawing.icon(
        image,
        in: NSRect(
          x: bounds.width - c.controlInset - c.metaFont, y: (bounds.height - c.metaFont) / 2,
          width: c.metaFont, height: c.metaFont), color: c.muted)
    }
  }
}

/// 搜索保留 NSSearchField 的查询/清除行为，表面改为原稿的10pt圆角。
public final class PaxRouteSearchField: NSSearchField {
  public override init(frame: NSRect) {
    super.init(frame: frame)
    cell = RouteSearchCell(textCell: "")
    isEditable = true
    isSelectable = true
    isBordered = false
    isBezeled = false
    drawsBackground = false
    focusRingType = .none
    font = PaxTheme.Typography.caption()
  }
  public convenience init() { self.init(frame: .zero) }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var intrinsicContentSize: NSSize {
    NSSize(width: PaxTheme.Routes.searchWidth, height: PaxTheme.Routes.controlHeight)
  }
  public override func draw(_ dirtyRect: NSRect) {
    let c = PaxTheme.Routes.self
    let shape = NSBezierPath(
      roundedRect: bounds.insetBy(dx: 0.5, dy: 0.5), xRadius: c.controlRadius,
      yRadius: c.controlRadius)
    c.card.setFill()
    shape.fill()
    c.line.setStroke()
    shape.lineWidth = PaxTheme.Layout.cardBorder
    shape.stroke()
    super.draw(dirtyRect)
  }
}

private final class RouteSearchCell: NSSearchFieldCell {
  override func select(
    withFrame rect: NSRect, in controlView: NSView, editor textObj: NSText, delegate: Any?,
    start selStart: Int, length selLength: Int
  ) {
    super.select(
      withFrame: searchTextRect(forBounds: rect), in: controlView, editor: textObj,
      delegate: delegate, start: selStart, length: selLength)
  }
  override func edit(
    withFrame rect: NSRect, in controlView: NSView, editor textObj: NSText, delegate: Any?,
    event: NSEvent?
  ) {
    super.edit(
      withFrame: searchTextRect(forBounds: rect), in: controlView, editor: textObj,
      delegate: delegate, event: event)
  }

  override func searchButtonRect(forBounds rect: NSRect) -> NSRect {
    let size = PaxTheme.Routes.nameFont
    return NSRect(
      x: rect.minX + PaxTheme.Routes.controlInset, y: rect.midY - size / 2, width: size,
      height: size)
  }
  override func cancelButtonRect(forBounds rect: NSRect) -> NSRect {
    let size = PaxTheme.Routes.nameFont
    return NSRect(
      x: rect.maxX - PaxTheme.Routes.controlInset - size, y: rect.midY - size / 2, width: size,
      height: size)
  }
  override func searchTextRect(forBounds rect: NSRect) -> NSRect {
    let height = PaxTheme.Typography.caption().boundingRectForFont.height
    let x = searchButtonRect(forBounds: rect).maxX + PaxTheme.Spacing.tight
    return NSRect(
      x: x, y: rect.midY - height / 2,
      width: max(0, cancelButtonRect(forBounds: rect).minX - x - PaxTheme.Spacing.tight),
      height: height)
  }
}
