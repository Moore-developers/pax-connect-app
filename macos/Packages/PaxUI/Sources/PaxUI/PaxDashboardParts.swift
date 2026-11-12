import AppKit

/// 卡片标题的统一白色图标座。Dashboard 与组件目录共享尺寸和颜色。
public final class PaxIconTile: NSView {
  private let image: NSImageView
  public init(_ symbol: String) {
    image = PaxIcon.view(symbol, size: PaxTheme.Dashboard.iconGlyph, weight: .light)
    super.init(frame: .zero)
    wantsLayer = true
    image.contentTintColor = PaxTheme.DashboardColor.icon
    addSubview(image)
    setAccessibilityElement(false)
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override func layout() {
    super.layout()
    image.frame = bounds.insetBy(
      dx: (bounds.width - PaxTheme.Dashboard.iconGlyph) / 2,
      dy: (bounds.height - PaxTheme.Dashboard.iconGlyph) / 2)
    layer?.backgroundColor = PaxTheme.Color.control.cgColor
    layer?.cornerRadius = PaxTheme.Dashboard.iconRadius
    layer?.borderWidth = PaxTheme.Layout.cardBorder
    layer?.borderColor = PaxTheme.DashboardColor.iconBorder.cgColor
  }
}

public final class PaxCardHeading: NSView {
  private let icon: PaxIconTile
  private let title: NSTextField
  private let detail: NSTextField
  public var titleFont: NSFont {
    get { title.font ?? PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyTitle) }
    set {
      title.font = newValue
      needsLayout = true
    }
  }
  public var text: String {
    get { title.stringValue }
    set {
      title.stringValue = newValue
      title.toolTip = newValue
    }
  }
  public var trailing: String {
    get { detail.stringValue }
    set {
      detail.stringValue = newValue
      needsLayout = true
    }
  }
  public override var isFlipped: Bool { true }
  public init(_ text: String, symbol: String, trailing: String = "") {
    icon = PaxIconTile(symbol)
    title = PaxText.label(
      text, font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.bodyTitle, weight: .medium))
    detail = PaxText.label(
      trailing, font: PaxTheme.Typography.label(size: PaxTheme.Dashboard.captionFont),
      secondary: true)
    super.init(frame: .zero)
    title.lineBreakMode = .byTruncatingTail
    for v in [icon, title, detail] { addSubview(v) }
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override func layout() {
    super.layout()
    let d = PaxTheme.Dashboard.self
    icon.frame = NSRect(x: 0, y: 0, width: d.icon, height: d.icon)
    let reserve = detail.stringValue.isEmpty ? 0 : detail.intrinsicContentSize.width + d.titleGap
    title.frame = NSRect(
      x: d.icon + d.titleGap, y: (d.icon - title.intrinsicContentSize.height) / 2,
      width: max(1, bounds.width - d.icon - d.titleGap - reserve),
      height: title.intrinsicContentSize.height)
    detail.frame = NSRect(
      x: bounds.width - detail.intrinsicContentSize.width,
      y: (d.icon - detail.intrinsicContentSize.height) / 2,
      width: detail.intrinsicContentSize.width, height: detail.intrinsicContentSize.height)
  }
}

/// 胶囊 / 小方标签；文字由调用方提供，不认识协议或分流模型。
public final class PaxTag: NSView {
  public enum Style { case capsule, subtle, proxy, direct, currentRoute }
  public var text: String {
    didSet {
      invalidateIntrinsicContentSize()
      needsDisplay = true
    }
  }
  public var font = PaxTheme.Typography.label(size: PaxTheme.Dashboard.captionFont) {
    didSet {
      invalidateIntrinsicContentSize()
      needsDisplay = true
    }
  }
  private let style: Style
  public init(_ text: String, style: Style = .capsule) {
    self.text = text
    self.style = style
    super.init(frame: .zero)
    setAccessibilityElement(true)
    setAccessibilityRole(.staticText)
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var isFlipped: Bool { true }
  public override var intrinsicContentSize: NSSize {
    return NSSize(
      width: (text as NSString).size(withAttributes: [.font: font]).width + PaxTheme.RTT
        .badgePadding * 2, height: PaxTheme.Dashboard.tableBadgeHeight)
  }
  public override func draw(_ dirtyRect: NSRect) {
    let radius =
      (style == .capsule || style == .currentRoute)
      ? bounds.height / 2 : PaxTheme.RTT.badgeSmallRadius
    let path = NSBezierPath(
      roundedRect: bounds.insetBy(
        dx: PaxTheme.Layout.cardBorder / 2, dy: PaxTheme.Layout.cardBorder / 2), xRadius: radius,
      yRadius: radius)
    let fill: NSColor? =
      style == .currentRoute
      ? PaxTheme.CurrentRoute.greenFill
      : style == .subtle
        ? PaxTheme.Color.navigationHover
        : style == .proxy
          ? PaxTheme.DashboardColor.proxyFill
          : style == .direct ? PaxTheme.DashboardColor.directFill : nil
    if let fill {
      fill.setFill()
      path.fill()
    }
    if style == .capsule {
      PaxTheme.Color.cardBorder.setStroke()
      path.lineWidth = PaxTheme.Layout.cardBorder
      path.stroke()
    }
    let color =
      style == .currentRoute
      ? PaxTheme.CurrentRoute.ink
      : style == .proxy
        ? PaxTheme.DashboardColor.proxyText
        : style == .direct ? PaxTheme.DashboardColor.directText : PaxTheme.Color.textSecondary
    let size = (text as NSString).size(withAttributes: [.font: font])
    (text as NSString).draw(
      at: NSPoint(x: (bounds.width - size.width) / 2, y: (bounds.height - size.height) / 2),
      withAttributes: [.font: font, .foregroundColor: color])
    setAccessibilityLabel(text)
  }
}

/// 只读提示不参与鼠标命中，避免遮住宿主后触发移出/关闭/重新进入循环。
@MainActor
private final class PaxChartReadoutContent: NSView {
  override func viewDidMoveToWindow() {
    super.viewDidMoveToWindow()
    window?.ignoresMouseEvents = true
  }
}

/// 一个工作台会话共用一个读数面板；owner限制旧图表不能关闭新图表的读数。
@MainActor
public final class PaxChartTooltipCoordinator {
  private let popover = NSPopover()
  private let content = PaxChartReadoutContent()
  private weak var owner: NSView?
  private var onDismiss: (() -> Void)?
  private var lastText = ""
  private var structuredContent: PaxChartStructuredReadout?
  private weak var scrollingWindow: NSWindow?
  private var inputMonitor: Any?
  public init() {
    let controller = NSViewController()
    controller.view = content
    popover.contentViewController = controller
    popover.behavior = .applicationDefined
    popover.animates = false  // 读数浮层随指针移动，必须即时（motion.md §3）
    inputMonitor = NSEvent.addLocalMonitorForEvents(
      matching: [.scrollWheel, .mouseMoved, .leftMouseDown, .rightMouseDown, .keyDown]
    ) { [weak self] event in
      self?.handleInput(event)
      return event
    }
  }
  isolated deinit {
    if let inputMonitor { NSEvent.removeMonitor(inputMonitor) }
  }

  /// Resume on deliberate pointer/keyboard input, not synthesized tracking-area entry.
  /// This also covers legacy wheels without gesture phases and trackpad momentum.
  private func handleInput(_ event: NSEvent) {
    guard let window = event.window else { return }
    if event.type == .scrollWheel {
      guard event.scrollingDeltaX != 0 || event.scrollingDeltaY != 0 else { return }
      guard scrollingWindow !== window else { return }
      scrollingWindow = window
      if owner?.window === window {
        // Hide only the readout. Keep the owner and its selection/pin dismissal callback.
        popover.close()
      }
    } else if scrollingWindow === window {
      scrollingWindow = nil
    }
  }

  func isShowing(from host: NSView) -> Bool { owner === host && popover.isShown }

  func suppressesHover(in host: NSView) -> Bool {
    guard let scrollingWindow else { return false }
    return host.window === scrollingWindow
  }

  func show(
    _ text: String, from host: NSView, anchor: NSRect? = nil, onDismiss: (() -> Void)? = nil
  ) {
    guard !suppressesHover(in: host) else { return }
    guard host.window != nil, !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
      close(from: host)
      return
    }
    let target = anchor ?? host.bounds
    if popover.isShown && owner === host && popover.positioningRect != target {
      popover.positioningRect = target
    }
    if popover.isShown && owner === host && lastText == text && structuredContent == nil { return }
    if owner !== host { close() }
    owner = host
    self.onDismiss = onDismiss
    lastText = text
    structuredContent = nil
    // 保留同一个内容控制器，避免可见 popover 替换 controller 时产生空壳。
    for view in content.subviews { view.removeFromSuperview() }
    let labels = text.components(separatedBy: " · ").enumerated().map { i, part in
      PaxText.label(
        part,
        font: PaxTheme.Typography.label(
          size: PaxTheme.Dashboard.bodyFont, weight: i == 0 ? .regular : .medium),
        secondary: i == 0)
    }
    let paddingX = PaxTheme.Spacing.medium
    let paddingY = PaxTheme.Spacing.tight
    let gap = PaxTheme.Spacing.xs
    let sizes = labels.map { $0.intrinsicContentSize }
    let size = NSSize(
      width: ceil((sizes.map(\.width).max() ?? 0) + paddingX * 2),
      height: ceil(
        sizes.reduce(0) { $0 + $1.height } + gap * CGFloat(max(0, labels.count - 1)) + paddingY * 2)
    )
    content.setFrameSize(size)
    var y = size.height - paddingY
    for (label, measured) in zip(labels, sizes) {
      y -= measured.height
      label.translatesAutoresizingMaskIntoConstraints = true
      label.frame = NSRect(
        x: paddingX, y: y, width: size.width - paddingX * 2, height: measured.height)
      content.addSubview(label)
      y -= gap
    }
    popover.contentSize = size
    if !popover.isShown { popover.show(relativeTo: target, of: host, preferredEdge: .minY) }
  }
  /// Structured H1 cards share the same single-popover ownership and scroll suppression.
  func show(
    _ hover: PaxAnalysisChart.Hover, from host: NSView, anchor: NSRect,
    onDismiss: (() -> Void)? = nil
  ) {
    guard !suppressesHover(in: host), host.window != nil else { return }
    if popover.isShown && owner === host { popover.positioningRect = anchor }
    let text = hover.accessibilityText
    if popover.isShown && owner === host && lastText == text && structuredContent != nil { return }
    if owner !== host { close() }
    owner = host
    self.onDismiss = onDismiss
    lastText = text
    let card: PaxChartStructuredReadout
    if let existing = structuredContent {
      card = existing
    } else {
      for view in content.subviews { view.removeFromSuperview() }
      card = PaxChartStructuredReadout()
      content.addSubview(card)
      structuredContent = card
    }
    card.apply(hover)
    content.setFrameSize(card.frame.size)
    popover.contentSize = card.frame.size
    if !popover.isShown { popover.show(relativeTo: anchor, of: host, preferredEdge: .minY) }
  }
  func close(from host: NSView) {
    guard owner === host else { return }
    close()
  }
  public func close() {
    let dismiss = onDismiss
    onDismiss = nil
    owner = nil
    lastText = ""
    popover.close()
    dismiss?()
  }
  var activeOwner: NSView? { owner }
  var readoutSize: NSSize { popover.contentSize }
  var readoutAnchor: NSRect { popover.positioningRect }

}

/// 卡片内共享的行分隔层；空行不创建假记录，也不参与辅助访问。
final class PaxRowDividers: NSView {
  var positions: [CGFloat] = [] { didSet { needsDisplay = true } }
  var horizontalInset: CGFloat = 0 { didSet { needsDisplay = true } }
  var header: NSRect? { didSet { needsDisplay = true } }
  private let lines = CAShapeLayer()
  private let headerFill = CAShapeLayer()
  private let headerEdge = CAShapeLayer()
  override var isFlipped: Bool { true }
  override var wantsUpdateLayer: Bool { true }
  override init(frame frameRect: NSRect) {
    super.init(frame: frameRect)
    wantsLayer = true
    layer?.addSublayer(headerFill)
    layer?.addSublayer(lines)
    layer?.addSublayer(headerEdge)
    setAccessibilityElement(false)
  }
  convenience init() { self.init(frame: .zero) }
  @available(*, unavailable)
  required init?(coder: NSCoder) { fatalError("代码构建") }
  override func hitTest(_ point: NSPoint) -> NSView? { nil }
  override func layout() {
    super.layout()
    needsDisplay = true
  }
  override func updateLayer() {
    for shape in [lines, headerFill, headerEdge] { shape.frame = bounds }
    let path = CGMutablePath()
    for y in positions {
      path.move(to: CGPoint(x: horizontalInset, y: y))
      path.addLine(to: CGPoint(x: bounds.width - horizontalInset, y: y))
    }
    lines.path = path
    lines.fillColor = nil
    lines.strokeColor = PaxTheme.DashboardColor.divider.cgColor
    lines.lineWidth = PaxTheme.Layout.cardBorder
    if let header {
      headerFill.path = CGPath(rect: header, transform: nil)
      headerFill.fillColor = PaxTheme.Color.control.cgColor
      let edges = CGMutablePath()
      for y in [header.minY, header.maxY] {
        edges.move(to: CGPoint(x: header.minX, y: y))
        edges.addLine(to: CGPoint(x: header.maxX, y: y))
      }
      headerEdge.path = edges
      headerEdge.fillColor = nil
      headerEdge.strokeColor = PaxTheme.DashboardColor.tableEdge.cgColor
      headerEdge.lineWidth = PaxTheme.Layout.cardBorder
    } else {
      headerFill.path = nil
      headerEdge.path = nil
    }
  }
}
