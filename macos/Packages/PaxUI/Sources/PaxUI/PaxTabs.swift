import AppKit

/// 两处筛选和代理开关使用同一滑块结构；代理样式支持拖动与左右键。
public final class PaxTabs: NSControl {
  public enum Style { case tabs, mode }
  public var distributesEqually = false {
    didSet {
      needsLayout = true
      needsDisplay = true
    }
  }
  public var onSelect: ((Int) -> Void)?
  public var selectedIndex: Int = 0 {
    didSet {
      if oldValue != selectedIndex { animateThumb(to: CGFloat(selectedIndex)) }
      updateModeShadow()
      needsDisplay = true
      for (i, button) in buttons.enumerated() { button.state = i == selectedIndex ? .on : .off }
      setAccessibilityValue(
        accessibilityLabels.indices.contains(selectedIndex)
          ? accessibilityLabels[selectedIndex] : "")
    }
  }
  public override var isEnabled: Bool {
    didSet {
      for button in buttons { button.isEnabled = isEnabled }
      needsDisplay = true
    }
  }
  private var buttons: [TabHitButton] = []
  private let labels: [String]
  private let symbols: [String?]
  private let counts: [Int?]
  private let accessibilityLabels: [String]
  private let style: Style
  var thumbPosition: CGFloat = 0
  var thumbRun: PaxThumbRun?
  var thumbLink: CADisplayLink?
  private var dragPosition: CGFloat?
  private var startX: CGFloat = 0
  public init(
    _ labels: [String], style: Style = .tabs, symbols: [String?] = [],
    accessibilityLabels: [String] = [], counts: [Int?] = []
  ) {
    self.labels = labels
    self.symbols = labels.indices.map {
      style == .tabs && symbols.indices.contains($0) ? symbols[$0] : nil
    }
    let normalizedCounts: [Int?] = labels.indices.map {
      style == .tabs && counts.indices.contains($0) ? counts[$0] : nil
    }
    self.counts = normalizedCounts
    self.accessibilityLabels = labels.indices.map { index in
      accessibilityLabels.indices.contains(index)
        ? accessibilityLabels[index]
        : labels[index] + (normalizedCounts[index].map { "（\($0)）" } ?? "")
    }
    self.style = style
    super.init(frame: .zero)
    focusRingType = .none
    clipsToBounds = false
    wantsLayer = true
    layer?.masksToBounds = false
    updateModeShadow()
    setAccessibilityElement(true)
    setAccessibilityRole(.radioGroup)
    setAccessibilityLabel(self.accessibilityLabels.joined(separator: " / "))
    buttons = labels.enumerated().map { i, label in
      let button = TabHitButton(owner: self)
      button.title = label + (self.counts[i].map { "（\($0)）" } ?? "")
      button.setAccessibilityLabel(self.accessibilityLabels[i])
      if let symbol = self.symbols[i] {
        button.image = PaxIcon.image(symbol, size: PaxTheme.Tabs.labelFont)
      }
      button.tag = i
      button.isBordered = false
      button.focusRingType = .none
      button.target = self
      button.action = #selector(pressTab(_:))
      button.state = i == selectedIndex ? .on : .off
      addSubview(button)
      return button
    }
    setAccessibilityChildren(buttons)
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override var isFlipped: Bool { true }
  public override var acceptsFirstResponder: Bool { isEnabled }
  public override var intrinsicContentSize: NSSize {
    style == .mode
      ? PaxTheme.Dashboard.modeSize
      : NSSize(
        width: contentWidths.reduce(0, +) + CGFloat(max(0, labels.count - 1))
          * PaxTheme.Tabs.itemSpacing + PaxTheme.Dashboard.tabPadding * 2,
        height: PaxTheme.Dashboard.tabHeight)
  }
  private func tabTextColor(selected: Bool) -> NSColor {
    isEnabled && selected ? PaxTheme.Color.text : PaxTheme.Color.textSecondary
  }
  private func text(at index: Int, selected: Bool) -> NSAttributedString {
    let color = tabTextColor(selected: selected)
    let content = NSMutableAttributedString(
      string: labels[index],
      attributes: [
        .font: PaxTheme.Typography.label(size: PaxTheme.Tabs.labelFont),
        .foregroundColor: color,
      ])
    if let count = counts[index] {
      content.append(
        NSAttributedString(
          string: "（\(count)）",
          attributes: [
            .font: PaxTheme.Typography.label(size: PaxTheme.Tabs.countFont),
            .foregroundColor: isEnabled ? PaxTheme.Tabs.countColor : color,
          ]))
    }
    return content
  }
  // Labels, counts, symbols and the measurement font are fixed for this instance.
  private lazy var contentWidths: [CGFloat] = {
    labels.indices.map { index in
      let width = text(at: index, selected: true).size().width
      let icon = symbols[index] == nil ? 0 : PaxTheme.Tabs.labelFont + PaxTheme.Tabs.iconGap
      return ceil(width + icon) + PaxTheme.Tabs.horizontalInset * 2
    }
  }()
  private var segmentRects: [NSRect] {
    let inset = style == .mode ? PaxTheme.Dashboard.modePadding : PaxTheme.Dashboard.tabPadding
    let widths =
      style == .mode || distributesEqually
      ? Array(
        repeating: max(0, bounds.width - inset * 2) / CGFloat(max(1, labels.count)),
        count: labels.count)
      : contentWidths
    var x = inset
    return widths.map { width in
      defer { x += width + (style == .tabs && !distributesEqually ? PaxTheme.Tabs.itemSpacing : 0) }
      return NSRect(x: x, y: inset, width: width, height: max(0, bounds.height - inset * 2))
    }
  }
  /// 可变宽度页签按真实选中项滚入可见区域。
  public func revealSelection() {
    let rects = segmentRects
    if rects.indices.contains(selectedIndex) { scrollToVisible(rects[selectedIndex]) }
  }
  private func thumbRect(at position: CGFloat, in rects: [NSRect]) -> NSRect {
    guard !rects.isEmpty else { return .zero }
    let clamped = min(CGFloat(rects.count - 1), max(0, position))
    let left = Int(clamped.rounded(.down))
    let right = min(left + 1, rects.count - 1)
    let progress = clamped - CGFloat(left)
    let from = rects[left]
    let to = rects[right]
    return NSRect(
      x: from.minX + (to.minX - from.minX) * progress, y: from.minY,
      width: from.width + (to.width - from.width) * progress, height: from.height)
  }
  public override func draw(_ dirtyRect: NSRect) {
    let d = PaxTheme.Dashboard.self
    let c = PaxTheme.DashboardColor.self
    let mode = style == .mode
    let proxy = selectedIndex == 0
    let radius = mode ? bounds.height / 2 : d.tabRadius
    let edgeInset = PaxTheme.Layout.cardBorder / 2
    let trackBounds = NSRect(
      origin: bounds.origin,
      size: NSSize(
        width: mode || distributesEqually
          ? bounds.width : min(bounds.width, intrinsicContentSize.width),
        height: bounds.height))
    let track = NSBezierPath(
      roundedRect: trackBounds.insetBy(dx: edgeInset, dy: edgeInset), xRadius: radius,
      yRadius: radius)
    if mode {
      let colors = proxy ? c.modeProxy : c.modeDirect
      drawModeGradient(
        colors, middle: PaxTheme.Mode.middleStop,
        in: NSBezierPath(roundedRect: bounds, xRadius: radius, yRadius: radius))
      PaxSurfaceHighlight.draw(
        in: bounds.insetBy(dx: PaxTheme.Layout.cardBorder, dy: PaxTheme.Layout.cardBorder),
        radius: radius, topOpacity: PaxTheme.Mode.trackHighlight,
        sideOpacity: 0, bottomShade: proxy ? c.modeTrackShade : c.modeDirectTrackShade,
        blur: PaxTheme.Mode.innerShadowBlur)
      (proxy ? c.modeProxyEdge : c.modeDirectEdge).setStroke()
      track.lineWidth = PaxTheme.Layout.cardBorder
      track.stroke()
    } else {
      PaxTheme.Tabs.trackFill.setFill()
      track.fill()
      PaxSurfaceHighlight.draw(
        in: track.bounds, radius: radius, topOpacity: 0, sideOpacity: 0,
        blur: PaxTheme.Tabs.trackInnerBlur, topShade: PaxTheme.Tabs.trackTopShadow)
      // 底部只描白色细边，沿圆角连续绘制，不叠加模糊阴影。
      NSGraphicsContext.saveGraphicsState()
      NSBezierPath(
        rect: NSRect(
          x: track.bounds.minX, y: track.bounds.maxY - radius,
          width: track.bounds.width, height: radius + PaxTheme.Layout.cardBorder
        )
      ).addClip()
      PaxTheme.Tabs.trackBottomBorder.setStroke()
      track.lineWidth = PaxTheme.Layout.cardBorder
      track.stroke()
      NSGraphicsContext.restoreGraphicsState()
    }
    let rects = segmentRects
    let thumbRect = thumbRect(at: dragPosition ?? thumbPosition, in: rects)
    let thumb = NSBezierPath(
      roundedRect: thumbRect, xRadius: mode ? thumbRect.height / 2 : d.tabRadius,
      yRadius: mode ? thumbRect.height / 2 : d.tabRadius)
    NSGraphicsContext.saveGraphicsState()
    let shadow = NSShadow()
    shadow.shadowColor = (mode ? c.modeThumbShadow : PaxTheme.Color.shadow).withAlphaComponent(
      mode ? PaxTheme.Mode.thumbShadowOpacity : d.tabShadowOpacity)
    shadow.shadowBlurRadius = mode ? PaxTheme.Mode.shadowBlur : PaxTheme.Shadow.cardRadius
    shadow.shadowOffset = mode ? PaxTheme.Mode.shadowOffset : PaxTheme.Shadow.cardOffset
    shadow.set()
    if mode {
      let colors = proxy ? c.modeProxyThumb : c.modeDirectThumb
      // NSGradient 不为路径投影；先填轮廓生成阴影，再覆盖渐变。
      colors[0].setFill()
      thumb.fill()
      NSGraphicsContext.restoreGraphicsState()
      NSGraphicsContext.saveGraphicsState()
      drawModeGradient(colors, middle: PaxTheme.Mode.thumbMiddleStop, in: thumb)
    } else {
      PaxTheme.Color.control.setFill()
      thumb.fill()
      NSGraphicsContext.restoreGraphicsState()
      NSGraphicsContext.saveGraphicsState()
      NSGradient(starting: PaxTheme.Color.control, ending: PaxTheme.Tabs.thumbBottom)?
        .draw(in: thumb, angle: PaxTheme.Tabs.gradientAngle)
    }
    NSGraphicsContext.restoreGraphicsState()
    if mode {
      PaxSurfaceHighlight.draw(
        in: thumbRect, radius: thumbRect.height / 2, topOpacity: PaxTheme.Mode.thumbHighlight,
        sideOpacity: 0, bottomShade: c.modeThumbShade, blur: PaxTheme.Mode.innerShadowBlur)
    } else {
      PaxSurfaceHighlight.draw(
        in: thumbRect, radius: d.tabRadius,
        topOpacity: PaxTheme.Tabs.thumbTopHighlight,
        sideOpacity: PaxTheme.Tabs.thumbSideHighlight,
        bottomShade: PaxTheme.Tabs.thumbBottomShade, blur: PaxTheme.Tabs.thumbInnerBlur)
    }
    for (i, label) in labels.enumerated() {
      let color =
        mode
        ? (proxy
          ? (i == selectedIndex ? c.modeProxyText : c.modeProxyInactiveText)
          : (i == selectedIndex ? c.modeDirectText : c.modeDirectInactiveText))
        : tabTextColor(selected: i == selectedIndex)
      let content =
        mode
        ? NSAttributedString(
          string: label,
          attributes: [
            .font: PaxTheme.Mode.labelFont(selected: i == selectedIndex),
            .foregroundColor: isEnabled ? color : PaxTheme.Color.textSecondary,
          ])
        : text(at: i, selected: i == selectedIndex)
      let size = content.size()
      let iconSize = PaxTheme.Tabs.labelFont
      let glyphWidth = symbols[i] == nil ? 0 : iconSize + PaxTheme.Tabs.iconGap
      let x = rects[i].midX - (size.width + glyphWidth) / 2
      if let symbol = symbols[i], let image = PaxIcon.image(symbol, size: iconSize) {
        PaxRouteDrawing.icon(
          image,
          in: NSRect(x: x, y: (bounds.height - iconSize) / 2, width: iconSize, height: iconSize),
          color: isEnabled ? color : PaxTheme.Color.textSecondary)
      }
      content.draw(at: NSPoint(x: x + glyphWidth, y: (bounds.height - size.height) / 2))
    }
  }

  private func updateModeShadow() {
    guard style == .mode else { return }
    let outer = NSShadow()
    outer.shadowColor =
      (selectedIndex == 0
      ? PaxTheme.DashboardColor.modeTrackShadow : PaxTheme.DashboardColor.modeDirectTrackShadow)
      .withAlphaComponent(PaxTheme.Mode.trackShadowOpacity)
    outer.shadowBlurRadius = PaxTheme.Mode.shadowBlur
    outer.shadowOffset = PaxTheme.Mode.shadowOffset
    shadow = outer
  }
  private func drawModeGradient(_ colors: [NSColor], middle: CGFloat, in path: NSBezierPath) {
    let locations: [CGFloat] = [0, middle, 1]
    NSGradient(colors: colors, atLocations: locations, colorSpace: .sRGB)?
      .draw(in: path, angle: PaxTheme.Mode.verticalAngle)
  }
  public override func mouseDown(with event: NSEvent) {
    guard isEnabled else { return }
    window?.makeFirstResponder(self)
    cancelThumbAnimation()
    startX = convert(event.locationInWindow, from: nil).x
    dragPosition = nil
  }
  public override func mouseDragged(with event: NSEvent) {
    guard isEnabled, style == .mode else { return }
    let x = convert(event.locationInWindow, from: nil).x
    if abs(x - startX) > PaxTheme.Dashboard.dragThreshold {
      dragPosition = min(1, max(0, CGFloat(selectedIndex) + (x - startX) / (bounds.width / 2)))
      needsDisplay = true
    }
  }
  public override func mouseUp(with event: NSEvent) {
    guard isEnabled else { return }
    let x = convert(event.locationInWindow, from: nil).x
    let rects = segmentRects
    let next: Int
    if let dragPosition {
      next = Int(dragPosition.rounded())
    } else if let hit = rects.firstIndex(where: {
      let halfGap = style == .tabs ? PaxTheme.Tabs.itemSpacing / 2 : 0
      return x >= $0.minX - halfGap && x < $0.maxX + halfGap
    }) {
      next = hit
    } else {
      next = x < (rects.first?.minX ?? 0) ? 0 : labels.count - 1
    }
    if let dragPosition { thumbPosition = dragPosition }
    dragPosition = nil
    select(min(labels.count - 1, max(0, next)))
  }
  public override func keyDown(with event: NSEvent) {
    switch event.keyCode {
    case 123: select(max(0, selectedIndex - 1))
    case 124: select(min(labels.count - 1, selectedIndex + 1))
    case 49: select((selectedIndex + 1) % max(1, labels.count))
    default: super.keyDown(with: event)
    }
  }
  public override func layout() {
    super.layout()
    for (button, rect) in zip(buttons, segmentRects) {
      button.frame = NSRect(x: rect.minX, y: 0, width: rect.width, height: bounds.height)
    }
    needsDisplay = true
  }
  @objc private func pressTab(_ sender: NSButton) { select(sender.tag) }
  public override func viewDidMoveToWindow() {
    super.viewDidMoveToWindow()
    if window == nil { settleThumb() }
  }
  private func select(_ index: Int) {
    guard isEnabled else { return }
    if selectedIndex == index { animateThumb(to: CGFloat(index)) }
    selectedIndex = index
    onSelect?(index)
  }
}

private final class TabHitButton: NSButton {
  private weak var owner: PaxTabs?
  init(owner: PaxTabs) {
    self.owner = owner
    super.init(frame: .zero)
  }
  @available(*, unavailable)
  required init?(coder: NSCoder) { fatalError("代码构建") }
  override func draw(_ dirtyRect: NSRect) {}
  override func mouseDown(with event: NSEvent) { owner?.mouseDown(with: event) }
  override func mouseDragged(with event: NSEvent) { owner?.mouseDragged(with: event) }
  override func mouseUp(with event: NSEvent) { owner?.mouseUp(with: event) }
}
