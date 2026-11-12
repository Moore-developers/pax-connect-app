import AppKit

/// 对象行共享图标和名称布局；名称可为标签或按钮，不认识对象目录或业务 Store。
@MainActor public final class PaxObjectIdentityView: NSView {
  private let icon: NSImageView
  private var assignedImage: NSImage
  private let content: NSView
  private let iconSize: CGFloat
  private let gap: CGFloat
  public init(
    image: NSImage, content: NSView, iconSize: CGFloat = 20,
    gap: CGFloat = PaxTheme.Spacing.tight, tint: NSColor? = nil
  ) {
    assignedImage = image
    icon = NSImageView(image: PaxImageInk.normalized(image))
    self.content = content
    self.iconSize = iconSize
    self.gap = gap
    super.init(frame: .zero)
    icon.imageScaling = .scaleProportionallyUpOrDown
    icon.wantsLayer = true
    icon.layer?.shadowColor = PaxTheme.Color.text.cgColor
    icon.layer?.shadowOpacity = PaxTheme.Dashboard.objectShadowOpacity
    icon.layer?.shadowRadius = PaxTheme.Dashboard.objectShadowRadius
    icon.layer?.shadowOffset = PaxTheme.Dashboard.objectShadowOffset
    icon.contentTintColor = image.isTemplate ? tint : nil
    addSubview(icon)
    addSubview(content)
  }
  @available(*, unavailable) public required init?(coder: NSCoder) { fatalError("代码构建") }
  public func setImage(_ image: NSImage) {
    guard image !== assignedImage else { return }
    assignedImage = image
    icon.image = PaxImageInk.normalized(image)
    icon.contentTintColor = image.isTemplate ? PaxTheme.Color.textSecondary : nil
  }
  public override var isFlipped: Bool { true }
  public override var intrinsicContentSize: NSSize {
    NSSize(
      width: iconSize + gap + max(0, content.intrinsicContentSize.width),
      height: max(iconSize, content.intrinsicContentSize.height))
  }
  public override func layout() {
    super.layout()
    icon.frame = NSRect(x: 0, y: (bounds.height - iconSize) / 2, width: iconSize, height: iconSize)
    let height = max(0, content.intrinsicContentSize.height)
    content.frame = NSRect(
      x: iconSize + gap, y: (bounds.height - height) / 2,
      width: max(0, bounds.width - iconSize - gap), height: height)
  }
}
