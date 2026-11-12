import AppKit

/// 同一中性选中态用于侧栏与组件目录；NSButton 保留点击、键盘和辅助访问。
public final class PaxNavigationButton: NSButton {
  private var hovered = false
  public var isSelected = false { didSet { updateAppearance() } }
  public init(title: String, symbol: String, target: AnyObject? = nil, action: Selector? = nil) {
    super.init(frame: .zero)
    focusRingType = .none
    cell = PaxNavigationCell(textCell: title)
    self.title = title
    self.target = target
    self.action = action
    image = NSImage(systemSymbolName: symbol, accessibilityDescription: nil)
    image?.isTemplate = true
    imagePosition = .imageLeading
    alignment = .left
    imageHugsTitle = true
    isBordered = false
    font = PaxTheme.Navigation.font
    wantsLayer = true
    clipsToBounds = false
    layer?.masksToBounds = false
    setAccessibilityLabel(title)
    updateAppearance()
  }
  @available(*, unavailable)
  public required init?(coder: NSCoder) { fatalError("代码构建") }
  public override func updateTrackingAreas() {
    super.updateTrackingAreas()
    for area in trackingAreas { removeTrackingArea(area) }
    addTrackingArea(
      NSTrackingArea(
        rect: bounds, options: [.mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect],
        owner: self))
  }
  public override func mouseEntered(with event: NSEvent) {
    hovered = true
    updateAppearance()
  }
  public override func mouseExited(with event: NSEvent) {
    hovered = false
    updateAppearance()
  }
  public override func layout() {
    super.layout()
    layer?.shadowPath = CGPath(
      roundedRect: bounds, cornerWidth: PaxTheme.Navigation.radius,
      cornerHeight: PaxTheme.Navigation.radius, transform: nil)
  }
  public override var intrinsicContentSize: NSSize {
    NSSize(
      width: super.intrinsicContentSize.width + PaxTheme.Navigation.contentInset * 2,
      height: PaxTheme.Navigation.rowHeight)
  }
  private func updateAppearance() {
    contentTintColor = isSelected ? PaxTheme.Navigation.selectedText : PaxTheme.Navigation.text
    effectiveAppearance.performAsCurrentDrawingAppearance {
      layer?.backgroundColor =
        isSelected
        ? PaxTheme.Navigation.selected.cgColor
        : hovered && isEnabled ? PaxTheme.Navigation.hover.cgColor : NSColor.clear.cgColor
      layer?.cornerRadius = PaxTheme.Navigation.radius
      layer?.shadowOpacity =
        isSelected ? PaxTheme.Navigation.shadowOpacity : 0
      layer?.shadowRadius = PaxTheme.Navigation.shadowRadius
      layer?.shadowOffset = PaxTheme.Navigation.shadowOffset
      layer?.shadowColor = PaxTheme.Color.shadow.cgColor
      if isSelected {
        let effect = NSShadow()
        effect.shadowColor = PaxTheme.Color.shadow.withAlphaComponent(
          CGFloat(PaxTheme.Navigation.shadowOpacity))
        effect.shadowBlurRadius = PaxTheme.Navigation.shadowRadius
        effect.shadowOffset = PaxTheme.Navigation.shadowOffset
        shadow = effect
      } else {
        shadow = nil
      }
    }
    needsDisplay = true
    setAccessibilityValue(isSelected ? "已选中" : "未选中")
  }
  public override func viewDidChangeEffectiveAppearance() {
    super.viewDidChangeEffectiveAppearance()
    updateAppearance()
  }
}

/// NSButtonCell 保留系统 focus/pressed 与标题绘制，仅调整图标和文字几何。
@MainActor
private final class PaxNavigationCell: NSButtonCell {
  override func imageRect(forBounds rect: NSRect) -> NSRect {
    NSRect(
      x: rect.minX + PaxTheme.Navigation.contentInset,
      y: rect.midY - PaxTheme.Navigation.iconSize / 2,
      width: PaxTheme.Navigation.iconSize, height: PaxTheme.Navigation.iconSize)
  }
  override func titleRect(forBounds rect: NSRect) -> NSRect {
    let original = super.titleRect(forBounds: rect)
    let x =
      rect.minX + PaxTheme.Navigation.contentInset + PaxTheme.Navigation.iconSize
      + PaxTheme.Navigation.iconGap
    return NSRect(
      x: x, y: original.minY,
      width: max(0, rect.maxX - x - PaxTheme.Navigation.contentInset), height: original.height)
  }
}
